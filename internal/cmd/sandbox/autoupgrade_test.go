package sandbox

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func TestParseAutoUpgrade(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"on", true}, {"ON", true}, {" on ", true},
		{"off", false}, {"OFF", false}, {" off ", false},
	}
	for _, tc := range tests {
		got, err := parseAutoUpgrade(tc.in)
		require.NoError(t, err, "in=%q", tc.in)
		assert.Equal(t, tc.want, got, "in=%q", tc.in)
	}
}

func TestParseAutoUpgradeRejectsUnknown(t *testing.T) {
	for _, in := range []string{"", "true", "false", "1", "yes"} {
		_, err := parseAutoUpgrade(in)
		assert.Error(t, err, "in=%q should be rejected", in)
	}
}

func TestConnectAutoUpgradeFlagDefault(t *testing.T) {
	cmd := newConnectCommand(&flags.Global{})
	f := cmd.Flags().Lookup("auto-upgrade")
	require.NotNil(t, f, "--auto-upgrade must be registered")
	assert.Equal(t, "on", f.DefValue, "auto-upgrade defaults to on")
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
