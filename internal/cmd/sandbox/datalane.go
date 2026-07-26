package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/nwebxyz/retask-cli/internal/version"
)

const (
	connStateConnecting int32 = 0
	connStateConnected  int32 = 1
	connStateError      int32 = 2
)

// Shared reconnect backoff for both lanes (data lane and session lane): start at
// reconnectInitialBackoff, double on each failure, cap at reconnectMaxBackoff,
// reset on a successful connect.
const (
	reconnectInitialBackoff = 2 * time.Second
	reconnectMaxBackoff     = 30 * time.Second
)

var errSandboxDeleted = errors.New("sandbox deleted")

type dataLaneMsgNewSession struct {
	Name         string          `json:"name,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	SystemPrompt string          `json:"system_prompt,omitempty"`
	SeedPrompt   string          `json:"seed_prompt,omitempty"`
	// Browser terminal geometry at connect time. Zero when the client did
	// not report one; the session PTY defaults then apply.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

type dataLaneMsg struct {
	Type       string                 `json:"type"`
	SessionID  string                 `json:"session_id,omitempty"`
	SandboxID  string                 `json:"sandbox_id,omitempty"`
	Token      string                 `json:"token,omitempty"`
	NewSession *dataLaneMsgNewSession `json:"new_session,omitempty"`
}

// DataLane manages the persistent reverse WebSocket to sandbox-proxy.
// It dispatches control messages to a SessionManager.
type DataLane struct {
	sandboxID string
	wsBase    string
	jwt       string
	sessions  *SessionManager
	connState *int32       // atomic
	log       *slog.Logger // nil in TUI mode
	sendCh    chan dataLaneMsg
}

func newDataLane(sandboxID, wsBase, jwt string, sessions *SessionManager, connState *int32, log *slog.Logger) *DataLane {
	return &DataLane{
		sandboxID: sandboxID,
		wsBase:    wsBase,
		jwt:       jwt,
		sessions:  sessions,
		connState: connState,
		log:       log,
		sendCh:    make(chan dataLaneMsg, 8),
	}
}

// Send queues a message to be written to the active data lane connection.
// Drops silently if the buffer is full or no connection is active.
func (dl *DataLane) Send(msg dataLaneMsg) {
	select {
	case dl.sendCh <- msg:
	default:
	}
}

// Run connects to the data lane and dispatches messages until ctx is cancelled
// or a delete_sandbox message is received. Reconnects with exponential backoff.
func (dl *DataLane) Run(ctx context.Context) {
	backoff := reconnectInitialBackoff
	for {
		err := dl.connectOnce(ctx)
		if err == nil || errors.Is(err, errSandboxDeleted) || ctx.Err() != nil {
			return
		}
		atomic.StoreInt32(dl.connState, connStateError)
		dl.logWarn("disconnected", "error", err, "retrying_in", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, reconnectMaxBackoff)
	}
}

// connectOnce dials the data lane and reads messages until an error or delete_sandbox.
func (dl *DataLane) connectOnce(ctx context.Context) error {
	dialURL := fmt.Sprintf("%s/ws/data-lane?sandbox_id=%s&token=%s&client_version=%s",
		dl.wsBase, dl.sandboxID, dl.jwt, url.QueryEscape(version.Version))

	conn, _, err := websocket.Dial(ctx, dialURL, nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow() //nolint:errcheck

	atomic.StoreInt32(dl.connState, connStateConnected)
	fmt.Fprintf(os.Stderr, "data lane: %s/ws/data-lane?sandbox_id=%s\n", dl.wsBase, dl.sandboxID)
	dl.logInfo("connected", "sandbox_id", dl.sandboxID)

	// Writer goroutine: drains sendCh and writes to conn.
	// Uses a local cancel so it exits when this connection closes.
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()
	go func() {
		for {
			select {
			case msg := <-dl.sendCh:
				raw, _ := json.Marshal(msg)
				conn.Write(connCtx, websocket.MessageText, raw) //nolint:errcheck
			case <-connCtx.Done():
				return
			}
		}
	}()

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
			if msg.NewSession == nil {
				dl.logWarn("new_session_missing_payload", "session_id", msg.SessionID)
				continue
			}
			go dl.sessions.Start(ctx, msg.SessionID, msg.Token, msg.NewSession.Name, msg.NewSession.Config, msg.NewSession.SystemPrompt, msg.NewSession.SeedPrompt, msg.NewSession.Cols, msg.NewSession.Rows)

		case "stop_session":
			dl.logInfo("stop_session", "session_id", msg.SessionID)
			dl.sessions.Stop(msg.SessionID)

		case "delete_session":
			dl.logInfo("delete_session", "session_id", msg.SessionID)
			dl.sessions.Remove(msg.SessionID)

		case "stop_sandbox":
			dl.logInfo("stop_sandbox", "sandbox_id", msg.SandboxID)
			dl.sessions.StopAll()

		case "delete_sandbox":
			dl.logInfo("delete_sandbox", "sandbox_id", msg.SandboxID)
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
