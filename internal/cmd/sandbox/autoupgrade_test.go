package sandbox

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func TestParseAutoUpgrade(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"1h", time.Hour},
		{"30m", 30 * time.Minute},
		{"1d", 24 * time.Hour},
		{" 2h ", 2 * time.Hour},
	}
	for _, tc := range tests {
		got, enabled, err := parseAutoUpgrade(tc.in)
		require.NoError(t, err, "in=%q", tc.in)
		assert.True(t, enabled, "in=%q", tc.in)
		assert.Equal(t, tc.want, got, "in=%q", tc.in)
	}

	for _, in := range []string{"off", "OFF", " off "} {
		_, enabled, err := parseAutoUpgrade(in)
		require.NoError(t, err, "in=%q", in)
		assert.False(t, enabled, "in=%q should disable auto-upgrade", in)
	}
}

func TestParseAutoUpgradeRejectsUnknown(t *testing.T) {
	for _, in := range []string{"", "true", "false", "on", "1", "yes"} {
		_, _, err := parseAutoUpgrade(in)
		assert.Error(t, err, "in=%q should be rejected", in)
	}
}

func TestParseAutoUpgradeRejectsZero(t *testing.T) {
	// 0 already means "act immediately" for --older-than; allowing it here
	// too would make an interval mean "check continuously". Disabling is
	// spelled "off".
	_, _, err := parseAutoUpgrade("0")
	assert.Error(t, err)
	_, _, err = parseAutoUpgrade("0h")
	assert.Error(t, err)
}

func TestConnectAutoUpgradeFlagDefault(t *testing.T) {
	cmd := newConnectCommand(&flags.Global{})
	f := cmd.Flags().Lookup("auto-upgrade")
	require.NotNil(t, f, "--auto-upgrade must be registered")
	assert.Equal(t, "1h", f.DefValue, "auto-upgrade defaults to an hourly check")
}

// --- autoUpgradeChecker.once() ---

func TestAutoUpgradeOnceAppliesWhenNoActiveSessions(t *testing.T) {
	upgradeCalled := false
	onUpgradedCalled := false
	c := &autoUpgradeChecker{
		hasActiveSessions: func() bool { return false },
		checkLatest:       func() (string, bool, error) { return "9.9.9", false, nil },
		upgrade: func() (bool, error) {
			upgradeCalled = true
			return true, nil
		},
		onUpgraded: func() { onUpgradedCalled = true },
	}
	c.once()
	assert.True(t, upgradeCalled, "a newer release with no active sessions must be applied")
	assert.True(t, onUpgradedCalled, "onUpgraded must fire once the binary is patched")
}

func TestAutoUpgradeOnceDefersWhenSessionsActive(t *testing.T) {
	upgradeCalled := false
	c := &autoUpgradeChecker{
		hasActiveSessions: func() bool { return true },
		checkLatest:       func() (string, bool, error) { return "9.9.9", false, nil },
		upgrade: func() (bool, error) {
			upgradeCalled = true
			return true, nil
		},
		onUpgraded: func() { t.Fatal("onUpgraded must not fire while a session is active") },
	}
	c.once()
	assert.False(t, upgradeCalled, "must not download/apply while a session is active")
}

func TestAutoUpgradeOnceNoopsWhenAlreadyUpToDate(t *testing.T) {
	c := &autoUpgradeChecker{
		hasActiveSessions: func() bool { t.Fatal("must not check sessions when already up to date"); return false },
		checkLatest:       func() (string, bool, error) { return "1.0.0", true, nil },
		upgrade:           func() (bool, error) { t.Fatal("must not upgrade when already up to date"); return false, nil },
		onUpgraded:        func() { t.Fatal("must not restart when already up to date") },
	}
	c.once()
}

func TestAutoUpgradeOnceSurvivesCheckError(t *testing.T) {
	c := &autoUpgradeChecker{
		hasActiveSessions: func() bool { t.Fatal("must not check sessions when the version check itself failed"); return false },
		checkLatest:       func() (string, bool, error) { return "", false, fmt.Errorf("network down") },
		upgrade:           func() (bool, error) { t.Fatal("must not upgrade when the version check failed"); return false, nil },
	}
	assert.NotPanics(t, c.once)
}

func TestAutoUpgradeOnceSurvivesUpgradeError(t *testing.T) {
	c := &autoUpgradeChecker{
		hasActiveSessions: func() bool { return false },
		checkLatest:       func() (string, bool, error) { return "9.9.9", false, nil },
		upgrade:           func() (bool, error) { return false, fmt.Errorf("download failed") },
		onUpgraded:        func() { t.Fatal("must not restart when the upgrade itself failed") },
	}
	assert.NotPanics(t, c.once)
}

// --- SessionManager.HasActiveSessions ---

func TestHasActiveSessionsReportsAnyLiveSession(t *testing.T) {
	sm := newTestSessionManager(t, t.TempDir())
	assert.False(t, sm.HasActiveSessions(), "no sessions yet")

	sm.sessions["sess-a"] = &sessionEntry{}
	assert.True(t, sm.HasActiveSessions(), "one live session is enough to count as active")
}
