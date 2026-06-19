# Private VM Session Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement full session bootstrapping for Private VM sandboxes — session folder, git repo cloning, system prompt, env vars, and retry/continue/exit menu — mirroring the Cloud sandbox `lib.sh` flow entirely in Go.

**Architecture:** sandbox-proxy builds and forwards the system prompt + seed prompt in the `new_session` WebSocket message. The retask-cli `SessionBootstrap` struct (new file) receives that message, connects the session lane first, streams setup progress to the FE terminal, clones git repos with retry, writes CLAUDE.md/AGENTS.md, builds the env slice (host + config + injected), then starts the PTY.

**Tech Stack:** Go 1.22+, `google.golang.org/protobuf/encoding/protojson`, `github.com/coder/websocket`, `github.com/stretchr/testify`, TypeScript (Cloudflare Workers), `@bufbuild/protobuf`

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `sandbox-proxy/src/index.ts` | Modify | Build + forward `x-system-prompt` / `x-seed-prompt` headers for PRIVATE terminal path |
| `sandbox-proxy/src/privateSandboxRelay.ts` | Modify | Read headers; add `system_prompt` + `seed_prompt` to `new_session` message |
| `internal/cmd/sandbox/datalane.go` | Modify | Update `dataLaneMsgNewSession` struct; update `new_session` dispatch |
| `internal/cmd/sandbox/sessionBootstrap.go` | **Create** | All per-session setup logic |
| `internal/cmd/sandbox/sessionBootstrap_test.go` | **Create** | Unit tests for pure helper functions |
| `internal/cmd/sandbox/sessionlane.go` | Modify | New `Start()` signature; connect lane before bootstrap; call bootstrap; cleanup |
| `internal/cmd/sandbox/connect.go` | Modify | Pass `workspaceID`, `sandboxName`, `baseDir`, `jwt`, `endpoint` to `SessionManager` |

---

## Task 1: sandbox-proxy — forward system prompt and seed prompt

**Files:**
- Modify: `sandbox-proxy/src/index.ts`
- Modify: `sandbox-proxy/src/privateSandboxRelay.ts`

- [ ] **Step 1: Update `src/index.ts` PRIVATE terminal path**

Add import for `buildSystemPrompt` at the top of `src/index.ts` (after the existing `setupSession` import block):

```ts
import { buildSystemPrompt } from './systemPrompt';
```

Find the PRIVATE fork block inside the `/ws/terminals` handler (starts at `if (resp?.sandbox?.type === Sandbox_Type.PRIVATE)`). Add two header lines after `headers.set('x-sandbox-config', configJson)`:

```ts
      // existing
      const headers = new Headers(request.headers);
      headers.set('x-sandbox-config', configJson);
      // add these two lines:
      headers.set('x-system-prompt', buildSystemPrompt(resp!));
      headers.set('x-seed-prompt', resp?.session?.seedPrompt ?? '');
      return stub.fetch(
```

- [ ] **Step 2: Update `src/privateSandboxRelay.ts` `handleTerminal`**

In `handleTerminal`, after the existing lines that read `session_name` and `sandboxConfig`, add:

```ts
    const systemPrompt = request.headers.get('x-system-prompt') ?? '';
    const seedPrompt   = request.headers.get('x-seed-prompt')   ?? '';
```

Update the `this.vmSocket.send(...)` call to include the new fields:

```ts
    this.vmSocket.send(
      JSON.stringify({
        type: 'new_session',
        session_id: sessionId,
        token: sessionToken,
        new_session: {
          name: sessionName,
          config: sandboxConfig,
          system_prompt: systemPrompt,
          seed_prompt:   seedPrompt,
        },
      }),
    );
```

- [ ] **Step 3: Build sandbox-proxy to verify no TypeScript errors**

```bash
cd /Users/tan/nweb/sandbox-proxy && yarn tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/tan/nweb/sandbox-proxy
git add src/index.ts src/privateSandboxRelay.ts
git commit -m "feat: forward system prompt and seed prompt to private VM in new_session"
```

---

## Task 2: Update `datalane.go` message struct and dispatch

**Files:**
- Modify: `internal/cmd/sandbox/datalane.go`

- [ ] **Step 1: Add `encoding/json` import if not present**

Open `internal/cmd/sandbox/datalane.go`. The import block currently has no `encoding/json`. Add it:

```go
import (
    "context"
    "encoding/json"   // add this
    "errors"
    "fmt"
    "log/slog"
    "os"
    "sync/atomic"
    "time"

    "github.com/coder/websocket"
)
```

- [ ] **Step 2: Replace `dataLaneMsgNewSession` struct**

Find and replace the existing struct:

```go
// OLD — remove this entire struct
type dataLaneMsgNewSession struct {
	Name        string            `json:"name,omitempty"`
	InitCommand string            `json:"init_command,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}
```

Replace with:

```go
type dataLaneMsgNewSession struct {
	Name         string          `json:"name,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	SystemPrompt string          `json:"system_prompt,omitempty"`
	SeedPrompt   string          `json:"seed_prompt,omitempty"`
}
```

- [ ] **Step 3: Update `new_session` dispatch in `connectOnce`**

Find the `case "new_session":` block. Change the `go dl.sessions.Start(...)` call:

```go
// OLD
go dl.sessions.Start(ctx, msg.SessionID, msg.Token, msg.NewSession.Name, msg.NewSession.InitCommand, msg.NewSession.Env)

// NEW
go dl.sessions.Start(ctx, msg.SessionID, msg.Token, msg.NewSession.Name, msg.NewSession.Config, msg.NewSession.SystemPrompt, msg.NewSession.SeedPrompt)
```

- [ ] **Step 4: Verify it compiles (sessionlane.go will fail — that's expected)**

```bash
go build ./internal/cmd/sandbox/... 2>&1 | head -20
```

Expected: compile errors only on `sessionlane.go` about the wrong `Start()` signature. No other errors.

---

## Task 3: Write failing tests for pure helper functions

**Files:**
- Create: `internal/cmd/sandbox/sessionBootstrap_test.go`

These functions don't exist yet — tests will fail to compile until Task 4.

- [ ] **Step 1: Create the test file**

```go
package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

// --- injectGithubToken ---

func TestInjectGithubToken(t *testing.T) {
	token := "ghp_TOKEN"
	tests := []struct {
		url  string
		want string
	}{
		{
			"https://github.com/foo/bar.git",
			"https://oauth2:ghp_TOKEN@github.com/foo/bar.git",
		},
		{
			"git@github.com:foo/bar.git",
			"https://oauth2:ghp_TOKEN@github.com/foo/bar.git",
		},
		{
			"https://gitlab.com/foo/bar.git",
			"https://gitlab.com/foo/bar.git", // non-github: unchanged
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, injectGithubToken(tc.url, token), "url=%q", tc.url)
	}
}

func TestInjectGithubToken_EmptyToken(t *testing.T) {
	url := "https://github.com/foo/bar.git"
	assert.Equal(t, url, injectGithubToken(url, ""))
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

// --- parseChoiceFrom ---

func TestParseChoiceFrom(t *testing.T) {
	tests := []struct {
		input []byte
		want  int
	}{
		{[]byte("1"), 1},
		{[]byte("2"), 2},
		{[]byte("3"), 3},
		{[]byte("4"), 0},
		{[]byte{'\r'}, 0},
		{[]byte{'\n'}, 0},
		{[]byte("12"), 1}, // first valid char wins
		{[]byte("abc2"), 2},
		{[]byte{}, 0},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, parseChoiceFrom(tc.input), "input=%q", tc.input)
	}
}

// helpers

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}
```

- [ ] **Step 2: Verify tests fail to compile (functions not yet defined)**

```bash
go test ./internal/cmd/sandbox/... 2>&1 | head -20
```

Expected: compile errors about `deriveTargetDir`, `injectGithubToken`, `buildEnv`, `parseChoiceFrom` undefined.

---

## Task 4: Create `sessionBootstrap.go` — pure helper functions

**Files:**
- Create: `internal/cmd/sandbox/sessionBootstrap.go`

- [ ] **Step 1: Create the file with package declaration, imports, and pure helpers**

```go
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
	"strings"
	"time"

	"github.com/coder/websocket"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// ErrAborted is returned by SessionBootstrap.Run when the user chooses Exit
// from the failure menu. The caller must close the session lane without
// starting a PTY.
var ErrAborted = errors.New("session aborted by user")

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
//
//	"git@github.com:nwebxyz/api.git"  -> "api"
//	"https://github.com/foo/bar.git"  -> "bar"
func deriveTargetDir(url string) string {
	url = strings.TrimRight(url, "/")
	parts := strings.FieldsFunc(url, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) == 0 {
		return "repo"
	}
	last := parts[len(parts)-1]
	return strings.TrimSuffix(last, ".git")
}

// injectGithubToken embeds token into GitHub HTTPS URLs so private repos can
// be cloned without a credential helper. SSH-style URLs are converted to HTTPS
// first. Non-GitHub URLs are returned unchanged. Returns url unchanged if token
// is empty.
func injectGithubToken(rawURL, token string) string {
	if token == "" {
		return rawURL
	}
	// Convert SSH to HTTPS.
	if strings.HasPrefix(rawURL, "git@github.com:") {
		rawURL = "https://github.com/" + strings.TrimPrefix(rawURL, "git@github.com:")
	}
	if strings.HasPrefix(rawURL, "https://github.com/") {
		return "https://oauth2:" + token + "@github.com/" +
			strings.TrimPrefix(rawURL, "https://github.com/")
	}
	return rawURL
}

// buildEnv merges three layers into a process environment slice.
// Later layers override earlier ones:
//  1. baseEnv  — host machine env (os.Environ())
//  2. config   — user-configured env vars from Sandbox_Config
//  3. injected — standard session vars that always override
func buildEnv(baseEnv []string, config *sandboxv1.Sandbox_Config, injected map[string]string) []string {
	env := make(map[string]string, len(baseEnv))
	for _, e := range baseEnv {
		k, v, _ := strings.Cut(e, "=")
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
// session lane WebSocket. Errors are silently dropped — if the lane is closed,
// the bootstrap will fail on the next Read anyway.
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
			continue // skip resize and other frames
		}
		b, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil || len(b) == 0 {
			continue
		}
		writeTerm(ctx, conn, string(b)) // echo
		if choice := parseChoiceFrom(b); choice != 0 {
			writeTerm(ctx, conn, "\r\n")
			return choice
		}
		// Enter with no prior selection defaults to 2 (continue).
		for _, c := range b {
			if c == '\r' || c == '\n' {
				writeTerm(ctx, conn, "\r\n")
				return 2
			}
		}
	}
}
```

- [ ] **Step 2: Run tests — pure helpers should pass**

```bash
go test ./internal/cmd/sandbox/... -run "TestDeriveTargetDir|TestInjectGithubToken|TestBuildEnv|TestParseChoiceFrom" -v
```

Expected: all matching tests pass.

---

## Task 5: Implement `SessionBootstrap.Run` — folder, configs, git repos

**Files:**
- Modify: `internal/cmd/sandbox/sessionBootstrap.go` (append to existing file)

- [ ] **Step 1: Append `setupFolder`, `writeAgentConfigs`, `cloneWithRetry`, `setupGitRepos`, and `Run`**

```go
// Run performs all bootstrap steps before the PTY starts. It streams progress
// to conn as terminal output frames.
//
// Returns (sessionDir, envSlice, nil) on success.
// Returns ("", nil, ErrAborted) if the user chooses Exit from the failure menu.
// Returns ("", nil, err) on non-recoverable error.
func (b *SessionBootstrap) Run(ctx context.Context, conn *websocket.Conn) (string, []string, error) {
	sessionDir := filepath.Join(b.BaseDir, "session-"+b.SessionID)

	writeTerm(ctx, conn, "\r\n[retask] Setting up session...\r\n")

	if err := b.setupFolder(sessionDir); err != nil {
		return "", nil, fmt.Errorf("create session folder: %w", err)
	}
	if err := b.writeAgentConfigs(sessionDir); err != nil {
		return "", nil, fmt.Errorf("write agent configs: %w", err)
	}

	// Git repos: retry loop with interactive menu on failure.
	if len(b.Config.GetGitRepos()) > 0 {
	outer:
		for {
			err := b.setupGitRepos(ctx, conn, sessionDir)
			if err == nil {
				break
			}
			writeTerm(ctx, conn, fmt.Sprintf("\r\n[retask] Git repo setup failed: %v\r\n", err))
			writeTerm(ctx, conn, "\r\n  1) Retry\r\n  2) Continue (skip repos)\r\n  3) Exit\r\n\r\nSelection [2]: ")
			switch readChoice(ctx, conn) {
			case 1:
				continue outer
			case 3:
				return "", nil, ErrAborted
			default: // 2 or read error
				break outer
			}
		}
	}

	env := b.buildSessionEnv()

	writeTerm(ctx, conn, "\r\n[retask] Session ready.\r\n\r\n")
	return sessionDir, env, nil
}

func (b *SessionBootstrap) setupFolder(sessionDir string) error {
	return os.MkdirAll(sessionDir, 0o755)
}

func (b *SessionBootstrap) writeAgentConfigs(sessionDir string) error {
	// CLAUDE.md — system prompt
	if err := os.WriteFile(filepath.Join(sessionDir, "CLAUDE.md"), []byte(b.SystemPrompt), 0o644); err != nil {
		return err
	}
	// AGENTS.md — same content (copy, not symlink, for portability across OSes)
	if err := os.WriteFile(filepath.Join(sessionDir, "AGENTS.md"), []byte(b.SystemPrompt), 0o644); err != nil {
		return err
	}
	// .claude/settings.json — project-scoped: suppresses onboarding prompts
	// without touching the user's global ~/.claude/settings.json.
	claudeDir := filepath.Join(sessionDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}
	settings := `{"skipDangerousModePermissionPrompt":true,"enabledPlugins":{"superpowers@claude-plugins-official":true}}`
	return os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0o644)
}

// setupGitRepos clones every repo in config.GitRepos into sessionDir.
// Each clone is retried up to 3 times with 1 s backoff before returning an error.
func (b *SessionBootstrap) setupGitRepos(ctx context.Context, conn *websocket.Conn, sessionDir string) error {
	// Find GitHub token from config env vars for private repo access.
	githubToken := ""
	for _, ev := range b.Config.GetEnvVars() {
		if ev.GetKey() == "GITHUB_TOKEN" || ev.GetKey() == "GH_TOKEN" {
			if s := ev.GetSecret(); s != nil && s.GetValue() != "" {
				githubToken = s.GetValue()
			} else {
				githubToken = ev.GetPlain()
			}
			if githubToken != "" {
				break
			}
		}
	}

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
		cloneURL := injectGithubToken(repo.GetUrl(), githubToken)

		if err := b.cloneWithRetry(ctx, conn, cloneURL, branch, dest); err != nil {
			return fmt.Errorf("clone %s: %w", repo.GetUrl(), err)
		}
	}
	return nil
}

// cloneWithRetry runs git clone --depth=1 up to 3 times with 1 s backoff.
func (b *SessionBootstrap) cloneWithRetry(ctx context.Context, conn *websocket.Conn, url, branch, dest string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		writeTerm(ctx, conn, fmt.Sprintf("\r\n[repos] cloning %s @ %s (attempt %d/3)...\r\n", url, branch, attempt))
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", "-b", branch, url, dest)
		out, err := cmd.CombinedOutput()
		if err == nil {
			writeTerm(ctx, conn, fmt.Sprintf("[repos] cloned %s\r\n", dest))
			return nil
		}
		lastErr = fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
		writeTerm(ctx, conn, fmt.Sprintf("[repos] attempt %d failed: %v\r\n", attempt, err))
		if attempt < 3 {
			time.Sleep(time.Second)
		}
	}
	return lastErr
}

// buildSessionEnv constructs the process environment for the PTY:
// host env → config env vars → injected session vars.
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
```

- [ ] **Step 2: Build to verify no compile errors**

```bash
go build ./internal/cmd/sandbox/...
```

Expected: compile errors only on `sessionlane.go` (Start signature mismatch — fixed in Task 6). No errors in `sessionBootstrap.go`.

- [ ] **Step 3: Run all bootstrap tests**

```bash
go test ./internal/cmd/sandbox/... -run "TestDeriveTargetDir|TestInjectGithubToken|TestBuildEnv|TestParseChoiceFrom" -v
```

Expected: all pass.

---

## Task 6: Update `sessionlane.go` — new `Start()` and cleanup

**Files:**
- Modify: `internal/cmd/sandbox/sessionlane.go`

- [ ] **Step 1: Add imports for `encoding/json`, `fmt`, `path/filepath`, `os`, and `protojson`**

Find the import block in `sessionlane.go`. Add the missing entries:

```go
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	agentfleet "github.com/hoaitan/agentfleet"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)
```

- [ ] **Step 2: Add new fields to `SessionManager` struct**

```go
type SessionManager struct {
	sandboxID   string
	wsBase      string
	fleet       *agentfleet.Fleet
	fleetCfg    agentfleet.FleetConfig
	agentCfg    agentfleet.AgentConfig
	log         *slog.Logger
	// new fields:
	workspaceID string
	sandboxName string
	baseDir     string
	jwt         string
	endpoint    string

	mu       sync.Mutex
	sessions map[string]*agentfleet.Runner
}
```

- [ ] **Step 3: Update `newSessionManager` constructor signature**

```go
func newSessionManager(
	sandboxID, wsBase string,
	fleet *agentfleet.Fleet,
	fleetCfg agentfleet.FleetConfig,
	agentCfg agentfleet.AgentConfig,
	log *slog.Logger,
	workspaceID, sandboxName, baseDir, jwt, endpoint string,
) *SessionManager {
	return &SessionManager{
		sandboxID:   sandboxID,
		wsBase:      wsBase,
		fleet:       fleet,
		fleetCfg:    fleetCfg,
		agentCfg:    agentCfg,
		log:         log,
		workspaceID: workspaceID,
		sandboxName: sandboxName,
		baseDir:     baseDir,
		jwt:         jwt,
		endpoint:    endpoint,
		sessions:    make(map[string]*agentfleet.Runner),
	}
}
```

- [ ] **Step 4: Replace `Start()` method entirely**

Remove the existing `Start()` method and replace with:

```go
// Start handles a new_session event: connects the session lane, runs bootstrap,
// then launches the PTY and bridges it to the session lane.
func (sm *SessionManager) Start(ctx context.Context, sessionID, token, name string, configJSON json.RawMessage, systemPrompt, seedPrompt string) {
	if name == "" {
		name = sessionID
	}

	// Parse Sandbox_Config from proto JSON (camelCase field names).
	var cfg sandboxv1.Sandbox_Config
	if len(configJSON) > 0 {
		if err := protojson.Unmarshal(configJSON, &cfg); err != nil {
			sm.logError("session_config_parse_error", "session_id", sessionID, "error", err)
			return
		}
	}

	// Connect session lane first so bootstrap can stream logs to the FE.
	wsURL := fmt.Sprintf("%s/ws/session-lane?sandbox_id=%s&session_id=%s&token=%s",
		sm.wsBase, sm.sandboxID, sessionID, token)
	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		sm.logError("session_lane_error", "session_id", sessionID, "error", err)
		return
	}
	fmt.Fprintf(os.Stderr, "session lane: %s/ws/session-lane?sandbox_id=%s&session_id=%s\n",
		sm.wsBase, sm.sandboxID, sessionID)

	// Run bootstrap — writes files, clones repos, builds env.
	bs := &SessionBootstrap{
		SessionID:    sessionID,
		SessionName:  name,
		SandboxID:    sm.sandboxID,
		SandboxName:  sm.sandboxName,
		WorkspaceID:  sm.workspaceID,
		Config:       &cfg,
		SystemPrompt: systemPrompt,
		SeedPrompt:   seedPrompt,
		JWT:          sm.jwt,
		Endpoint:     sm.endpoint,
		BaseDir:      sm.baseDir,
		Log:          sm.log,
	}
	sessionDir, env, err := bs.Run(ctx, wsConn)
	if err != nil {
		sm.logError("session_bootstrap_failed", "session_id", sessionID, "error", err)
		wsConn.CloseNow() //nolint:errcheck
		return
	}

	initCommand := cfg.GetSessionInitCommand()
	if initCommand == "" {
		initCommand = "bash"
	}
	// cd into session folder before running the init command.
	shellCmd := fmt.Sprintf("cd '%s' && exec %s", sessionDir, initCommand)

	agCfg := sm.agentCfg
	agCfg.Env = env

	sm.logInfo("session_starting", "session_id", sessionID, "name", name, "init_command", initCommand)
	ag := agentfleet.NewPtyAgent([]string{"sh", "-c", shellCmd}, agCfg)
	task := &agentfleet.BasicTask{TaskID: sessionID, TaskName: name, Cmd: initCommand}
	r := agentfleet.NewRunner(task, ag, sm.fleetCfg, agCfg)
	r.Start()

	if err := sm.fleet.Add(ctx, r); err != nil {
		r.Stop() //nolint:errcheck
		wsConn.CloseNow() //nolint:errcheck
		return
	}

	sm.mu.Lock()
	sm.sessions[sessionID] = r
	sm.mu.Unlock()

	r.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})
	go func() {
		err := sm.readLoop(ctx, wsConn, r)
		sm.logInfo("session_lane_closed", "session_id", sessionID, "error", err)
		r.Stop() //nolint:errcheck
	}()

	go func() {
		<-r.Done()
		wsConn.Close(websocket.StatusNormalClosure, "session ended") //nolint:errcheck
		sm.mu.Lock()
		delete(sm.sessions, sessionID)
		sm.mu.Unlock()
		sm.logInfo("session_stopped", "session_id", sessionID)
	}()
}
```

- [ ] **Step 5: Add folder cleanup to `Remove()`**

Find the `Remove()` method and add `os.RemoveAll` after stopping the runner:

```go
func (sm *SessionManager) Remove(sessionID string) {
	sm.mu.Lock()
	r := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()
	if r != nil {
		r.Stop() //nolint:errcheck
		sm.fleet.Remove(sessionID)
	}
	// Clean up the session working folder.
	os.RemoveAll(filepath.Join(sm.baseDir, "session-"+sessionID)) //nolint:errcheck
}
```

- [ ] **Step 6: Build — only `connect.go` should fail now**

```bash
go build ./internal/cmd/sandbox/... 2>&1 | head -20
```

Expected: only errors from `connect.go` about wrong `newSessionManager` argument count.

---

## Task 7: Update `connect.go` — pass new fields to `SessionManager`

**Files:**
- Modify: `internal/cmd/sandbox/connect.go`

- [ ] **Step 1: Capture `baseDir` and update `newSessionManager` call**

In `connect.go`, find the line:
```go
sm := newSessionManager(sandboxID, wsBase, fleet, fleetCfg.Fleet, fleetCfg.Agent, logger)
```

Replace with:

```go
baseDir, err := os.Getwd()
if err != nil {
    return err
}
sm := newSessionManager(
    sandboxID, wsBase,
    fleet, fleetCfg.Fleet, fleetCfg.Agent,
    logger,
    sbResp.Msg.WorkspaceId,
    sbResp.Msg.Name,
    baseDir,
    jwt,
    profile.Endpoint,
)
```

- [ ] **Step 2: Ensure `"os"` is in the import block of `connect.go`**

`connect.go` already imports `"os"` (used for `os.Stderr`, `os.Getenv`). Verify it's present — no change needed if it is.

- [ ] **Step 3: Full build — should compile cleanly**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add \
  internal/cmd/sandbox/datalane.go \
  internal/cmd/sandbox/sessionBootstrap.go \
  internal/cmd/sandbox/sessionBootstrap_test.go \
  internal/cmd/sandbox/sessionlane.go \
  internal/cmd/sandbox/connect.go
git commit -m "feat: implement Private VM session bootstrap (folder, git repos, system prompt, env)"
```

---

## Task 8: Create PR

- [ ] **Step 1: Push branch and open PR**

```bash
git push -u origin main
gh pr create \
  --title "feat: Private VM session bootstrap — git repos, system prompt, env vars" \
  --body "$(cat <<'EOF'
## Summary

- **sandbox-proxy**: forwards system prompt (built by `buildSystemPrompt`) and seed prompt as new fields in the `new_session` WebSocket message for Private VM sandboxes
- **retask-cli**: new `SessionBootstrap` performs full per-session setup before PTY starts: session folder, CLAUDE.md/AGENTS.md, project-scoped `.claude/settings.json`, shallow git clone with 3-attempt retry + Retry/Continue/Exit menu, env var merging (host → config → injected session vars)
- Session folder `session-<id>/` is created relative to where `retask sandbox connect` was invoked and removed on `delete_session`

## Test plan

- [ ] `go test ./internal/cmd/sandbox/...` — all unit tests pass
- [ ] Run `retask sandbox connect <private-sandbox-id>` against a sandbox with git repos configured; verify repos clone and CLAUDE.md appears in `session-<id>/`
- [ ] Simulate a bad GitHub token; verify the Retry/Continue/Exit menu appears in the FE terminal
- [ ] Verify `delete_session` removes the session folder from disk
- [ ] Verify Cloud sandbox terminal sessions are unaffected (no regression in `setupSession` / `lib.sh` path)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 2: Note PR URL**

The `gh pr create` command will print the PR URL. Copy it for review.
