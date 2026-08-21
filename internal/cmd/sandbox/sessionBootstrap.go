package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

const bannerArt = `
                              #####
                              #######
  ----                #################
------------      ######################
-------------   ########################
  ----------- #########################
          -- #######          #######
             ######           #####
            ######
            ######
           ######             -----
          ####### --          -------
  ############## ----------------------
##############   -----------------------
############      ----------------------
  ######                ---------------
                              -------
                              -----

             R E T A S K

`

// SessionBootstrap performs per-session setup for a Private VM sandbox:
// creates the session folder, writes agent configs, clones git repos, and
// builds the process environment.
type SessionBootstrap struct {
	SessionID    string
	SessionName  string
	SandboxID    string
	SandboxName  string
	WorkspaceID  string
	Config       *sandboxv1.Sandbox_Config
	SystemPrompt string
	SeedPrompt   string
	Endpoint     string
	BaseDir      string // directory where `retask sandbox connect` was invoked
	Log          *slog.Logger
}

// deriveTargetDir returns the default clone directory name for a repo URL —
// the last path/colon segment with a trailing .git stripped.
func deriveTargetDir(url string) string {
	url = strings.TrimRight(url, "/")
	parts := strings.FieldsFunc(url, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) == 0 {
		return "repo"
	}
	last := parts[len(parts)-1]
	return strings.TrimSuffix(last, ".git")
}

// normalizeGitRepoURL rewrites SSH scp-style git URLs (git@host:owner/repo) —
// GitHub, GitLab, or any host — to their HTTPS form (https://host/owner/repo) so
// the host-scoped auth header from gitTokenEnv applies. Already-HTTPS and other
// URLs are returned unchanged. The token is never placed in the URL.
func normalizeGitRepoURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "git@") {
		if host, path, ok := strings.Cut(strings.TrimPrefix(rawURL, "git@"), ":"); ok {
			return "https://" + host + "/" + path
		}
	}
	return rawURL
}

// gitTokenEnv returns environment variables that inject host-scoped Authorization
// headers into git via config (GIT_CONFIG_COUNT, added in git 2.31). This keeps
// tokens out of the clone URL, process arguments, terminal output, and the cloned
// repo's .git/config — a token only ever lives in the child process environment.
// tokens maps a git host (e.g. "github.com") to its access token; hosts with an
// empty token are skipped. Returns nil when no non-empty token is provided.
func gitTokenEnv(tokens map[string]string) []string {
	hosts := make([]string, 0, len(tokens))
	for host, token := range tokens {
		if token != "" {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	sort.Strings(hosts) // deterministic GIT_CONFIG_* indices

	env := []string{fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(hosts))}
	for i, host := range hosts {
		auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tokens[host]))
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=http.https://%s/.extraHeader", i, host),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=Authorization: Basic %s", i, auth),
		)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0") // never block on an interactive credential prompt
	return env
}

// hostEnvDenylist are variables that must never be inherited from the host
// machine into a sandbox session. NWEB_API_KEY is the operator's PAT;
// NWEB_API_TOKEN / NWEB_WORKSPACE_ID identify the operator's own session. The
// session gets its own scoped values via the injected layer instead.
var hostEnvDenylist = map[string]bool{
	"NWEB_API_TOKEN":    true,
	"NWEB_API_KEY":      true,
	"NWEB_WORKSPACE_ID": true,
}

// hostTerminalEnvDenylist are variables that identify the terminal emulator
// the OPERATOR is sitting in when they run `retask sandbox connect`. A
// session's terminal is the browser's xterm.js on the other end of the
// session lane — never the operator's — so every one of these is a false
// statement about the session, and agents act on them.
//
// Claude Code is the worked example. Told it is in iTerm2 or Terminal.app
// (TERM_PROGRAM), it starts a 200ms poll whose only purpose is to notice the
// Cmd+K those emulators handle themselves and never forward: it asks for the
// cursor position and reads "row 1" as "the user wiped the screen", then
// submits /clear — discarding the conversation, not the display. A browser
// terminal never swallows Cmd+K, so the detector has nothing true to detect;
// it only has our scrollback replay, which parks the cursor at row 1 every
// time a viewer reconnects.
var hostTerminalEnvDenylist = map[string]bool{
	// Terminal program identity (what Claude Code's detector keys on).
	"TERM_PROGRAM":         true,
	"TERM_PROGRAM_VERSION": true,
	"__CFBundleIdentifier": true,
	"TERMINAL_EMULATOR":    true,
	"LC_TERMINAL":          true,
	"LC_TERMINAL_VERSION":  true,
	// Per-emulator session handles: stale ids pointing at the operator's window.
	"TERM_SESSION_ID":        true,
	"ITERM_SESSION_ID":       true,
	"ITERM_PROFILE":          true,
	"KITTY_WINDOW_ID":        true,
	"KITTY_PID":              true,
	"KITTY_LISTEN_ON":        true,
	"GHOSTTY_RESOURCES_DIR":  true,
	"GHOSTTY_BIN_DIR":        true,
	"ALACRITTY_LOG":          true,
	"ALACRITTY_SOCKET":       true,
	"ALACRITTY_WINDOW_ID":    true,
	"WEZTERM_PANE":           true,
	"WEZTERM_UNIX_SOCKET":    true,
	"WEZTERM_EXECUTABLE":     true,
	"KONSOLE_VERSION":        true,
	"KONSOLE_DBUS_SERVICE":   true,
	"KONSOLE_DBUS_SESSION":   true,
	"GNOME_TERMINAL_SERVICE": true,
	"GNOME_TERMINAL_SCREEN":  true,
	"VTE_VERSION":            true,
	"TERMINATOR_UUID":        true,
	"TILIX_ID":               true,
	"XTERM_VERSION":          true,
	"WT_SESSION":             true,
	"WT_PROFILE_ID":          true,
	"ZED_TERM":               true,
	// Multiplexers the operator may be inside; the session is not.
	"TMUX":                true,
	"TMUX_PANE":           true,
	"STY":                 true,
	"ZELLIJ":              true,
	"ZELLIJ_SESSION_NAME": true,
	"ZELLIJ_PANE_ID":      true,
	// Editor-embedded terminals.
	"CURSOR_TRACE_ID":               true,
	"VSCODE_GIT_ASKPASS_MAIN":       true,
	"VSCODE_GIT_ASKPASS_NODE":       true,
	"VSCODE_GIT_ASKPASS_EXTRA_ARGS": true,
	"VSCODE_GIT_IPC_HANDLE":         true,
	// Palette hints measured from the operator's window.
	"COLORFGBG": true,
}

// sessionTerminalEnv is the truthful replacement for the stripped identity:
// the session's terminal really is xterm.js, which reports itself as
// xterm-256color and renders 24-bit color. Applied in the host layer, so a
// deliberate Sandbox Config value still overrides it.
var sessionTerminalEnv = map[string]string{
	"TERM":      "xterm-256color",
	"COLORTERM": "truecolor",
}

// buildEnv merges three layers into a process environment slice.
// Later layers override earlier ones:
//  1. baseEnv  — host machine env (os.Environ()), minus hostEnvDenylist and
//     hostTerminalEnvDenylist, plus sessionTerminalEnv
//  2. config   — user-configured env vars from Sandbox_Config
//  3. injected — standard session vars that always override
func buildEnv(baseEnv []string, config *sandboxv1.Sandbox_Config, injected map[string]string) []string {
	env := make(map[string]string, len(baseEnv))
	for _, e := range baseEnv {
		k, v, _ := strings.Cut(e, "=")
		if hostEnvDenylist[k] {
			continue // never inherit operator auth/workspace vars from the host
		}
		if hostTerminalEnvDenylist[k] {
			continue // the operator's terminal is not the session's terminal
		}
		env[k] = v
	}
	for k, v := range sessionTerminalEnv {
		env[k] = v
	}
	for _, ev := range config.GetEnvVars() {
		if ev.GetKey() == "" {
			continue
		}
		val := ev.GetPlain()
		if s := ev.GetSecret(); s != nil && s.GetValue() != "" {
			val = s.GetValue()
		}
		env[ev.GetKey()] = val
	}
	for k, v := range injected {
		env[k] = v
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}

// writeTerm encodes text as a base64 terminal data frame and writes it to the
// session lane WebSocket. Safe to call concurrently: Conn.Write may be called
// concurrently with itself and with other Conn methods (only Read may not).
func writeTerm(ctx context.Context, conn *websocket.Conn, text string) {
	msg, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{"data", base64.StdEncoding.EncodeToString([]byte(text))})
	conn.Write(ctx, websocket.MessageText, msg) //nolint:errcheck
}

// Run performs all bootstrap steps before the PTY starts. It streams progress
// to conn as terminal output frames.
//
// Returns (sessionDir, envSlice, nil) on success — even when some git repos
// failed to set up; a failed repo is logged, skipped, and reported to the
// agent via CLAUDE.md/AGENTS.md rather than blocking the session.
// Returns ("", nil, err) on non-recoverable error (folder/config setup).
func (b *SessionBootstrap) Run(ctx context.Context, conn *websocket.Conn) (sessionDir string, env []string, err error) {
	sessionDir = filepath.Join(b.BaseDir, "session-"+b.SessionID)

	b.logInfo("session_bootstrap_starting", "session_id", b.SessionID)
	b.writeBanner(ctx, conn)

	if err = b.setupFolder(sessionDir); err != nil {
		return "", nil, fmt.Errorf("create session folder: %w", err)
	}
	if err = b.writeAgentConfigs(sessionDir); err != nil {
		return "", nil, fmt.Errorf("write agent configs: %w", err)
	}

	if len(b.Config.GetGitRepos()) > 0 {
		if failures := b.setupGitRepos(ctx, conn, sessionDir); len(failures) > 0 {
			if warnErr := b.appendGitRepoWarnings(sessionDir, failures); warnErr != nil {
				b.logError("session_repo_warning_append_failed", "session_id", b.SessionID, "error", warnErr)
			}
		}
	}

	env, err = b.buildSessionEnv()
	if err != nil {
		return "", nil, err
	}

	b.logInfo("session_bootstrap_complete", "session_id", b.SessionID)
	writeTerm(ctx, conn, "\r\n[retask] Session ready.\r\n\r\n")
	return sessionDir, env, nil
}

func (b *SessionBootstrap) setupFolder(sessionDir string) error {
	return os.MkdirAll(sessionDir, 0o755)
}

func (b *SessionBootstrap) writeAgentConfigs(sessionDir string) error {
	if err := os.WriteFile(filepath.Join(sessionDir, "CLAUDE.md"), []byte(b.SystemPrompt), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "AGENTS.md"), []byte(b.SystemPrompt), 0o644); err != nil {
		return err
	}
	claudeDir := filepath.Join(sessionDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}
	settings := `{"skipDangerousModePermissionPrompt":true,"enabledPlugins":{"superpowers@claude-plugins-official":true}}`
	return os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0o644)
}

// gitToken returns the value of the first configured env var among keys (secret
// value preferred over plain), or "" if none is set to a non-empty value.
func (b *SessionBootstrap) gitToken(keys ...string) string {
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	for _, ev := range b.Config.GetEnvVars() {
		if !want[ev.GetKey()] {
			continue
		}
		if s := ev.GetSecret(); s != nil && s.GetValue() != "" {
			return s.GetValue()
		}
		if v := ev.GetPlain(); v != "" {
			return v
		}
	}
	return ""
}

// setupGitRepos clones or updates every configured repo concurrently — one
// goroutine per repo, since they touch disjoint destination directories and
// independent git processes, so there's no shared state to race on. A repo
// that fails (after cloneOrFetchWithRetry's own retries) is logged and
// skipped rather than blocking the others: it never aborts the session.
// Returns one "<url>: <error>" message per failed repo, in Config order, or
// nil if every repo succeeded.
func (b *SessionBootstrap) setupGitRepos(ctx context.Context, conn *websocket.Conn, sessionDir string) []string {
	tokens := map[string]string{
		"github.com": b.gitToken("GITHUB_TOKEN", "GH_TOKEN"),
		"gitlab.com": b.gitToken("GITLAB_TOKEN"),
	}
	if tokens["github.com"] == "" && tokens["gitlab.com"] == "" {
		writeTerm(ctx, conn, "[repos] Warning: no GITHUB_TOKEN / GITLAB_TOKEN found — private repos may fail to clone\r\n")
	}
	tokenEnv := gitTokenEnv(tokens)

	repos := b.Config.GetGitRepos()
	urls := make([]string, len(repos))
	errs := make([]error, len(repos))

	var wg sync.WaitGroup
	for i, repo := range repos {
		targetDir := repo.GetTargetDir()
		if targetDir == "" {
			targetDir = deriveTargetDir(repo.GetUrl())
		}
		branch := repo.GetBranch()
		if branch == "" {
			branch = "main"
		}
		dest := filepath.Join(sessionDir, targetDir)
		cloneURL := normalizeGitRepoURL(repo.GetUrl())
		urls[i] = repo.GetUrl()

		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine only ever writes its own index — errs/urls need
			// no mutex — and Conn.Write (via writeTerm, inside the retry
			// helper) is safe to call concurrently with itself.
			errs[i] = b.cloneOrFetchWithRetry(ctx, conn, cloneURL, branch, dest, tokenEnv)
		}(i)
	}
	wg.Wait()

	var failures []string
	for i, err := range errs {
		if err == nil {
			continue
		}
		writeTerm(ctx, conn, fmt.Sprintf("\r\n[repos] %s: setup failed, skipping this repo: %v\r\n", urls[i], err))
		b.logError("session_repo_setup_failed", "session_id", b.SessionID, "url", urls[i], "error", err)
		failures = append(failures, fmt.Sprintf("%s: %v", urls[i], err))
	}
	return failures
}

// appendGitRepoWarnings appends a note to CLAUDE.md and AGENTS.md (both
// already written by writeAgentConfigs before repos were cloned) so the agent
// knows some repos failed setup and can raise it with the user — mirroring
// the CLOUD path's setup-warnings pipeline (sandbox-proxy's log.sh).
func (b *SessionBootstrap) appendGitRepoWarnings(sessionDir string, failures []string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n\n## Sandbox setup issues\nSetup had %d git repo(s) fail to clone/update; they were skipped so the session could still start:\n", len(failures))
	for _, f := range failures {
		fmt.Fprintf(&sb, "- %s\n", f)
	}
	sb.WriteString("Surface this to the user at the start of the conversation and ask them to fix the repo URL/branch/access.\n")
	note := []byte(sb.String())

	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		f, err := os.OpenFile(filepath.Join(sessionDir, name), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(note)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (b *SessionBootstrap) cloneOrFetchWithRetry(ctx context.Context, conn *websocket.Conn, url, branch, dest string, tokenEnv []string) error {
	if info, err := os.Stat(dest); err == nil && info.IsDir() {
		writeTerm(ctx, conn, fmt.Sprintf("[repos] %s exists, fetching latest...\r\n", dest))
		cmd := exec.CommandContext(ctx, "git", "-C", dest, "fetch", "--depth=1", "origin", branch)
		cmd.Env = append(os.Environ(), tokenEnv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			b.logError("session_repo_fetch_failed", "session_id", b.SessionID, "dest", dest, "error", err)
			return fmt.Errorf("fetch %s: %w\n%s", dest, err, strings.TrimSpace(string(out)))
		}
		cmd = exec.CommandContext(ctx, "git", "-C", dest, "reset", "--hard", "FETCH_HEAD")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.logError("session_repo_reset_failed", "session_id", b.SessionID, "dest", dest, "error", err)
			return fmt.Errorf("reset %s: %w\n%s", dest, err, strings.TrimSpace(string(out)))
		}
		writeTerm(ctx, conn, fmt.Sprintf("[repos] updated %s\r\n", dest))
		b.logInfo("session_repo_updated", "session_id", b.SessionID, "dest", dest)
		return nil
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		writeTerm(ctx, conn, fmt.Sprintf("\r\n[repos] cloning %s @ %s (attempt %d/3)...\r\n", url, branch, attempt))
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", "-b", branch, url, dest)
		cmd.Env = append(os.Environ(), tokenEnv...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			writeTerm(ctx, conn, fmt.Sprintf("[repos] cloned %s\r\n", dest))
			b.logInfo("session_repo_cloned", "session_id", b.SessionID, "dest", dest)
			return nil
		}
		lastErr = fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
		b.logError("session_repo_clone_failed", "session_id", b.SessionID, "dest", dest, "attempt", attempt, "error", err)
		writeTerm(ctx, conn, fmt.Sprintf("[repos] attempt %d failed: %v\r\n", attempt, err))
		if attempt < 3 {
			time.Sleep(time.Second)
		}
	}
	return lastErr
}

func (b *SessionBootstrap) writeBanner(ctx context.Context, conn *websocket.Conn) {
	// Convert Unix newlines to terminal CRLF.
	art := strings.ReplaceAll(bannerArt, "\n", "\r\n")
	info := fmt.Sprintf("  Workspace : %s\r\n  Sandbox   : %s\r\n  Session   : %s\r\n\r\n",
		b.WorkspaceID, b.SandboxID, b.SessionID)
	writeTerm(ctx, conn, art+info)
}

func (b *SessionBootstrap) logInfo(msg string, args ...any) {
	if b.Log != nil {
		b.Log.Info(msg, args...)
	}
}

func (b *SessionBootstrap) logError(msg string, args ...any) {
	if b.Log != nil {
		b.Log.Error(msg, args...)
	}
}

func (b *SessionBootstrap) buildSessionEnv() ([]string, error) {
	injected := map[string]string{
		"SESSION_ID":               b.SessionID,
		"NWEB_WORKSPACE_ID":        b.WorkspaceID,
		"NWEB_API_ENDPOINT":        b.Endpoint,
		"NWEB_API_TRANSPORT":       "http",
		"RETASK_NO_PERSIST":        "true",
		"IS_SANDBOX":               "1",
		"CLAUDE_CODE_EFFORT_LEVEL": "xhigh",
		"SEED_PROMPT":              b.SeedPrompt,
	}
	// NWEB_API_TOKEN is deliberately NOT injected here. The backend mints a
	// per-session token scoped to the session's creator (the user who created
	// the session, with a long TTL) and delivers it inside Config.EnvVars.
	// Injecting the connect operator's own JWT would override it, so every
	// user's session on a shared Private VM would run under the operator's
	// short-lived identity. Let the Config value flow through the config layer.
	env := buildEnv(os.Environ(), b.Config, injected)
	if envValue(env, "NWEB_API_TOKEN") == "" {
		return nil, errors.New("NWEB_API_TOKEN missing from sandbox config: backend did not provision a per-session token")
	}
	return env, nil
}

// envValue returns the value for key in a KEY=VALUE environment slice, or ""
// if the key is absent. buildEnv produces at most one entry per key.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, prefix); ok {
			return v
		}
	}
	return ""
}
