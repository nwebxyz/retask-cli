package sandbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// connectOutcome scripts one pass through the data lane's connect loop:
// established says whether the socket came up before the error was returned.
type connectOutcome struct{ established bool }

// runBackoffScript drives DataLane.Run against a scripted sequence of connect
// outcomes and returns the retry delay it logged after each one. The loop is
// terminated by errSandboxDeleted once the script runs out, so Run returns and
// the log buffer has a single writer.
func runBackoffScript(t *testing.T, outcomes []connectOutcome) []string {
	t.Helper()

	var buf bytes.Buffer
	var connState int32
	atomic.StoreInt32(&connState, connStateConnecting)
	dl := newDataLane("sb", "wss://test", staticToken("jwt"), nil, &connState,
		slog.New(slog.NewTextHandler(&buf, nil)))
	dl.reconnectInitial = time.Millisecond
	dl.reconnectMax = 8 * time.Millisecond

	attempt := 0
	dl.connect = func(context.Context) (established bool, err error) {
		if attempt >= len(outcomes) {
			return false, errSandboxDeleted // unwinds Run
		}
		o := outcomes[attempt]
		attempt++
		return o.established, errors.New("lane dropped")
	}

	dl.Run(context.Background())

	var delays []string
	for _, line := range strings.Split(buf.String(), "\n") {
		_, rest, found := strings.Cut(line, "retrying_in=")
		if !found {
			continue
		}
		delay, _, _ := strings.Cut(rest, " ")
		delays = append(delays, delay)
	}
	return delays
}

// A dial that never succeeds must escalate and then hold at the cap.
func TestDataLaneRun_BackoffEscalatesToCap(t *testing.T) {
	delays := runBackoffScript(t, []connectOutcome{
		{established: false},
		{established: false},
		{established: false},
		{established: false},
		{established: false},
	})
	assert.Equal(t, []string{"1ms", "2ms", "4ms", "8ms", "8ms"}, delays)
}

// A lane that actually came up before dropping starts the next incident from
// the initial backoff. Without the reset, a long-lived process that has seen a
// handful of drops waits the full cap on every later drop — long enough for the
// relay to declare the VM offline while its sessions are still running.
func TestDataLaneRun_EstablishedConnectionResetsBackoff(t *testing.T) {
	delays := runBackoffScript(t, []connectOutcome{
		{established: false}, // dial failed  → 1ms
		{established: false}, // dial failed  → 2ms
		{established: false}, // dial failed  → 4ms
		{established: true},  // came up, then dropped → back to 1ms
		{established: false}, // dial failed  → 2ms
	})
	assert.Equal(t, []string{"1ms", "2ms", "4ms", "1ms", "2ms"}, delays)
}

func staticToken(tok string) tokenSource {
	return func(context.Context) (string, error) { return tok, nil }
}

// dialTokens runs a data lane against a proxy that refuses every dial, as the
// real one does once a token has expired, and returns the token each of the
// first n dials carried.
func dialTokens(t *testing.T, token tokenSource, n int) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, r.URL.Query().Get("token"))
		if len(got) == n {
			cancel() // unwinds Run
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	var connState int32
	dl := newDataLane("sb", "ws"+strings.TrimPrefix(srv.URL, "http"), token, nil, &connState, nil)
	dl.reconnectInitial = time.Millisecond
	dl.reconnectMax = time.Millisecond
	dl.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	return got
}

// The proxy checks the token on every dial, and the lane outlives any one
// access token (an hour by default). Dialing again with the token resolved at
// startup would be refused forever after the first drop past that hour.
func TestDataLaneDial_ResolvesTokenPerDial(t *testing.T) {
	var calls int
	token := func(context.Context) (string, error) {
		calls++
		return []string{"tok-1", "tok-2", "tok-3"}[calls-1], nil
	}

	assert.Equal(t, []string{"tok-1", "tok-2", "tok-3"}, dialTokens(t, token, 3))
}

// A failed refresh (auth unreachable, no NWEB_API_KEY to exchange) must not
// stop the lane from dialing: the previous token may still be valid.
func TestDataLaneDial_KeepsPreviousTokenWhenRefreshFails(t *testing.T) {
	var calls int
	token := func(context.Context) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("PAT exchange failed")
		}
		return []string{"tok-1", "", "tok-3"}[calls-1], nil
	}

	assert.Equal(t, []string{"tok-1", "tok-1", "tok-3"}, dialTokens(t, token, 3))
}
