package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"0d", 0},
		{"12h", 12 * time.Hour},
		{"90m", 90 * time.Minute},
		{"0", 0},
		{" 7d ", 7 * 24 * time.Hour},
	}
	for _, tc := range tests {
		got, err := parseDuration(tc.in)
		require.NoError(t, err, "in=%q", tc.in)
		assert.Equal(t, tc.want, got, "in=%q", tc.in)
	}
}

func TestParseDurationRejects(t *testing.T) {
	for _, in := range []string{"", "off", "30days", "-1d", "-5h", "abc", "d"} {
		_, err := parseDuration(in)
		assert.Error(t, err, "in=%q should be rejected", in)
	}
}

func TestParseRetention(t *testing.T) {
	d, enabled, err := parseRetention("30d")
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, 30*24*time.Hour, d)

	for _, in := range []string{"off", "OFF", " off "} {
		_, enabled, err := parseRetention(in)
		require.NoError(t, err, "in=%q", in)
		assert.False(t, enabled, "in=%q should disable retention", in)
	}
}

func TestParseRetentionRejectsZero(t *testing.T) {
	// 0 means "delete everything" for --older-than; allowing it here would turn
	// an hourly sweep into an hourly wipe. Disabling is spelled "off".
	_, _, err := parseRetention("0")
	assert.Error(t, err)
	_, _, err = parseRetention("0d")
	assert.Error(t, err)
}

func TestSweeperOnceDeletesAgedFolders(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	old := "session-old"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, old), 0o755))
	require.NoError(t, l.record("old", "old", old, time.Now().Add(-40*24*time.Hour)))

	s := &retentionSweeper{log: l, baseDir: dir, window: 30 * 24 * time.Hour}
	s.once()

	_, err := os.Stat(filepath.Join(dir, old))
	assert.True(t, os.IsNotExist(err))
}

func TestSweeperRunSweepsAtStartupThenStops(t *testing.T) {
	dir := t.TempDir()
	l := newSessionLog(dir, "sb-1")
	old := "session-old"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, old), 0o755))
	require.NoError(t, l.record("old", "old", old, time.Now().Add(-40*24*time.Hour)))

	// A long interval proves the startup sweep happened, not a tick.
	s := &retentionSweeper{log: l, baseDir: dir, window: 30 * 24 * time.Hour, interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, old))
		return os.IsNotExist(err)
	}, 2*time.Second, 10*time.Millisecond, "sweeper must sweep once at startup")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper must stop when ctx is cancelled")
	}
}

func TestConnectRetentionFlagDefault(t *testing.T) {
	cmd := newConnectCommand(&flags.Global{})
	f := cmd.Flags().Lookup("retention")
	require.NotNil(t, f, "--retention must be registered")
	assert.Equal(t, "30d", f.DefValue, "retention defaults to 30 days")
}
