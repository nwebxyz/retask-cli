# Private VM Sandbox Connect — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `retask sandbox connect <sandbox-id>` — a long-running command that connects this machine as the PTY execution backend for a Private VM sandbox, relaying sessions over WebSocket via sandbox-proxy.

**Architecture:** Three small additive changes to `agentfleet` (Runner.Resize, AgentConfig.Env, TUIConfig.Title func) enable isolated per-session env vars, PTY resize, and a live-updating TUI header. retask-cli adds three new files in `internal/cmd/sandbox/` (connect.go, datalane.go, sessionlane.go) plus a WebSocket and agentfleet dependency. The DataLane goroutine handles the persistent reverse-WS control channel; SessionManager creates one agentfleet Runner per incoming session and bridges its PTY I/O to a per-session WebSocket.

**Tech Stack:** Go, `github.com/hoaitan/agentfleet`, `github.com/coder/websocket`, `github.com/charmbracelet/lipgloss`, `connectrpc.com/connect`, `log/slog`

---

## File Map

**agentfleet** (`/Users/tan/nweb/opensource/agentfleet/`)

| Action | File | Change |
|--------|------|--------|
| Modify | `runner.go` | Add `Runner.Resize(rows, cols int) error` |
| Modify | `runner_test.go` | Add `TestRunnerResize` |
| Modify | `config.go` | Add `AgentConfig.Env []string`; change `TUIConfig.Title` to `func() string` |
| Modify | `pty_agent.go` | Apply `cfg.Env` to `cmd.Env` in `Start()` |
| Create | `pty_agent_test.go` | `TestPtyAgentEnv` |
| Modify | `tui/tui.go` | Use `m.cfg.Title()` in `renderHeader` when non-nil |

**retask-cli** (`/Users/tan/nweb/retask-cli/`)

| Action | File | Change |
|--------|------|--------|
| Modify | `go.mod` | Add `github.com/hoaitan/agentfleet` (replace→local) + `github.com/coder/websocket` |
| Create | `internal/cmd/sandbox/connect.go` | `newConnectCommand`, `proxyWSBase`, `makeTitleFunc` |
| Create | `internal/cmd/sandbox/connect_test.go` | `TestProxyWSBase`, `TestWsWriterWrite` |
| Create | `internal/cmd/sandbox/datalane.go` | `DataLane` struct + `Run` loop |
| Create | `internal/cmd/sandbox/sessionlane.go` | `SessionManager`, `wsWriter`, `readLoop` |
| Modify | `internal/cmd/sandbox/command.go` | Register `newConnectCommand` |
| Modify | `internal/cmd/helpcmd/command.go` | Add manifest entry |

---

## Task 1: agentfleet — Add `Runner.Resize()`

**Files:**
- Modify: `/Users/tan/nweb/opensource/agentfleet/runner_test.go`
- Modify: `/Users/tan/nweb/opensource/agentfleet/runner.go`

- [ ] **Step 1: Add the failing test to `runner_test.go`**

Append to `/Users/tan/nweb/opensource/agentfleet/runner_test.go`:

```go
func TestRunnerResize(t *testing.T) {
	ag := agentfleet.NewMockAgent()
	task := &agentfleet.BasicTask{TaskID: "resize-t", TaskName: "resize", Cmd: "echo"}
	r := agentfleet.NewRunner(task, ag, testCfg(), agentfleet.AgentConfig{})
	r.Start()

	err := r.Resize(40, 100)
	assert.NoError(t, err)

	ag.Stop()
	<-r.Done()
}
```

- [ ] **Step 2: Run the test — expect FAIL**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test -run TestRunnerResize ./...
```

Expected: `r.Resize undefined`

- [ ] **Step 3: Add `Resize` to `runner.go`**

Append to `/Users/tan/nweb/opensource/agentfleet/runner.go` after `Stop()`:

```go
// Resize resizes the underlying PTY agent.
func (r *Runner) Resize(rows, cols int) error { return r.ag.Resize(rows, cols) }
```

- [ ] **Step 4: Run the test — expect PASS**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test -run TestRunnerResize ./...
```

Expected: `ok  github.com/hoaitan/agentfleet`

- [ ] **Step 5: Run full suite to confirm no regressions**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git add runner.go runner_test.go
git commit -m "feat: add Runner.Resize delegating to Agent.Resize"
```

---

## Task 2: agentfleet — Add `AgentConfig.Env`

**Files:**
- Modify: `/Users/tan/nweb/opensource/agentfleet/config.go`
- Modify: `/Users/tan/nweb/opensource/agentfleet/pty_agent.go`
- Create: `/Users/tan/nweb/opensource/agentfleet/pty_agent_test.go`

- [ ] **Step 1: Write the failing test — create `pty_agent_test.go`**

```go
package agentfleet_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentfleet "github.com/hoaitan/agentfleet"
)

func TestPtyAgentEnv(t *testing.T) {
	ag := agentfleet.NewPtyAgent(
		[]string{"sh", "-c", "printenv RETASK_FLEET_TEST_ENV"},
		agentfleet.AgentConfig{
			PTYRows: 24,
			PTYCols: 80,
			Env:     []string{"RETASK_FLEET_TEST_ENV=sentinel_xyz"},
		},
	)
	task := &agentfleet.BasicTask{TaskID: "env-t", TaskName: "env test", Cmd: "sh"}
	cfg := agentfleet.FleetConfig{RingBufferSize: 200}
	r := agentfleet.NewRunner(task, ag, cfg, agentfleet.AgentConfig{
		PTYRows: 24, PTYCols: 80,
		Env: []string{"RETASK_FLEET_TEST_ENV=sentinel_xyz"},
	})
	r.Start()

	select {
	case <-r.Done():
	case <-time.After(5 * time.Second):
		require.Fail(t, "timeout: sh process did not exit")
	}

	output := strings.Join(r.Lines(), "\n")
	assert.Contains(t, output, "sentinel_xyz")
}
```

- [ ] **Step 2: Run the test — expect FAIL (Env field missing)**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test -run TestPtyAgentEnv ./...
```

Expected: compile error `unknown field Env in struct literal`

- [ ] **Step 3: Add `Env []string` to `AgentConfig` in `config.go`**

In `/Users/tan/nweb/opensource/agentfleet/config.go`, change `AgentConfig` to:

```go
// AgentConfig controls PTY dimensions and environment.
type AgentConfig struct {
	PTYRows int
	PTYCols int
	Env     []string // extra env vars (KEY=VALUE); appended to os.Environ() for child process only
}
```

- [ ] **Step 4: Apply `cfg.Env` in `pty_agent.go`**

In `/Users/tan/nweb/opensource/agentfleet/pty_agent.go`, inside `Start()` add `a.cmd.Env = ...` **before** `a.cmd.Start()`. The surrounding context for placement:

```go
	a.cmd.Stdin = pts
	a.cmd.Stdout = pts
	a.cmd.Stderr = pts

	if len(a.cfg.Env) > 0 {
		a.cmd.Env = append(os.Environ(), a.cfg.Env...)
	}

	if a.cmd.SysProcAttr == nil {
```

Add `"os"` to the import block in `pty_agent.go` if not already present.

- [ ] **Step 5: Run the test — expect PASS**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test -run TestPtyAgentEnv ./...
```

Expected: `ok  github.com/hoaitan/agentfleet`

- [ ] **Step 6: Run full suite**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./...
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git add config.go pty_agent.go pty_agent_test.go
git commit -m "feat: add AgentConfig.Env for per-session isolated environment"
```

---

## Task 3: agentfleet — `TUIConfig.Title func() string`

**Files:**
- Modify: `/Users/tan/nweb/opensource/agentfleet/config.go`
- Modify: `/Users/tan/nweb/opensource/agentfleet/tui/tui.go`

No unit test: Bubbletea view tests require a full terminal emulator. Verified by build + visual inspection at the end.

- [ ] **Step 1: Change `TUIConfig.Title` from `string` to `func() string` in `config.go`**

In `/Users/tan/nweb/opensource/agentfleet/config.go`, change `TUIConfig` to:

```go
// TUIConfig controls the Bubbletea dashboard appearance.
type TUIConfig struct {
	Title        func() string // header title func; nil = "◈ agentfleet"
	Columns      int           // grid columns               — default: 3
	PreviewLines int           // output lines shown in card — default: 3
	CardWidth    int           // card width in chars        — default: 64
	RefreshRate  time.Duration // TUI tick interval          — default: 500ms
	AutoOpen     bool          // auto-open a tab for each task when it starts — default: true
}
```

- [ ] **Step 2: Update `renderHeader` in `tui/tui.go`**

Replace the existing `renderHeader` function body:

```go
func renderHeader(m model) string {
	runners := m.fleet.Runners()
	var running, done, failed int
	for _, r := range runners {
		switch r.Status() {
		case agentfleet.StatusRunning:
			running++
		case agentfleet.StatusDone:
			done++
		case agentfleet.StatusFailed:
			failed++
		}
	}
	summary := fmt.Sprintf("%d tasks · %d running · %d done", len(runners), running, done)
	if failed > 0 {
		summary += fmt.Sprintf(" · %d failed", failed)
	}
	title := styleTitle.Render("◈ agentfleet")
	if m.cfg.Title != nil {
		title = m.cfg.Title()
	}
	return title + "  " + styleSummary.Render(summary)
}
```

- [ ] **Step 3: Build to verify no regressions**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go build ./...
```

Expected: no errors.

- [ ] **Step 4: Run full suite**

```bash
cd /Users/tan/nweb/opensource/agentfleet && go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/opensource/agentfleet
git add config.go tui/tui.go
git commit -m "feat: add TUIConfig.Title func() string for dynamic header"
```

---

## Task 4: retask-cli — Add dependencies

**Files:**
- Modify: `/Users/tan/nweb/retask-cli/go.mod`
- Update: `/Users/tan/nweb/retask-cli/go.sum` (via go mod tidy)

- [ ] **Step 1: Add agentfleet replace directive to `go.mod`**

Run from `/Users/tan/nweb/retask-cli/`:

```bash
go mod edit -require=github.com/hoaitan/agentfleet@v0.0.0-00010101000000-000000000000
go mod edit -replace=github.com/hoaitan/agentfleet=../opensource/agentfleet
```

- [ ] **Step 2: Add `github.com/coder/websocket`**

```bash
cd /Users/tan/nweb/retask-cli && go get github.com/coder/websocket@latest
```

- [ ] **Step 3: Tidy**

```bash
cd /Users/tan/nweb/retask-cli && go mod tidy
```

This will also pull in transitive deps from agentfleet (`charmbracelet/lipgloss`, `golang.org/x/term`, etc.) and may update the `go` directive to match agentfleet's minimum (`go 1.26.3`). That is expected.

- [ ] **Step 4: Verify build still works**

```bash
cd /Users/tan/nweb/retask-cli && go build ./...
```

Expected: no errors (no new source files yet, just deps).

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add go.mod go.sum
git commit -m "chore: add agentfleet (local replace) and coder/websocket dependencies"
```

---

## Task 5: retask-cli — `proxyWSBase()` + `connect.go` skeleton

**Files:**
- Create: `internal/cmd/sandbox/connect.go`
- Create: `internal/cmd/sandbox/connect_test.go`

- [ ] **Step 1: Write the failing test — create `connect_test.go`**

```go
package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProxyWSBase(t *testing.T) {
	tests := []struct {
		env  string
		want string
	}{
		{"", "wss://sandbox-proxy.prd.nweb.app"},
		{"https://sandbox-proxy.prd.nweb.app/", "wss://sandbox-proxy.prd.nweb.app"},
		{"http://localhost:8080", "ws://localhost:8080"},
		{"http://localhost:8080/", "ws://localhost:8080"},
		{"https://custom.proxy.example.com/", "wss://custom.proxy.example.com"},
	}
	for _, tc := range tests {
		t.Setenv("SANDBOX_PROXY_ENDPOINT", tc.env)
		assert.Equal(t, tc.want, proxyWSBase(), "env=%q", tc.env)
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/tan/nweb/retask-cli && go test -run TestProxyWSBase ./internal/cmd/sandbox/
```

Expected: `undefined: proxyWSBase`

- [ ] **Step 3: Create `connect.go` with the skeleton and `proxyWSBase`**

Create `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/connect.go`:

```go
package sandbox

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/nwebxyz/retask-cli/internal/flags"
)

func newConnectCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "connect <id>",
		Short: "Connect this machine as a Private VM sandbox",
		Long: `Connect this machine as the execution backend for a Private VM sandbox.

This is a long-running command that maintains a persistent WebSocket connection
to sandbox-proxy and manages sessions as local PTY processes.

Usage example:
  retask sandbox connect sandbox_abc123

Environment:
  SANDBOX_PROXY_ENDPOINT  Proxy base URL (default: https://sandbox-proxy.prd.nweb.app/)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Full implementation added in Task 8.
			return nil
		},
	}
}

// proxyWSBase returns the WebSocket base URL for sandbox-proxy,
// converting https→wss and http→ws.
func proxyWSBase() string {
	ep := os.Getenv("SANDBOX_PROXY_ENDPOINT")
	if ep == "" {
		ep = "https://sandbox-proxy.prd.nweb.app/"
	}
	ep = strings.TrimRight(ep, "/")
	ep = strings.Replace(ep, "https://", "wss://", 1)
	ep = strings.Replace(ep, "http://", "ws://", 1)
	return ep
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/tan/nweb/retask-cli && go test -run TestProxyWSBase ./internal/cmd/sandbox/
```

Expected: `ok  github.com/nwebxyz/retask-cli/internal/cmd/sandbox`

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add internal/cmd/sandbox/connect.go internal/cmd/sandbox/connect_test.go
git commit -m "feat: add proxyWSBase and connect command skeleton"
```

---

## Task 6: retask-cli — `DataLane` (`datalane.go`)

**Files:**
- Create: `internal/cmd/sandbox/datalane.go`

- [ ] **Step 1: Create `datalane.go`**

```go
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

const (
	connStateConnecting int32 = 0
	connStateConnected  int32 = 1
	connStateError      int32 = 2
)

var errSandboxDeleted = errors.New("sandbox deleted")

type dataLaneMsg struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Token     string `json:"token,omitempty"`
}

// DataLane manages the persistent reverse WebSocket to sandbox-proxy.
// It dispatches control messages to a SessionManager.
type DataLane struct {
	sandboxID string
	wsBase    string
	jwt       string
	sessions  *SessionManager
	connState *int32 // atomic: 0=connecting, 1=connected, 2=error
	log       *slog.Logger // nil in TUI mode
}

func newDataLane(sandboxID, wsBase, jwt string, sessions *SessionManager, connState *int32, log *slog.Logger) *DataLane {
	return &DataLane{
		sandboxID: sandboxID,
		wsBase:    wsBase,
		jwt:       jwt,
		sessions:  sessions,
		connState: connState,
		log:       log,
	}
}

// Run connects to the data lane and dispatches messages until ctx is cancelled
// or a delete_sandbox message is received. Reconnects with exponential backoff.
func (dl *DataLane) Run(ctx context.Context) {
	backoff := 2 * time.Second
	for {
		err := dl.connectOnce(ctx)
		if err == nil || errors.Is(err, errSandboxDeleted) || ctx.Err() != nil {
			return
		}
		atomicStore(dl.connState, connStateError)
		dl.logWarn("disconnected", "retrying_in", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// connectOnce dials the data lane and reads messages until an error or delete_sandbox.
// Returns errSandboxDeleted on clean delete, ctx.Err() on cancellation, or a network error.
func (dl *DataLane) connectOnce(ctx context.Context) error {
	url := fmt.Sprintf("%s/ws/data-lane?sandbox_id=%s&token=%s",
		dl.wsBase, dl.sandboxID, dl.jwt)

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow() //nolint:errcheck

	atomicStore(dl.connState, connStateConnected)
	dl.logInfo("connected", "sandbox_id", dl.sandboxID)

	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		var msg dataLaneMsg
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}

		switch msg.Type {
		case "ping":
			pong, _ := json.Marshal(dataLaneMsg{Type: "pong"})
			conn.Write(ctx, websocket.MessageText, pong) //nolint:errcheck

		case "new_session":
			dl.logInfo("new_session", "session_id", msg.SessionID)
			go dl.sessions.Start(ctx, msg.SessionID, msg.Token)

		case "stop_session":
			dl.logInfo("stop_session", "session_id", msg.SessionID)
			dl.sessions.Stop(msg.SessionID)

		case "stop_sandbox":
			dl.logInfo("stop_sandbox")
			dl.sessions.StopAll()

		case "delete_sandbox":
			dl.logInfo("delete_sandbox")
			dl.sessions.StopAll()
			conn.Close(websocket.StatusNormalClosure, "deleted") //nolint:errcheck
			return errSandboxDeleted
		}
	}
}

func (dl *DataLane) logInfo(msg string, args ...any) {
	if dl.log != nil {
		dl.log.Info(msg, args...)
	}
}

func (dl *DataLane) logWarn(msg string, args ...any) {
	if dl.log != nil {
		dl.log.Warn(msg, args...)
	}
}

// atomicStore is a thin wrapper so we can use *int32 without importing sync/atomic
// in every file that holds connState.
func atomicStore(p *int32, v int32) {
	*p = v // NOTE: replaced with sync/atomic call in connect.go; this placeholder is overridden
}
```

> **Note:** `atomicStore` above is a placeholder — in Task 8 we use `sync/atomic.Int32` directly and remove this helper. For now it lets the package compile.

- [ ] **Step 2: Build to verify compilation**

```bash
cd /Users/tan/nweb/retask-cli && go build ./internal/cmd/sandbox/
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add internal/cmd/sandbox/datalane.go
git commit -m "feat: add DataLane WebSocket control channel"
```

---

## Task 7: retask-cli — `SessionManager` + `wsWriter` + test

**Files:**
- Create: `internal/cmd/sandbox/sessionlane.go`
- Modify: `internal/cmd/sandbox/connect_test.go` (add wsWriter test)

- [ ] **Step 1: Add `TestWsWriterWrite` to `connect_test.go`**

Append to `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/connect_test.go`:

```go
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWsWriterWrite(t *testing.T) {
	received := make(chan []byte, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		defer conn.Close(websocket.StatusNormalClosure, "")
		_, msg, err := conn.Read(r.Context())
		if err == nil {
			received <- msg
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	ww := &wsWriter{ctx: ctx, conn: conn}
	input := []byte("hello sandbox")
	n, err := ww.Write(input)
	assert.NoError(t, err)
	assert.Equal(t, len(input), n)

	select {
	case raw := <-received:
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &msg))
		assert.Equal(t, "data", msg.Type)
		decoded, err := base64.StdEncoding.DecodeString(msg.Data)
		require.NoError(t, err)
		assert.Equal(t, input, decoded)
	case <-ctx.Done():
		t.Fatal("timeout: server did not receive message")
	}
}
```

> **Note:** The import block for `connect_test.go` now has two separate blocks (one for `TestProxyWSBase`, one for `TestWsWriterWrite`). Merge them into one import block at the top of the file.

The final import block for `connect_test.go`:

```go
package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 2: Run the test — expect FAIL (`wsWriter` undefined)**

```bash
cd /Users/tan/nweb/retask-cli && go test -run TestWsWriterWrite ./internal/cmd/sandbox/
```

Expected: compile error `undefined: wsWriter`

- [ ] **Step 3: Create `sessionlane.go`**

```go
package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	connectrpc "connectrpc.com/connect"
	agentfleet "github.com/hoaitan/agentfleet"
	"github.com/coder/websocket"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	sandboxv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1/sandboxv1connect"
)

// SessionManager creates and tracks one agentfleet Runner per active sandbox session.
type SessionManager struct {
	sandboxID string
	wsBase    string
	svc       sandboxv1connect.SandboxServiceClient
	fleet     *agentfleet.Fleet
	fleetCfg  agentfleet.FleetConfig
	agentCfg  agentfleet.AgentConfig
	log       *slog.Logger

	mu       sync.Mutex
	sessions map[string]*agentfleet.Runner // keyed by session_id
}

func newSessionManager(
	sandboxID, wsBase string,
	svc sandboxv1connect.SandboxServiceClient,
	fleet *agentfleet.Fleet,
	fleetCfg agentfleet.FleetConfig,
	agentCfg agentfleet.AgentConfig,
	log *slog.Logger,
) *SessionManager {
	return &SessionManager{
		sandboxID: sandboxID,
		wsBase:    wsBase,
		svc:       svc,
		fleet:     fleet,
		fleetCfg:  fleetCfg,
		agentCfg:  agentCfg,
		log:       log,
		sessions:  make(map[string]*agentfleet.Runner),
	}
}

// Start handles a new_session event: fetches runtime, launches PTY, connects session lane.
func (sm *SessionManager) Start(ctx context.Context, sessionID, token string) {
	resp, err := sm.svc.GetSessionRuntime(ctx, connectrpc.NewRequest(&commonv1.Id{Id: sessionID}))
	if err != nil {
		sm.logError("session_runtime_error", "session_id", sessionID, "error", err)
		return
	}
	rt := resp.Msg

	initCommand := rt.Sandbox.Config.SessionInitCommand
	if initCommand == "" {
		sm.logError("session_no_init_command", "session_id", sessionID)
		return
	}

	// Resolve env vars (plaintext secrets already populated by GetSessionRuntime).
	var envSlice []string
	for _, ev := range rt.Sandbox.Config.EnvVars {
		if ev.Secret != nil && ev.Secret.Value != "" {
			envSlice = append(envSlice, ev.Key+"="+ev.Secret.Value)
		} else if ev.Plain != "" {
			envSlice = append(envSlice, ev.Key+"="+ev.Plain)
		}
	}

	name := rt.Session.Name
	if name == "" {
		name = sessionID
	}

	agCfg := sm.agentCfg
	agCfg.Env = envSlice

	ag := agentfleet.NewPtyAgent(strings.Fields(initCommand), agCfg)
	task := &agentfleet.BasicTask{TaskID: sessionID, TaskName: name, Cmd: initCommand}
	r := agentfleet.NewRunner(task, ag, sm.fleetCfg, agCfg)
	r.Start()

	if err := sm.fleet.Add(ctx, r); err != nil {
		r.Stop() //nolint:errcheck
		return
	}

	sm.mu.Lock()
	sm.sessions[sessionID] = r
	sm.mu.Unlock()

	// Connect session lane WebSocket.
	wsURL := fmt.Sprintf("%s/ws/session-lane?sandbox_id=%s&session_id=%s&token=%s",
		sm.wsBase, sm.sandboxID, sessionID, token)
	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		sm.logError("session_lane_error", "session_id", sessionID, "error", err)
		r.Stop() //nolint:errcheck
		return
	}

	// Agent stdout → session lane (encoded as base64 JSON).
	r.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})

	// Session lane → agent stdin + PTY resize.
	go sm.readLoop(ctx, wsConn, r)

	// Cleanup when PTY process exits.
	go func() {
		<-r.Done()
		wsConn.Close(websocket.StatusNormalClosure, "session ended") //nolint:errcheck
		sm.mu.Lock()
		delete(sm.sessions, sessionID)
		sm.mu.Unlock()
		sm.logInfo("session_stopped", "session_id", sessionID)
	}()
}

func (sm *SessionManager) readLoop(ctx context.Context, conn *websocket.Conn, r *agentfleet.Runner) {
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Rows int    `json:"rows"`
			Cols int    `json:"cols"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "data":
			b, err := base64.StdEncoding.DecodeString(msg.Data)
			if err != nil {
				continue
			}
			r.StdinWriter().Write(b) //nolint:errcheck
		case "resize":
			r.Resize(msg.Rows, msg.Cols) //nolint:errcheck
		}
	}
}

// Stop sends SIGTERM to the session's PTY process. The cleanup goroutine handles
// the rest when the process exits.
func (sm *SessionManager) Stop(sessionID string) {
	sm.mu.Lock()
	r := sm.sessions[sessionID]
	sm.mu.Unlock()
	if r != nil {
		r.Stop() //nolint:errcheck
	}
}

// StopAll stops every active session. Used on stop_sandbox and delete_sandbox.
func (sm *SessionManager) StopAll() {
	sm.mu.Lock()
	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	sm.mu.Unlock()
	for _, id := range ids {
		sm.Stop(id)
	}
}

func (sm *SessionManager) logInfo(msg string, args ...any) {
	if sm.log != nil {
		sm.log.Info(msg, args...)
	}
}

func (sm *SessionManager) logError(msg string, args ...any) {
	if sm.log != nil {
		sm.log.Error(msg, args...)
	}
}

// wsWriter implements io.Writer by encoding bytes as base64 JSON and writing
// them to a session-lane WebSocket connection.
type wsWriter struct {
	ctx  context.Context
	conn *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
	msg, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{"data", base64.StdEncoding.EncodeToString(p)})
	if err := w.conn.Write(w.ctx, websocket.MessageText, msg); err != nil {
		return 0, err
	}
	return len(p), nil
}
```

- [ ] **Step 4: Run the wsWriter test — expect PASS**

```bash
cd /Users/tan/nweb/retask-cli && go test -run TestWsWriterWrite ./internal/cmd/sandbox/
```

Expected: `ok  github.com/nwebxyz/retask-cli/internal/cmd/sandbox`

- [ ] **Step 5: Run all sandbox tests**

```bash
cd /Users/tan/nweb/retask-cli && go test ./internal/cmd/sandbox/
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add internal/cmd/sandbox/sessionlane.go internal/cmd/sandbox/connect_test.go
git commit -m "feat: add SessionManager, wsWriter, and session lane relay"
```

---

## Task 8: retask-cli — Full `connect.go` RunE

**Files:**
- Modify: `internal/cmd/sandbox/connect.go` (replace skeleton RunE with full implementation)
- Modify: `internal/cmd/sandbox/datalane.go` (remove `atomicStore` placeholder, use `sync/atomic`)

- [ ] **Step 1: Update `datalane.go` — replace `atomicStore` placeholder with `sync/atomic`**

In `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/datalane.go`:

1. Add `"sync/atomic"` to imports.
2. Remove the `atomicStore` helper function entirely.
3. Replace all `atomicStore(dl.connState, ...)` calls with `atomic.StoreInt32(dl.connState, ...)`.
4. Change `connState *int32` field type to remain `*int32` (compatible with `atomic.StoreInt32`).

The updated `DataLane` struct and its two store calls:

```go
// in imports: "sync/atomic"

// DataLane struct — no change needed, connState *int32 is already correct.

// In connectOnce, replace:
//   atomicStore(dl.connState, connStateConnected)
// with:
atomic.StoreInt32(dl.connState, connStateConnected)

// In Run, replace:
//   atomicStore(dl.connState, connStateError)
// with:
atomic.StoreInt32(dl.connState, connStateError)
```

Also remove the `atomicStore` function definition at the bottom of `datalane.go`.

- [ ] **Step 2: Replace `connect.go` with full implementation**

Replace the entire contents of `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/connect.go`:

```go
package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	connectrpc "connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	agentfleet "github.com/hoaitan/agentfleet"
	"github.com/hoaitan/agentfleet/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
	sandboxv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1/sandboxv1connect"
)

func newConnectCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "connect <id>",
		Short: "Connect this machine as a Private VM sandbox",
		Long: `Connect this machine as the execution backend for a Private VM sandbox.

This is a long-running command that maintains a persistent WebSocket connection
to sandbox-proxy and manages sessions as local PTY processes.

Usage example:
  retask sandbox connect sandbox_abc123

Environment:
  SANDBOX_PROXY_ENDPOINT  Proxy base URL (default: https://sandbox-proxy.prd.nweb.app/)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sandboxID := args[0]

			// Resolve credentials.
			path := gf.ConfigPath
			if path == "" {
				path = config.DefaultConfigPath()
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			profile := cfg.ActiveProfileData(gf.Profile)
			resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			jwt, err := resolver.Token(ctx)
			if err != nil {
				return err
			}

			// Build sandbox service client.
			httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
			baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
			svc := sandboxv1connect.NewSandboxServiceClient(httpClient, baseURL, client.Options(gf.Transport)...)

			// Validate sandbox type.
			sbResp, err := svc.GetSandbox(ctx, connectrpc.NewRequest(&commonv1.Id{Id: sandboxID}))
			if err != nil {
				return err
			}
			if sbResp.Msg.Type != sandboxv1.Sandbox_TYPE_PRIVATE {
				return fmt.Errorf("sandbox %q must be type PRIVATE (got %s)", sandboxID, sbResp.Msg.Type)
			}

			wsBase := proxyWSBase()
			sandboxLabel := fmt.Sprintf("%s (%s)", sbResp.Msg.Name, sbResp.Msg.SandboxId)

			// Connection state: 0=connecting, 1=connected, 2=error.
			var rawConnState int32
			atomic.StoreInt32(&rawConnState, connStateConnecting)

			// agentfleet config.
			fleetCfg := agentfleet.DefaultConfig()
			fleetCfg.Fleet.SocketDir = "" // no Unix socket server
			fleetCfg.TUI.Title = makeTitleFunc(&rawConnState, sandboxLabel)
			fleet := agentfleet.NewFleet(fleetCfg.Fleet)

			// Logger: non-nil only in headless mode.
			var logger *slog.Logger
			if !term.IsTerminal(int(os.Stdout.Fd())) {
				logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
			}

			sm := newSessionManager(sandboxID, wsBase, svc, fleet, fleetCfg.Fleet, fleetCfg.Agent, logger)
			dl := newDataLane(sandboxID, wsBase, jwt, sm, &rawConnState, logger)

			go dl.Run(ctx)

			if term.IsTerminal(int(os.Stdout.Fd())) {
				if err := tui.Run(ctx, fleet, fleetCfg.TUI, nil); err != nil {
					return err
				}
			} else {
				<-ctx.Done()
			}

			sm.StopAll()
			return nil
		},
	}
}

// proxyWSBase returns the WebSocket base URL for sandbox-proxy.
func proxyWSBase() string {
	ep := os.Getenv("SANDBOX_PROXY_ENDPOINT")
	if ep == "" {
		ep = "https://sandbox-proxy.prd.nweb.app/"
	}
	ep = strings.TrimRight(ep, "/")
	ep = strings.Replace(ep, "https://", "wss://", 1)
	ep = strings.Replace(ep, "http://", "ws://", 1)
	return ep
}

// makeTitleFunc returns a TUIConfig.Title func that reflects live connection state.
func makeTitleFunc(connState *int32, sandboxLabel string) func() string {
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))
	gray := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	return func() string {
		switch atomic.LoadInt32(connState) {
		case connStateConnected:
			return green.Render("●") + " connected  " + sandboxLabel
		case connStateError:
			return red.Render("●") + " error  " + sandboxLabel
		default:
			return gray.Render("○") + " connecting  " + sandboxLabel
		}
	}
}
```

- [ ] **Step 3: Build to verify everything compiles**

```bash
cd /Users/tan/nweb/retask-cli && go build ./...
```

Expected: no errors.

- [ ] **Step 4: Run all tests**

```bash
cd /Users/tan/nweb/retask-cli && go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add internal/cmd/sandbox/connect.go internal/cmd/sandbox/datalane.go
git commit -m "feat: implement sandbox connect RunE with TUI/headless mode"
```

---

## Task 9: retask-cli — Register command + help manifest

**Files:**
- Modify: `internal/cmd/sandbox/command.go`
- Modify: `internal/cmd/helpcmd/command.go`

- [ ] **Step 1: Add `newConnectCommand` to `command.go`**

In `/Users/tan/nweb/retask-cli/internal/cmd/sandbox/command.go`, in `NewCommand()`, add `newConnectCommand`:

```go
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes and sessions",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newCreateCommand(gf),
		newUpdateCommand(gf),
		newStopCommand(gf),
		newDeleteCommand(gf),
		newSessionCommand(gf),
		newConnectCommand(gf),
	)
	return cmd
}
```

- [ ] **Step 2: Add help manifest entry in `helpcmd/command.go`**

In `/Users/tan/nweb/retask-cli/internal/cmd/helpcmd/command.go`, append after the last `sandbox session delete` entry:

```go
{Command: "retask sandbox connect", Description: "Connect this machine as a Private VM sandbox (long-running)", Example: "retask sandbox connect <sandbox-id>"},
```

- [ ] **Step 3: Build and smoke-test the CLI**

```bash
cd /Users/tan/nweb/retask-cli
go build -ldflags "-X github.com/nwebxyz/retask-cli/internal/version.Version=dev" -o /tmp/retask ./cmd/retask/
/tmp/retask sandbox --help
```

Expected output includes:
```
  connect     Connect this machine as a Private VM sandbox
```

```bash
/tmp/retask sandbox connect --help
```

Expected: full Long description with usage example and SANDBOX_PROXY_ENDPOINT note.

- [ ] **Step 4: Run full test suite**

```bash
cd /Users/tan/nweb/retask-cli && go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
cd /Users/tan/nweb/retask-cli
git add internal/cmd/sandbox/command.go internal/cmd/helpcmd/command.go
git commit -m "feat: register sandbox connect command and add to help manifest"
```

---

## Self-review

**Spec coverage check:**

| Spec requirement | Task |
|---|---|
| `Runner.Resize()` | Task 1 |
| `AgentConfig.Env` isolated per child process | Task 2 |
| `TUIConfig.Title func() string` | Task 3 |
| `SANDBOX_PROXY_ENDPOINT` + `https→wss` | Task 5 |
| DataLane: ping/pong, new_session, stop_session, stop_sandbox, delete_sandbox | Task 6 |
| Exponential backoff reconnect (2s→30s cap) | Task 6 |
| SessionManager: GetSessionRuntime, PtyAgent, fleet.Add | Task 7 |
| wsWriter: base64 JSON encoding | Task 7 |
| readLoop: data + resize dispatch | Task 7 |
| TUI on TTY / headless slog on non-TTY | Task 8 |
| `sandboxLabel` = name + id in TUI title | Task 8 |
| Live connState dot (connecting/connected/error) | Task 8 |
| Command registration | Task 9 |
| Help manifest | Task 9 |

All spec sections covered. No gaps found.
