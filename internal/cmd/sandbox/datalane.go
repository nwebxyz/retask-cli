package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
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
	connState *int32  // atomic
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
		atomic.StoreInt32(dl.connState, connStateError)
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
func (dl *DataLane) connectOnce(ctx context.Context) error {
	url := fmt.Sprintf("%s/ws/data-lane?sandbox_id=%s&token=%s",
		dl.wsBase, dl.sandboxID, dl.jwt)

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow() //nolint:errcheck

	atomic.StoreInt32(dl.connState, connStateConnected)
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
