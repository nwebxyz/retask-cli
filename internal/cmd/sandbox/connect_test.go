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
	agentfleet "github.com/hoaitan/agentfleet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nwebxyz/retask-cli/internal/flags"
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

func TestVersionLabel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"dev", "[dev]"},
		{"", "[dev]"},
		{"0.1.0", "[v0.1.0]"},
		{"1.2.3", "[v1.2.3]"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, versionLabel(tc.in), "in=%q", tc.in)
	}
}

func TestSessionWriter_LiveWriteOverRealSocket(t *testing.T) {
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

	ww := newSessionWriter(ctx, conn, 1024)
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

func TestSessionAgentConfig_Is80x90(t *testing.T) {
	a := sessionAgentConfig()
	assert.Equal(t, 80, a.PTYCols, "session terminal width")
	assert.Equal(t, 90, a.PTYRows, "session terminal height")
}

func TestConnectMaxSessionsDefaultsTo99(t *testing.T) {
	f := newConnectCommand(&flags.Global{}).Flags().Lookup("max-sessions")
	require.NotNil(t, f, "--max-sessions must be registered")
	assert.Equal(t, "99", f.DefValue)
}

// agentfleet silently turns a limit <= 0 into 9, so these must be refused
// rather than passed through.
func TestConnectRejectsMaxSessionsBelowOne(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			cmd := newConnectCommand(&flags.Global{})
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs([]string{"sandbox_abc123", "--max-sessions=" + value})

			err := cmd.Execute()

			require.Error(t, err)
			assert.EqualError(t, err, "invalid --max-sessions "+value+": must be at least 1")
		})
	}
}

func TestSessionFleetConfigAppliesMaxSessions(t *testing.T) {
	const limit = 10 // above agentfleet's default of 9
	cfg := sessionFleetConfig(limit)
	fleet := agentfleet.NewFleet(cfg.Fleet)
	newRunner := func() *agentfleet.Runner {
		task := &agentfleet.BasicTask{TaskID: "s", TaskName: "s", Cmd: "sh"}
		return agentfleet.NewRunner(task, agentfleet.NewMockAgent(), cfg.Fleet, cfg.Agent)
	}

	for i := range limit {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := fleet.Add(ctx, newRunner())
		cancel()
		require.NoError(t, err, "session %d should get a slot", i+1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, fleet.Add(ctx, newRunner()), context.DeadlineExceeded,
		"the session after the limit should wait for a slot")
}

func TestSessionFleetConfigKeepsSessionPTYSize(t *testing.T) {
	assert.Equal(t, sessionAgentConfig(), sessionFleetConfig(1).Agent)
}
