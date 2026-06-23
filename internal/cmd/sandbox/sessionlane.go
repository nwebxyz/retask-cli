package sandbox

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
	jwt         string
	endpoint    string
	autoRespond bool // auto-accept known agent prompts (e.g. folder-trust)

	mu       sync.Mutex
	sessions map[string]*agentfleet.Runner // keyed by session_id
}

func newSessionManager(
	sandboxID, wsBase string,
	fleet *agentfleet.Fleet,
	fleetCfg agentfleet.FleetConfig,
	agentCfg agentfleet.AgentConfig,
	log *slog.Logger,
	workspaceID, sandboxName, baseDir, jwt, endpoint string,
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
		jwt:         jwt,
		endpoint:    endpoint,
		autoRespond: autoRespond,
		sessions:    make(map[string]*agentfleet.Runner),
	}
}

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
	sm.logInfo("session_lane_connected", "sandbox_id", sm.sandboxID, "session_id", sessionID)

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
	shellCmd := fmt.Sprintf("cd '%s' && %s", sessionDir, initCommand)

	agCfg := sm.agentCfg
	agCfg.Env = env

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

	sm.mu.Lock()
	sm.sessions[sessionID] = r
	sm.mu.Unlock()

	r.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})
	if sm.autoRespond {
		// Watch the session's rendered screen (via the emulator) for known
		// startup prompts (e.g. Claude Code's folder-trust dialog) and inject
		// the accept keystroke once, so unattended sessions don't stall waiting
		// for a human. Stops once every rule has fired or the watch window ends.
		go newPromptWatcher(r.Lines, r.StdinWriter(), defaultPromptRules(), defaultPollInterval, defaultPromptWindow, sm.log).Run(ctx)
	}
	go func() {
		err := sm.readLoop(ctx, wsConn, r, sessionID)
		sm.logInfo("session_lane_closed", "session_id", sessionID, "error", err)
		r.Stop() //nolint:errcheck
	}()

	go func() {
		<-r.Done()
		wsConn.Close(websocket.StatusNormalClosure, "session ended") //nolint:errcheck
		sm.mu.Lock()
		delete(sm.sessions, sessionID)
		sm.mu.Unlock()
		sm.fleet.Remove(sessionID)
		sm.logInfo("session_stopped", "session_id", sessionID)
	}()
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
			r.Resize(msg.Rows, msg.Cols) //nolint:errcheck
		}
	}
}

// Stop sends SIGTERM to the session's PTY process.
func (sm *SessionManager) Stop(sessionID string) {
	sm.logInfo("session_stopping", "session_id", sessionID)
	sm.mu.Lock()
	r := sm.sessions[sessionID]
	sm.mu.Unlock()
	if r != nil {
		r.Stop() //nolint:errcheck
	}
}

// Remove stops the session's PTY and removes it from the fleet so it
// disappears from the TUI immediately. Used for delete_session messages.
func (sm *SessionManager) Remove(sessionID string) {
	sm.logInfo("session_removing", "session_id", sessionID)
	sm.mu.Lock()
	r := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()
	if r != nil {
		r.Stop() //nolint:errcheck
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
	if err := w.conn.Write(w.ctx, websocket.MessageText, msg); err != nil {
		return 0, err
	}
	return len(p), nil
}
