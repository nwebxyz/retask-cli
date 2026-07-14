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
	"time"

	"github.com/coder/websocket"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// ErrAborted is returned by SessionBootstrap.Run when the user chooses Exit
// from the failure menu. The caller must close the session lane without
// starting a PTY.
var ErrAborted = errors.New("session aborted by user")

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
	JWT          string
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

// buildEnv merges three layers into a process environment slice.
// Later layers override earlier ones:
//  1. baseEnv  — host machine env (os.Environ()), minus hostEnvDenylist
//  2. config   — user-configured env vars from Sandbox_Config
//  3. injected — standard session vars that always override
func buildEnv(baseEnv []string, config *sandboxv1.Sandbox_Config, injected map[string]string) []string {
	env := make(map[string]string, len(baseEnv))
	for _, e := range baseEnv {
		k, v, _ := strings.Cut(e, "=")
		if hostEnvDenylist[k] {
			continue // never inherit operator auth/workspace vars from the host
		}
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

// parseChoiceFrom returns the first valid menu choice (1, 2, or 3) found in b,
// or 0 if none is present.
func parseChoiceFrom(b []byte) int {
	for _, c := range b {
		switch c {
		case '1':
			return 1
		case '2':
			return 2
		case '3':
			return 3
		}
	}
	return 0
}

// writeTerm encodes text as a base64 terminal data frame and writes it to the
// session lane WebSocket.
func writeTerm(ctx context.Context, conn *websocket.Conn, text string) {
	msg, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{"data", base64.StdEncoding.EncodeToString([]byte(text))})
	conn.Write(ctx, websocket.MessageText, msg) //nolint:errcheck
}

// readChoice reads keyboard input from the session lane and returns the user's
// choice (1/2/3). Keystrokes are echoed back to the terminal. Returns 2
// (continue) on any read error so a disconnected client does not block forever.
func readChoice(ctx context.Context, conn *websocket.Conn) int {
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return 2
		}
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.Type != "data" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil || len(b) == 0 {
			continue
		}
		writeTerm(ctx, conn, string(b))
		if choice := parseChoiceFrom(b); choice != 0 {
			writeTerm(ctx, conn, "\r\n")
			return choice
		}
		for _, c := range b {
			if c == '\r' || c == '\n' {
				writeTerm(ctx, conn, "\r\n")
				return 2
			}
		}
	}
}

// Run performs all bootstrap steps before the PTY starts. It streams progress
// to conn as terminal output frames.
//
// Returns (sessionDir, envSlice, nil) on success.
// Returns ("", nil, ErrAborted) if the user chooses Exit from the failure menu.
// Returns ("", nil, err) on non-recoverable error.
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
		for {
			err = b.setupGitRepos(ctx, conn, sessionDir)
			if err == nil {
				break
			}
			writeTerm(ctx, conn, fmt.Sprintf("\r\n[retask] Git repo setup failed: %v\r\n", err))
			writeTerm(ctx, conn, "\r\n  1) Retry\r\n  2) Continue (skip repos)\r\n  3) Exit\r\n\r\nSelection [2]: ")
			choice := readChoice(ctx, conn)
			if choice == 3 {
				return "", nil, ErrAborted
			}
			if choice != 1 {
				break
			}
		}
	}

	env = b.buildSessionEnv()

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

func (b *SessionBootstrap) setupGitRepos(ctx context.Context, conn *websocket.Conn, sessionDir string) error {
	tokens := map[string]string{
		"github.com": b.gitToken("GITHUB_TOKEN", "GH_TOKEN"),
		"gitlab.com": b.gitToken("GITLAB_TOKEN"),
	}
	if tokens["github.com"] == "" && tokens["gitlab.com"] == "" {
		writeTerm(ctx, conn, "[repos] Warning: no GITHUB_TOKEN / GITLAB_TOKEN found — private repos may fail to clone\r\n")
	}
	tokenEnv := gitTokenEnv(tokens)

	for _, repo := range b.Config.GetGitRepos() {
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

		if err := b.cloneOrFetchWithRetry(ctx, conn, cloneURL, branch, dest, tokenEnv); err != nil {
			return fmt.Errorf("clone %s: %w", repo.GetUrl(), err)
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

func (b *SessionBootstrap) buildSessionEnv() []string {
	injected := map[string]string{
		"SESSION_ID":               b.SessionID,
		"NWEB_API_TOKEN":           b.JWT,
		"NWEB_WORKSPACE_ID":        b.WorkspaceID,
		"NWEB_API_ENDPOINT":        b.Endpoint,
		"NWEB_API_TRANSPORT":       "http",
		"RETASK_NO_PERSIST":        "true",
		"IS_SANDBOX":               "1",
		"CLAUDE_CODE_EFFORT_LEVEL": "xhigh",
		"SEED_PROMPT":              b.SeedPrompt,
	}
	return buildEnv(os.Environ(), b.Config, injected)
}
