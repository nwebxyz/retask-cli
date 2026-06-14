package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/coder/websocket"
	agentfleet "github.com/hoaitan/agentfleet"
)

// SessionManager creates and tracks one agentfleet Runner per active sandbox session.
type SessionManager struct {
	sandboxID string
	wsBase    string
	fleet     *agentfleet.Fleet
	fleetCfg  agentfleet.FleetConfig
	agentCfg  agentfleet.AgentConfig
	log       *slog.Logger

	mu       sync.Mutex
	sessions map[string]*agentfleet.Runner // keyed by session_id
}

func newSessionManager(
	sandboxID, wsBase string,
	fleet *agentfleet.Fleet,
	fleetCfg agentfleet.FleetConfig,
	agentCfg agentfleet.AgentConfig,
	log *slog.Logger,
) *SessionManager {
	return &SessionManager{
		sandboxID: sandboxID,
		wsBase:    wsBase,
		fleet:     fleet,
		fleetCfg:  fleetCfg,
		agentCfg:  agentCfg,
		log:       log,
		sessions:  make(map[string]*agentfleet.Runner),
	}
}

// Start handles a new_session event: launches PTY and connects session lane.
// initCommand and env come from the data lane new_session message.
func (sm *SessionManager) Start(ctx context.Context, sessionID, token, name, initCommand string, env map[string]string) {
	if initCommand == "" {
		sm.logError("session_no_init_command", "session_id", sessionID)
		return
	}
	if name == "" {
		name = sessionID
	}

	var envSlice []string
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}

	agCfg := sm.agentCfg
	agCfg.Env = envSlice

	sm.logInfo("session_starting", "session_id", sessionID, "name", name, "init_command", initCommand)
	ag := agentfleet.NewPtyAgent([]string{"sh", "-c", initCommand}, agCfg)
	task := &agentfleet.BasicTask{TaskID: sessionID, TaskName: name, Cmd: initCommand}
	r := agentfleet.NewRunner(task, ag, sm.fleetCfg, agCfg)
	r.Start()

	if err := sm.fleet.Add(ctx, r); err != nil {
		r.Stop() //nolint:errcheck
		return
	}

	wsURL := fmt.Sprintf("%s/ws/session-lane?sandbox_id=%s&session_id=%s&token=%s",
		sm.wsBase, sm.sandboxID, sessionID, token)
	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		sm.logError("session_lane_error", "session_id", sessionID, "error", err)
		r.Stop() //nolint:errcheck
		return
	}
	fmt.Fprintf(os.Stderr, "session lane: %s/ws/session-lane?sandbox_id=%s&session_id=%s\n",
		sm.wsBase, sm.sandboxID, sessionID)

	sm.mu.Lock()
	sm.sessions[sessionID] = r
	sm.mu.Unlock()

	r.SetOutput(&wsWriter{ctx: ctx, conn: wsConn})
	go func() {
		err := sm.readLoop(ctx, wsConn, r)
		sm.logInfo("session_lane_closed", "session_id", sessionID, "error", err)
		r.Stop() //nolint:errcheck  // ensure PTY exits when WS disconnects
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

func (sm *SessionManager) readLoop(ctx context.Context, conn *websocket.Conn, r *agentfleet.Runner) error {
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
			r.Resize(msg.Rows, msg.Cols) //nolint:errcheck
		}
	}
}

// Stop sends SIGTERM to the session's PTY process.
func (sm *SessionManager) Stop(sessionID string) {
	sm.mu.Lock()
	r := sm.sessions[sessionID]
	sm.mu.Unlock()
	if r != nil {
		r.Stop() //nolint:errcheck
	}
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
