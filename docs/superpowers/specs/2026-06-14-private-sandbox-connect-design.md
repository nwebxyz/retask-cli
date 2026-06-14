# Private Sandbox Connect — Design Spec

**Date:** 2026-06-14
**Status:** Approved

---

## Overview

Add `retask sandbox connect <sandbox-id>` — a long-running command that registers this machine as the execution backend for a Private VM sandbox. It maintains a persistent reverse WebSocket connection to sandbox-proxy, receives session dispatch messages, and manages each session as an agentfleet Runner (PTY process + I/O relay).

Two companion repositories are also touched:
- **`github.com/hoaitan/agentfleet`** — three small additive changes
- **`github.com/nwebxyz/retask-cli`** — new command + WebSocket dependency

---

## Architecture

```
retask sandbox connect
        │
        ├─[data lane WS]──► sandbox-proxy PrivateSandboxRelay DO
        │   ping/pong, new_session, stop_session, stop_sandbox, delete_sandbox
        │
        └─ per session: [session lane WS]──► sandbox-proxy ──► FE browser
                         raw PTY I/O (framed JSON: data + resize)
                         ↕
                    agentfleet Runner (PtyAgent)
```

### Connection flows

**Data lane registration:**
1. CLI resolves JWT (existing auth.Resolver)
2. `GetSandbox(id)` → validate `type == PRIVATE`
3. Build WebSocket URL from `SANDBOX_PROXY_ENDPOINT` env var (default `https://sandbox-proxy.prd.nweb.app/`), auto-map `https→wss` / `http→ws`
4. Connect: `wss://<proxy>/ws/data-lane?sandbox_id=<id>&token=<jwt>`
5. Proxy validates token via `GetSandboxRuntime` (BE-BE), reports sandbox `READY`

**Session lifecycle (per `new_session` message):**
1. Receive `{"type":"new_session","session_id":"Y","token":"<rand>"}` on data lane
2. Call `GetSessionRuntime(session_id)` → get init command + resolved env vars
3. Create `PtyAgent` with init command + env vars
4. Create `Runner`, start it, add to `Fleet`
5. Open session lane: `wss://<proxy>/ws/session-lane?sandbox_id=X&session_id=Y&token=<rand>`
6. Relay: agent stdout → session lane (base64 JSON); session lane → agent stdin + resize

---

## Section 1: agentfleet changes

Three additive changes — no breaking changes, no new interfaces.

### 1.1 `TUIConfig.Title func() string` (`config.go`)

```go
type TUIConfig struct {
    Title        func() string // header title func; nil = "◈ agentfleet"
    Columns      int
    PreviewLines int
    CardWidth    int
    RefreshRate  time.Duration
    AutoOpen     bool
}
```

`renderHeader` in `tui/tui.go` calls `m.cfg.Title()` when non-nil, falls back to `"◈ agentfleet"`:

```go
func renderHeader(m model) string {
    title := styleTitle.Render("◈ agentfleet")
    if m.cfg.Title != nil {
        title = m.cfg.Title()
    }
    // ...
}
```

retask-cli constructs a closure that reads an `atomic.Int32` (connection state) and a captured `sandboxLabel` string to produce a live, styled header:

```go
sandboxLabel := fmt.Sprintf("%s (%s)", sandbox.Name, sandbox.SandboxId)
var connState atomic.Int32  // 0=connecting, 1=connected, 2=error

cfg.TUI.Title = func() string {
    switch connState.Load() {
    case 1:
        return lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80")).Render("●") +
               " connected  " + sandboxLabel
    case 2:
        return lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Render("●") +
               " error  " + sandboxLabel
    default:
        return lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("○") +
               " connecting  " + sandboxLabel
    }
}
```

`DataLane` holds a pointer to `connState` and updates it on connect/disconnect/error. The TUI picks it up on the next 500ms tick with no extra signaling.

### 1.2 `AgentConfig.Env []string` (`config.go`)

```go
type AgentConfig struct {
    PTYRows int
    PTYCols int
    Env     []string // extra env vars (KEY=VALUE); appended to os.Environ() for child process only
}
```

`PtyAgent.Start()` sets `a.cmd.Env = append(os.Environ(), a.cfg.Env...)` before `cmd.Start()`. Isolated per child process — does not modify the Go process environment.

### 1.3 `Runner.Resize(rows, cols int) error` (`runner.go`)

```go
func (r *Runner) Resize(rows, cols int) error { return r.ag.Resize(rows, cols) }
```

---

## Section 2: `retask sandbox connect` command structure

### New files

```
internal/cmd/sandbox/
  command.go      (existing — add newConnectCommand)
  connect.go      (new — cobra command, wiring, TUI/headless)
  datalane.go     (new — DataLane struct, WebSocket, control dispatch)
  sessionlane.go  (new — SessionManager, per-session PTY + I/O relay)
```

### New dependency

```
github.com/coder/websocket
```

Added to `go.mod`. No other dependencies change.

### `connect.go` — RunE flow

```
1. Resolve JWT via auth.Resolver (same as all commands)
2. GetSandbox(id)
   - error if sandbox not found / no permission
   - error if sandbox.Type != PRIVATE ("sandbox must be type PRIVATE")
3. Build proxy WebSocket base URL:
   SANDBOX_PROXY_ENDPOINT env (default https://sandbox-proxy.prd.nweb.app/)
   → strip trailing slash → replace https:// with wss:// (or http:// with ws://)
4. Build sandboxLabel = fmt.Sprintf("%s (%s)", sandbox.Name, sandbox.SandboxId)
5. Set up atomic connState + TUI title func (see §1.1)
6. Create Fleet + SessionManager
   - Fleet.SocketDir = ""   (no Unix socket server needed)
   - Fleet.LogDir = /tmp    (session output logs)
7. Create DataLane, passing &connState + SessionManager
8. Set up context with signal.NotifyContext (SIGINT, SIGTERM)
9. Launch DataLane.Run in goroutine
10. if term.IsTerminal(os.Stdout.Fd()):
       tui.Run(ctx, fleet, cfg.TUI, nil)   // blocks until ctx cancelled or q/^C
    else:
       headlessLoop(ctx, fleet)            // structured slog lines to stderr
11. On exit: SessionManager.StopAll()
```

### `SANDBOX_PROXY_ENDPOINT` resolution

```go
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

---

## Section 3: Data Lane (`datalane.go`)

### `DataLane` struct

```go
type DataLane struct {
    sandboxID string
    wsBase    string
    jwt       string
    sessions  *SessionManager
    connState *atomic.Int32
    log       *slog.Logger   // non-nil in headless mode, nil in TUI mode
}
```

### Connection URL

```
{wsBase}/ws/data-lane?sandbox_id={sandboxID}&token={jwt}
```

### Run loop

Blocks until ctx is cancelled or `delete_sandbox` is received. Reconnects with exponential backoff (2s → 4s → 8s → … → 30s cap, reset on successful read).

```
func (dl *DataLane) Run(ctx context.Context) error:
  loop:
    connect WebSocket
    on error: update connState=error, wait backoff, retry
    update connState=connected
    for:
      read next JSON message
      switch msg.type:
        "ping"            → write {"type":"pong"}
        "new_session"     → go sessions.Start(ctx, msg.session_id, msg.token)
        "stop_session"    → sessions.Stop(msg.session_id)
        "stop_sandbox"    → sessions.StopAll()  // stay connected, wait for new sessions
        "delete_sandbox"  → sessions.StopAll(); return nil
      on read error:
        update connState=error
        apply backoff, retry (unless ctx cancelled)
```

### Heartbeat

The proxy pings every 60s; the CLI replies with `pong`. No client-side timer needed — the proxy owns the heartbeat cadence.

### Headless log lines (slog, text format to stderr)

| Event | Level | Fields |
|---|---|---|
| Connected | INFO | `sandbox_id`, `name` |
| New session | INFO | `session_id` |
| Stop session | INFO | `session_id` |
| Stop sandbox | INFO | — |
| Delete sandbox | INFO | — |
| Disconnected / retrying | WARN | `retrying_in` |
| Session runtime error | ERROR | `session_id`, `error` |

---

## Section 4: Session Lane (`sessionlane.go`)

### `SessionManager` struct

```go
type SessionManager struct {
    sandboxID string
    wsBase    string
    svc       sandboxv1connect.SandboxServiceClient
    fleet     *agentfleet.Fleet
    fleetCfg  agentfleet.FleetConfig
    agentCfg  agentfleet.AgentConfig

    mu       sync.Mutex
    sessions map[string]*agentfleet.Runner  // keyed by session_id
}
```

### `Start(ctx, sessionID, token)` flow

```
1. GetSessionRuntime(sessionID)
   on error: log ERROR, return (FE WS times out on proxy side naturally)

2. Build env slice from runtime.Sandbox.Config.EnvVars:
   for each EnvVar:
     if EnvVar.Secret != nil: "KEY=secret.value"
     else:                    "KEY=plain"

3. initCommand = runtime.Sandbox.Config.SessionInitCommand

4. Create PtyAgent:
   ag := agentfleet.NewPtyAgent(
       strings.Fields(initCommand),
       agentfleet.AgentConfig{PTYRows: 24, PTYCols: 220, Env: envSlice},
   )

5. Task display name:
   name = runtime.Session.Name (fallback: sessionID)
   task := &agentfleet.BasicTask{TaskID: sessionID, TaskName: name, Cmd: initCommand}

6. Create Runner, start, add to Fleet:
   r := agentfleet.NewRunner(task, ag, fleetCfg, agentCfg)
   r.Start()
   fleet.Add(ctx, r)
   sessions[sessionID] = r

7. Connect session-lane WebSocket:
   url = fmt.Sprintf("%s/ws/session-lane?sandbox_id=%s&session_id=%s&token=%s",
                      wsBase, sandboxID, sessionID, token)

8. I/O relay:
   r.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})   // agent stdout → WS
   go readLoop(ctx, wsConn, r)                        // WS → agent stdin + resize

9. Cleanup goroutine:
   go func() {
       <-r.Done()
       wsConn.Close(websocket.StatusNormalClosure, "session ended")
       sm.mu.Lock(); delete(sm.sessions, sessionID); sm.mu.Unlock()
   }()
```

### `wsWriter` — `io.Writer` → session-lane WebSocket

```go
type wsWriter struct {
    ctx  context.Context
    conn *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
    msg, _ := json.Marshal(struct {
        Type string `json:"type"`
        Data string `json:"data"`
    }{"data", base64.StdEncoding.EncodeToString(p)})
    return len(p), w.conn.Write(w.ctx, websocket.MessageText, msg)
}
```

### `readLoop` — session-lane WebSocket → agent stdin + resize

```go
for {
    _, raw, err := wsConn.Read(ctx)
    if err != nil { return }
    var msg struct {
        Type string `json:"type"`
        Data string `json:"data"`
        Rows int    `json:"rows"`
        Cols int    `json:"cols"`
    }
    if json.Unmarshal(raw, &msg) != nil { continue }
    switch msg.Type {
    case "data":
        b, _ := base64.StdEncoding.DecodeString(msg.Data)
        r.StdinWriter().Write(b)
    case "resize":
        r.Resize(msg.Rows, msg.Cols)
    }
}
```

### `Stop(sessionID)` and `StopAll()`

```go
func (sm *SessionManager) Stop(sessionID string) {
    sm.mu.Lock()
    r := sm.sessions[sessionID]
    sm.mu.Unlock()
    if r != nil { r.Stop() }  // SIGTERM → runner exits → cleanup goroutine fires
}

func (sm *SessionManager) StopAll() {
    sm.mu.Lock()
    ids := make([]string, 0, len(sm.sessions))
    for id := range sm.sessions { ids = append(ids, id) }
    sm.mu.Unlock()
    for _, id := range ids { sm.Stop(id) }
}
```

---

## Section 5: Remaining details

### Help manifest

One new entry in `internal/cmd/helpcmd/command.go` after the existing sandbox session entries:

```go
{
    Command:     "retask sandbox connect",
    Description: "Connect this machine as a Private VM sandbox (long-running)",
    Example:     "retask sandbox connect <sandbox-id>",
},
```

### Environment variables

| Var | Default | Purpose |
|---|---|---|
| `SANDBOX_PROXY_ENDPOINT` | `https://sandbox-proxy.prd.nweb.app/` | Sandbox proxy base URL |

### Error behaviour summary

| Situation | Behaviour |
|---|---|
| Sandbox not found / no permission | Fatal error, command exits |
| Sandbox type != PRIVATE | Fatal error, command exits |
| Data lane connect failure | Retry with backoff; update connState=error |
| `GetSessionRuntime` failure | Log ERROR, skip session (data lane stays alive) |
| Session PTY exits | Cleanup goroutine closes session-lane WS, removes from session map |
| SIGINT / SIGTERM | StopAll sessions, exit cleanly |

---

## Out of scope

- `retask sandbox connect` reconnect token refresh (JWT expiry during long-lived connection — follow-up)
- Session replay (handled entirely by sandbox-proxy R2, transparent to CLI)
- TUI attach flow for private sandbox sessions (the socket server is disabled; attach is handled via session-lane WS)
