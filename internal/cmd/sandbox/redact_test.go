package sandbox

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeJWT = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJl-_x"

func TestRedactTokenInLaneURLs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "data lane url",
			in:   "wss://proxy/ws/data-lane?sandbox_id=sb_1&token=" + fakeJWT + "&client_version=0.23.0",
			want: "wss://proxy/ws/data-lane?sandbox_id=sb_1&token=REDACTED&client_version=0.23.0",
		},
		{
			name: "session lane url, token last",
			in:   "wss://proxy/ws/session-lane?sandbox_id=sb_1&session_id=s_1&token=" + fakeJWT,
			want: "wss://proxy/ws/session-lane?sandbox_id=sb_1&session_id=s_1&token=REDACTED",
		},
		{
			name: "token as the first parameter",
			in:   "wss://proxy/ws?token=" + fakeJWT + "&sandbox_id=sb_1",
			want: "wss://proxy/ws?token=REDACTED&sandbox_id=sb_1",
		},
		{
			name: "quoted url inside an error message",
			in:   `failed to dial: Get "wss://proxy/ws?token=` + fakeJWT + `": connection refused`,
			want: `failed to dial: Get "wss://proxy/ws?token=REDACTED": connection refused`,
		},
		{
			name: "two urls in one message",
			in:   "a?token=" + fakeJWT + " and b?token=" + fakeJWT,
			want: "a?token=REDACTED and b?token=REDACTED",
		},
		{
			name: "nothing to redact",
			in:   "dial tcp 127.0.0.1:59999: connect: connection refused",
			want: "dial tcp 127.0.0.1:59999: connect: connection refused",
		},
		{
			name: "leaves lookalike parameters alone",
			in:   "wss://proxy/ws?refresh_token_hint=keep&sandbox_id=sb_1",
			want: "wss://proxy/ws?refresh_token_hint=keep&sandbox_id=sb_1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactToken(tc.in)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, fakeJWT)
		})
	}
}

func TestRedactErrScrubsMessageAndKeepsChain(t *testing.T) {
	sentinel := errors.New("connection refused")
	wrapped := fmt.Errorf(`Get "wss://proxy/ws?token=%s": %w`, fakeJWT, sentinel)

	got := redactErr(wrapped)

	require.NotNil(t, got)
	assert.NotContains(t, got.Error(), fakeJWT)
	assert.Contains(t, got.Error(), "token=REDACTED")
	assert.ErrorIs(t, got, sentinel, "the original chain stays reachable")
}

func TestRedactErrPassesThroughCleanErrors(t *testing.T) {
	assert.Nil(t, redactErr(nil))

	clean := errors.New("connection refused")
	assert.Same(t, clean, redactErr(clean), "an error with no token is returned unchanged")
}
