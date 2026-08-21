package sandbox

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integrationv1 "github.com/nwebxyz/retask-cli/proto-gen/integration/v1"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// --- deriveTargetDir ---

func TestDeriveTargetDir(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:nwebxyz/api-contracts.git", "api-contracts"},
		{"https://github.com/foo/bar.git", "bar"},
		{"https://github.com/foo/bar", "bar"},
		{"https://github.com/foo/bar/", "bar"},
		{"https://example.com/deep/path/repo.git", "repo"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, deriveTargetDir(tc.url), "url=%q", tc.url)
	}
}

// --- normalizeGitRepoURL ---

func TestNormalizeGitRepoURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{
			"git@github.com:foo/bar.git",
			"https://github.com/foo/bar.git", // github ssh form rewritten to https
		},
		{
			"git@gitlab.com:foo/bar.git",
			"https://gitlab.com/foo/bar.git", // gitlab ssh form rewritten to https
		},
		{
			"git@gitlab.example.com:group/sub/repo.git",
			"https://gitlab.example.com/group/sub/repo.git", // self-hosted host rewritten too
		},
		{
			"https://github.com/foo/bar.git",
			"https://github.com/foo/bar.git", // already https: unchanged
		},
		{
			"https://gitlab.com/foo/bar.git",
			"https://gitlab.com/foo/bar.git", // already https: unchanged
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, normalizeGitRepoURL(tc.url), "url=%q", tc.url)
	}
}

// --- gitTokenEnv ---

func TestGitTokenEnv_NoTokens(t *testing.T) {
	assert.Nil(t, gitTokenEnv(nil))
	assert.Nil(t, gitTokenEnv(map[string]string{"github.com": "", "gitlab.com": ""}))
}

func TestGitTokenEnv_InjectsScopedAuthHeader(t *testing.T) {
	token := "ghp_TOKEN"
	m := envToMap(gitTokenEnv(map[string]string{"github.com": token}))

	assert.Equal(t, "1", m["GIT_CONFIG_COUNT"])
	assert.Equal(t, "http.https://github.com/.extraHeader", m["GIT_CONFIG_KEY_0"])
	assert.Equal(t, "0", m["GIT_TERMINAL_PROMPT"])

	// The header value decodes to x-access-token:<token>.
	const prefix = "Authorization: Basic "
	val := m["GIT_CONFIG_VALUE_0"]
	assert.True(t, strings.HasPrefix(val, prefix), "value=%q", val)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(val, prefix))
	assert.NoError(t, err)
	assert.Equal(t, "x-access-token:"+token, string(decoded))
}

func TestGitTokenEnv_MultiHost(t *testing.T) {
	m := envToMap(gitTokenEnv(map[string]string{
		"github.com": "ghp_GITHUB",
		"gitlab.com": "glpat_GITLAB",
	}))

	assert.Equal(t, "2", m["GIT_CONFIG_COUNT"])
	// Hosts are sorted for deterministic indices: github.com < gitlab.com.
	assert.Equal(t, "http.https://github.com/.extraHeader", m["GIT_CONFIG_KEY_0"])
	assert.Equal(t, "http.https://gitlab.com/.extraHeader", m["GIT_CONFIG_KEY_1"])

	const prefix = "Authorization: Basic "
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(m["GIT_CONFIG_VALUE_1"], prefix))
	assert.NoError(t, err)
	assert.Equal(t, "x-access-token:glpat_GITLAB", string(decoded))
}

func TestGitTokenEnv_NeverLeaksRawToken(t *testing.T) {
	token := "ghp_SECRET"
	for _, e := range gitTokenEnv(map[string]string{"github.com": token}) {
		assert.NotContains(t, e, token, "raw token leaked in env entry: %q", e)
	}
}

// --- buildEnv ---

func TestBuildEnv_HostLayer(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}
	cfg := &sandboxv1.Sandbox_Config{}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "/usr/bin", m["PATH"])
	assert.Equal(t, "/root", m["HOME"])
}

func TestBuildEnv_ConfigOverridesHost(t *testing.T) {
	base := []string{"MY_VAR=host_value"}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "MY_VAR", Plain: "config_value"},
		},
	}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "config_value", m["MY_VAR"])
}

func TestBuildEnv_SecretOverridesPlain(t *testing.T) {
	base := []string{}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{
				Key:   "API_KEY",
				Plain: "not-this",
				Secret: &sandboxv1.Sandbox_Config_EnvVar_SecretValue{
					Value: "real-secret",
				},
			},
		},
	}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "real-secret", m["API_KEY"])
}

func TestBuildEnv_InjectedWinsAll(t *testing.T) {
	base := []string{"SESSION_ID=old"}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "SESSION_ID", Plain: "also-old"},
		},
	}
	injected := map[string]string{"SESSION_ID": "injected"}
	env := buildEnv(base, cfg, injected)
	m := envToMap(env)
	assert.Equal(t, "injected", m["SESSION_ID"])
}

func TestBuildEnv_StripsHostAuthVars(t *testing.T) {
	// Host-provided auth/workspace vars must never leak into the session.
	base := []string{
		"PATH=/usr/bin",
		"NWEB_API_TOKEN=host-jwt",
		"NWEB_API_KEY=host-pat",
		"NWEB_WORKSPACE_ID=host-ws",
	}
	cfg := &sandboxv1.Sandbox_Config{}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)

	assert.Equal(t, "/usr/bin", m["PATH"], "unrelated host vars must survive")
	_, hasToken := m["NWEB_API_TOKEN"]
	_, hasKey := m["NWEB_API_KEY"]
	_, hasWS := m["NWEB_WORKSPACE_ID"]
	assert.False(t, hasToken, "NWEB_API_TOKEN must be stripped from host layer")
	assert.False(t, hasKey, "NWEB_API_KEY must be stripped from host layer")
	assert.False(t, hasWS, "NWEB_WORKSPACE_ID must be stripped from host layer")
}

func TestBuildEnv_InjectedSetsStrippedAuthVars(t *testing.T) {
	// Stripping the host layer must not block injected session values.
	base := []string{"NWEB_API_TOKEN=host-jwt", "NWEB_WORKSPACE_ID=host-ws"}
	cfg := &sandboxv1.Sandbox_Config{}
	injected := map[string]string{
		"NWEB_API_TOKEN":    "session-jwt",
		"NWEB_WORKSPACE_ID": "session-ws",
	}
	env := buildEnv(base, cfg, injected)
	m := envToMap(env)
	assert.Equal(t, "session-jwt", m["NWEB_API_TOKEN"])
	assert.Equal(t, "session-ws", m["NWEB_WORKSPACE_ID"])
}

func TestBuildEnv_ConfigCanSetStrippedAuthVars(t *testing.T) {
	// Stripping is host-only: a deliberate Sandbox Config value still applies.
	base := []string{"NWEB_WORKSPACE_ID=host-ws"}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "NWEB_WORKSPACE_ID", Plain: "config-ws"},
		},
	}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "config-ws", m["NWEB_WORKSPACE_ID"])
}

func TestBuildEnv_SkipsEmptyKey(t *testing.T) {
	base := []string{}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "", Plain: "should-be-skipped"},
		},
	}
	env := buildEnv(base, cfg, nil)
	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "="), "empty key leaked: %q", e)
	}
}

// --- buildSessionEnv ---

// The backend mints a per-session NWEB_API_TOKEN scoped to the session's
// creator (long TTL) and delivers it inside Config.EnvVars. buildSessionEnv
// must let that value flow through untouched — it must NOT override it with
// the connect operator's own JWT, or every user's session on a shared Private
// VM would run under the operator's short-lived identity.
func TestBuildSessionEnv_UsesBackendConfigToken(t *testing.T) {
	b := &SessionBootstrap{
		SessionID:   "sess-1",
		WorkspaceID: "ws-1",
		Endpoint:    "https://api.example",
		Config: &sandboxv1.Sandbox_Config{
			EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
				{Key: "NWEB_API_TOKEN", Plain: "userB-session-token"},
			},
		},
	}
	env, err := b.buildSessionEnv()
	assert.NoError(t, err)
	m := envToMap(env)
	assert.Equal(t, "userB-session-token", m["NWEB_API_TOKEN"],
		"session must use the backend-provided per-session token, not an injected operator token")
}

// If the backend did not provision NWEB_API_TOKEN in the config, the session
// must fail loudly rather than start with no (or the wrong) API identity —
// mirroring the Cloud path, which treats an absent NWEB_API_TOKEN as an error.
func TestBuildSessionEnv_ErrorsWhenConfigMissingToken(t *testing.T) {
	b := &SessionBootstrap{Config: &sandboxv1.Sandbox_Config{}}
	_, err := b.buildSessionEnv()
	assert.Error(t, err, "must fail when the backend did not provision NWEB_API_TOKEN")
}

// --- setupGitRepos ---

// One repo has a bad remote. It must be logged and skipped, not abort setup:
// the other two repos — cloned concurrently, one goroutine per repo — still
// end up on disk. Run with -race to confirm the concurrent clones and their
// concurrent writeTerm calls don't race on shared state.
func TestSetupGitRepos_SkipsFailedRepoAndClonesRestConcurrently(t *testing.T) {
	root := t.TempDir()
	good1 := initGitRemote(t, filepath.Join(root, "remotes", "good1"))
	good2 := initGitRemote(t, filepath.Join(root, "remotes", "good2"))
	badURL := filepath.Join(root, "remotes", "does-not-exist")

	sessionDir := filepath.Join(root, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	conn, cleanup := discardingWSConn(t)
	defer cleanup()

	b := &SessionBootstrap{
		SessionID: "test-session",
		Config: &sandboxv1.Sandbox_Config{
			GitRepos: []*integrationv1.GitRepo{
				{Url: good1, Branch: "main", TargetDir: "good1"},
				{Url: badURL, Branch: "main", TargetDir: "bad"},
				{Url: good2, Branch: "main", TargetDir: "good2"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	failures := b.setupGitRepos(ctx, conn, sessionDir)

	require.Len(t, failures, 1, "exactly the bad repo should fail")
	assert.Contains(t, failures[0], badURL)
	assert.DirExists(t, filepath.Join(sessionDir, "good1", ".git"),
		"repo before the bad one in Config order must still be cloned")
	assert.DirExists(t, filepath.Join(sessionDir, "good2", ".git"),
		"repo after the bad one in Config order must still be cloned — the loop must not abort on the first failure")
	assert.NoDirExists(t, filepath.Join(sessionDir, "bad"))
}

// appendGitRepoWarnings must land on both CLAUDE.md and AGENTS.md — Private
// VM sessions write them as two independent files (no symlink, unlike the
// CLOUD path), so the agent reads whichever one it's configured for.
func TestAppendGitRepoWarnings_AppendsToBothAgentConfigs(t *testing.T) {
	sessionDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "CLAUDE.md"), []byte("# base prompt\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "AGENTS.md"), []byte("# base prompt\n"), 0o644))

	b := &SessionBootstrap{SessionID: "test-session"}
	err := b.appendGitRepoWarnings(sessionDir, []string{"https://example.com/foo.git: clone failed: exit status 128"})
	require.NoError(t, err)

	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		content, readErr := os.ReadFile(filepath.Join(sessionDir, name))
		require.NoError(t, readErr)
		s := string(content)
		assert.True(t, strings.HasPrefix(s, "# base prompt\n"), "%s: original content must be preserved, got %q", name, s)
		assert.Contains(t, s, "## Sandbox setup issues")
		assert.Contains(t, s, "https://example.com/foo.git: clone failed: exit status 128")
	}
}

// helpers

// initGitRemote creates a local git repo with one commit on main, usable as
// a clone source in tests.
func initGitRemote(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// discardingWSConn dials a real websocket against a local httptest server
// that reads and discards every frame, so production code that writes
// progress over the connection (writeTerm) has somewhere real to write —
// including from multiple goroutines at once.
func discardingWSConn(t *testing.T) (conn *websocket.Conn, cleanup func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	require.NoError(t, err)

	return conn, func() {
		conn.CloseNow() //nolint:errcheck
		srv.Close()
	}
}

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

// --- buildEnv: host terminal identity ---

// A Private VM session's terminal is the browser's xterm.js, never the
// terminal the operator ran `retask sandbox connect` from. Inheriting the
// operator's terminal identity makes the agent probe for emulator features
// that are not there; Claude Code's iTerm2/Terminal.app "external clear"
// detector reads a replayed cursor report as a Cmd+K and submits /clear,
// wiping the user's conversation.
func TestBuildEnv_StripsHostTerminalIdentity(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"TERM_PROGRAM=iTerm.app",
		"TERM_PROGRAM_VERSION=3.5.0",
		"__CFBundleIdentifier=com.googlecode.iterm2",
		"ITERM_SESSION_ID=w0t0p0",
		"LC_TERMINAL=iTerm2",
		"TERM_SESSION_ID=abc",
		"TERMINAL_EMULATOR=JetBrains-JediTerm",
		"TMUX=/tmp/tmux-501/default,1,0",
		"COLORFGBG=15;0",
	}
	cfg := &sandboxv1.Sandbox_Config{}
	m := envToMap(buildEnv(base, cfg, nil))

	assert.Equal(t, "/usr/bin", m["PATH"], "unrelated host vars must survive")
	for _, k := range []string{
		"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "__CFBundleIdentifier",
		"ITERM_SESSION_ID", "LC_TERMINAL", "TERM_SESSION_ID",
		"TERMINAL_EMULATOR", "TMUX", "COLORFGBG",
	} {
		_, ok := m[k]
		assert.False(t, ok, "%s describes the operator's terminal and must not reach the session", k)
	}
}

// Stripping the host terminal identity must leave the session with a truthful
// one, not an absent one: without TERM the agent renders in degraded mode, and
// without COLORTERM it drops from truecolor to 256 colors.
func TestBuildEnv_SetsBrowserTerminalDefaults(t *testing.T) {
	base := []string{"TERM=xterm-kitty", "TERM_PROGRAM=Apple_Terminal"}
	m := envToMap(buildEnv(base, &sandboxv1.Sandbox_Config{}, nil))
	assert.Equal(t, "xterm-256color", m["TERM"], "the session's terminal is xterm.js")
	assert.Equal(t, "truecolor", m["COLORTERM"])
}

// The defaults sit in the host layer, so a deliberate config value still wins.
func TestBuildEnv_ConfigOverridesTerminalDefaults(t *testing.T) {
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "TERM", Plain: "screen-256color"},
		},
	}
	m := envToMap(buildEnv([]string{"TERM=xterm-kitty"}, cfg, nil))
	assert.Equal(t, "screen-256color", m["TERM"])
}
