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
