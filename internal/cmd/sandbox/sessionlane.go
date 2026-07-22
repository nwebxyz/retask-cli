package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/coder/websocket"
	agentfleet "github.com/hoaitan/agentfleet"
	"google.golang.org/protobuf/encoding/protojson"

	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// SessionManager creates and tracks one agentfleet Runner per active sandbox session.
type SessionManager struct {
	sandboxID   string
	wsBase      string
	fleet       *agentfleet.Fleet
	fleetCfg    agentfleet.FleetConfig
	agentCfg    agentfleet.AgentConfig
	log         *slog.Logger
	workspaceID string
	sandboxName string
	baseDir     string
	endpoint    string
	autoRespond bool // auto-accept known agent prompts (e.g. folder-trust)

	mu       sync.Mutex
	sessions map[string]*sessionEntry // keyed by session_id
}

func newSessionManager(
	sandboxID, wsBase string,
	fleet *agentfleet.Fleet,
	fleetCfg agentfleet.FleetConfig,
	agentCfg agentfleet.AgentConfig,
	log *slog.Logger,
	workspaceID, sandboxName, baseDir, endpoint string,
	autoRespond bool,
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
		endpoint:    endpoint,
		autoRespond: autoRespond,
		sessions:    make(map[string]*sessionEntry),
	}
}

// Start handles a new_session event. It is IDEMPOTENT: if session_id already
// has a live runner (the relay lost its state, or the VM data-lane reconnected
// and the relay re-sent new_session), re-bind a fresh session-lane to the
// existing PTY instead of creating a second one.
func (sm *SessionManager) Start(ctx context.Context, sessionID, token, name string, configJSON json.RawMessage, systemPrompt, seedPrompt string, cols, rows int) {
	sm.mu.Lock()
	entry := sm.sessions[sessionID]
	sm.mu.Unlock()
	if entry != nil {
		sm.attach(ctx, entry, sessionID, token, cols, rows)
		return
	}
	sm.create(ctx, sessionID, token, name, configJSON, systemPrompt, seedPrompt, cols, rows)
}

// attach re-binds a fresh session-lane to an already-running runner. It never
// bootstraps or spawns a new PTY. The token is the fresh session-lane token
// from the re-sent new_session frame.
func (sm *SessionManager) attach(ctx context.Context, entry *sessionEntry, sessionID, token string, cols, rows int) {
	wsURL := fmt.Sprintf("%s/ws/session-lane?sandbox_id=%s&session_id=%s&token=%s",
		sm.wsBase, sm.sandboxID, sessionID, token)
	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		sm.logError("session_reattach_error", "session_id", sessionID, "error", err)
		return
	}
	sm.logInfo("session_lane_reattached", "sandbox_id", sm.sandboxID, "session_id", sessionID)

	// Swap in the new conn AND repoint output under the entry lock, so this
	// never races a concurrent detach's SetOutput(io.Discard).
	old := entry.swapConn(wsConn, func() {
		entry.runner.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})
	})
	if old != nil {
		old.CloseNow() //nolint:errcheck
	}
	if cols > 0 && rows > 0 {
		entry.runner.Resize(rows, cols) //nolint:errcheck
	}
	go sm.readPump(ctx, entry, sessionID, wsConn)
}

// create bootstraps a brand-new session: connects a session lane, runs
// bootstrap, launches a new PTY, and bridges it. Called only when session_id is
// not already tracked.
func (sm *SessionManager) create(ctx context.Context, sessionID, token, name string, configJSON json.RawMessage, systemPrompt, seedPrompt string, cols, rows int) {
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
	sm.logInfo("session_lane_connected", "sandbox_id", sm.sandboxID, "session_id", sessionID)

	// pending latches window sizes that arrive before the PTY exists: the
	// initial geometry reported in the new_session frame, or a resize that
	// races bootstrap. It is flushed once the PTY is up.
	pending := &pendingSize{}
	if cols > 0 && rows > 0 {
		pending.store(rows, cols)
	}

	// Run bootstrap — writes files, clones repos, builds env.
	sb := &SessionBootstrap{
		SessionID:    sessionID,
		SessionName:  name,
		SandboxID:    sm.sandboxID,
		SandboxName:  sm.sandboxName,
		WorkspaceID:  sm.workspaceID,
		Config:       &cfg,
		SystemPrompt: systemPrompt,
		SeedPrompt:   seedPrompt,
		Endpoint:     sm.endpoint,
		BaseDir:      sm.baseDir,
		Log:          sm.log,
		Pending:      pending,
	}
	sessionDir, env, err := sb.Run(ctx, wsConn)
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
	shellCmd := fmt.Sprintf("cd '%s' && %s", sessionDir, initCommand)

	agCfg := sm.agentCfg
	agCfg.Env = env
	// Start the PTY at the browser's real geometry when the client reported
	// one, so the session never renders at a default the user cannot see.
	if cols > 0 && rows > 0 {
		agCfg.PTYCols = cols
		agCfg.PTYRows = rows
	}

	sm.logInfo("session_starting", "session_id", sessionID, "name", name, "init_command", initCommand)
	ag := agentfleet.NewPtyAgent([]string{"sh", "-c", shellCmd}, agCfg)
	task := &agentfleet.BasicTask{TaskID: sessionID, TaskName: name, Cmd: initCommand}
	r := agentfleet.NewRunner(task, ag, sm.fleetCfg, agCfg)
	r.Start()

	if err := sm.fleet.Add(ctx, r); err != nil {
		r.Stop()          //nolint:errcheck
		wsConn.CloseNow() //nolint:errcheck
		return
	}

	entry := newSessionEntry(r, wsConn)
	sm.mu.Lock()
	sm.sessions[sessionID] = entry
	sm.mu.Unlock()

	r.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})

	// (geometry apply block stays exactly as before — it uses `r` and `pending`)
	if pRows, pCols, ok := pending.take(); ok {
		if err := applyResize(r, pRows, pCols, 20, 50*time.Millisecond); err != nil {
			pending.store(pRows, pCols)
			sm.logError("session_initial_resize_failed", "session_id", sessionID, "error", err)
		} else {
			sm.logInfo("session_initial_resize", "session_id", sessionID, "rows", pRows, "cols", pCols)
		}
	}

	if sm.autoRespond {
		go newPromptWatcher(r.Lines, r.StdinWriter(), defaultPromptRules(), defaultPollInterval, defaultPromptWindow, sm.log).Run(ctx)
	}

	// Read pump: bridge the session-lane socket to the PTY stdin. A socket
	// error detaches (see readPump) rather than stopping the agent.
	go sm.readPump(ctx, entry, sessionID, wsConn)

	// PTY-exit watcher: closes whichever socket is currently attached and
	// removes the session. currentConn() (not a captured local) so a re-attached
	// socket is the one closed.
	go func() {
		<-r.Done()
		if c := entry.currentConn(); c != nil {
			c.Close(websocket.StatusNormalClosure, "session ended") //nolint:errcheck
		}
		sm.mu.Lock()
		delete(sm.sessions, sessionID)
		sm.mu.Unlock()
		sm.fleet.Remove(sessionID)
		sm.logInfo("session_stopped", "session_id", sessionID)
	}()
}

// readPump bridges the session-lane socket to the runner's stdin until the
// socket errors. A socket error means the viewer/relay went away — the PTY is
// left RUNNING (detached) and re-bound on the next new_session. The runner is
// NEVER stopped here; only an explicit Stop/Remove ends it (teardown contract).
func (sm *SessionManager) readPump(ctx context.Context, entry *sessionEntry, sessionID string, conn *websocket.Conn) {
	err := sm.readLoop(ctx, conn, entry.runner, sessionID)
	sm.logInfo("session_lane_detached", "session_id", sessionID, "error", err)
	// Detach + redirect output to io.Discard atomically under the entry lock, so
	// this never clobbers the output a concurrent re-attach installed. The PTY
	// keeps running either way.
	entry.detachIfCurrent(conn, func() {
		entry.runner.SetOutput(io.Discard)
	})
}

func (sm *SessionManager) readLoop(ctx context.Context, conn *websocket.Conn, r *agentfleet.Runner, sessionID string) error {
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return err
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
			sm.logInfo("session_resize", "session_id", sessionID, "rows", msg.Rows, "cols", msg.Cols)
			// Values <= 0 are ignored; values above maxPTYDimension are
			// clamped down to it rather than ignored (see clampDimension).
			if msg.Rows > 0 && msg.Cols > 0 {
				if err := applyResize(r, clampDimension(msg.Rows), clampDimension(msg.Cols), 5, 50*time.Millisecond); err != nil {
					sm.logError("session_resize_failed", "session_id", sessionID, "error", err)
				}
			}
		}
	}
}

// Stop sends SIGTERM to the session's PTY process (keeps the folder).
func (sm *SessionManager) Stop(sessionID string) {
	sm.logInfo("session_stopping", "session_id", sessionID)
	sm.mu.Lock()
	entry := sm.sessions[sessionID]
	sm.mu.Unlock()
	if entry != nil {
		entry.runner.Stop() //nolint:errcheck
	}
}

// Remove stops the session's PTY, drops it from the fleet, and deletes its
// folder. Used for delete_session.
func (sm *SessionManager) Remove(sessionID string) {
	sm.logInfo("session_removing", "session_id", sessionID)
	sm.mu.Lock()
	entry := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()
	if entry != nil {
		entry.runner.Stop() //nolint:errcheck
		sm.fleet.Remove(sessionID)
	}
	os.RemoveAll(filepath.Join(sm.baseDir, "session-"+sessionID)) //nolint:errcheck
}

// StopAll stops every active session.
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

// wsWriter implements io.Writer, encoding bytes as base64 JSON to the session-lane WebSocket.
type wsWriter struct {
	ctx  context.Context
	conn *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
	msg, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{"data", base64.StdEncoding.EncodeToString(p)})
	// Swallow transport errors: a dead session-lane socket must never propagate
	// failure into the agent's output path. The read pump detects the dead
	// socket and detaches (redirecting output to io.Discard); the runner keeps
	// running regardless of this socket's health.
	_ = w.conn.Write(w.ctx, websocket.MessageText, msg) //nolint:errcheck
	return len(p), nil
}
