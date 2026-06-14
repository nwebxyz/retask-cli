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
