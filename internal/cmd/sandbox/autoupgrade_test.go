package sandbox

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// newTestAutoUpgradeSweeper returns a sweeper with counting stop/respawn
// stubs; callers override the function fields they care about.
func newTestAutoUpgradeSweeper() (s *autoUpgradeSweeper, stopCalls, respawnCalls *int) {
	stops, respawns := 0, 0
	s = &autoUpgradeSweeper{
		currentVersion: "1.0.0",
		activeCount:    func() int { return 0 },
		respawn:        func() error { respawns++; return nil },
		stop:           func() { stops++ },
	}
	return s, &stops, &respawns
}

func TestAutoUpgradeSweeperOnce_SameVersion_NoOp(t *testing.T) {
	s, stopCalls, respawnCalls := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "1.0.0", nil }
	s.upgrade = func() (bool, string, error) {
		t.Fatal("upgrade must not run when already on the latest version")
		return false, "", nil
	}

	s.once()

	assert.Equal(t, 0, *respawnCalls)
	assert.Equal(t, 0, *stopCalls)
}

func TestAutoUpgradeSweeperOnce_LatestCheckFails_NoOp(t *testing.T) {
	s, stopCalls, respawnCalls := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "", fmt.Errorf("network down") }
	s.activeCount = func() int {
		t.Fatal("must not check active sessions when the version check itself failed")
		return 0
	}
	s.upgrade = func() (bool, string, error) {
		t.Fatal("upgrade must not run when the version check failed")
		return false, "", nil
	}

	s.once()

	assert.Equal(t, 0, *respawnCalls)
	assert.Equal(t, 0, *stopCalls)
}

func TestAutoUpgradeSweeperOnce_NewerVersionWithActiveSessions_Defers(t *testing.T) {
	s, stopCalls, respawnCalls := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "1.1.0", nil }
	s.activeCount = func() int { return 2 }
	s.upgrade = func() (bool, string, error) {
		t.Fatal("upgrade must not run while a session is active")
		return false, "", nil
	}

	s.once()

	assert.Equal(t, 0, *respawnCalls, "must not respawn while sessions are active")
	assert.Equal(t, 0, *stopCalls, "must not tear down the connection while sessions are active")
}

func TestAutoUpgradeSweeperOnce_NewerVersionNoActiveSessions_UpgradesAndRestarts(t *testing.T) {
	s, stopCalls, respawnCalls := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "1.1.0", nil }
	s.activeCount = func() int { return 0 }
	s.upgrade = func() (bool, string, error) { return true, "1.1.0", nil }

	s.once()

	assert.Equal(t, 1, *respawnCalls, "must respawn once the binary is upgraded")
	assert.Equal(t, 1, *stopCalls, "must only tear down the old process after a successful respawn")
}

func TestAutoUpgradeSweeperOnce_UpgradeFails_NoRestart(t *testing.T) {
	s, stopCalls, respawnCalls := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "1.1.0", nil }
	s.activeCount = func() int { return 0 }
	s.upgrade = func() (bool, string, error) { return false, "", fmt.Errorf("checksum mismatch") }

	s.once()

	assert.Equal(t, 0, *respawnCalls, "a failed upgrade must not attempt a respawn")
	assert.Equal(t, 0, *stopCalls, "a failed upgrade must not tear down a working connection")
}

func TestAutoUpgradeSweeperOnce_UpgradeAppliesNothing_NoRestart(t *testing.T) {
	// Defensive case: the version check said newer-available, but Run
	// itself decided (e.g. a race where the release changed again) that
	// nothing needed applying. Must not restart on a no-op.
	s, stopCalls, respawnCalls := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "1.1.0", nil }
	s.activeCount = func() int { return 0 }
	s.upgrade = func() (bool, string, error) { return false, "", nil }

	s.once()

	assert.Equal(t, 0, *respawnCalls)
	assert.Equal(t, 0, *stopCalls)
}

func TestAutoUpgradeSweeperOnce_SessionStartsDuringDownload_DefersRestartWithoutReDownloading(t *testing.T) {
	// TOCTOU guard: activeCount() said 0 when the check started, but by the
	// time the (slow) upgrade finished, a session had started. once() must
	// re-check before handing off, and must not have to re-download to
	// notice on the next tick.
	s, stopCalls, respawnCalls := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "1.1.0", nil }
	checks := 0
	s.activeCount = func() int {
		checks++
		if checks == 1 {
			return 0 // safe to start the download
		}
		return 1 // a session started while downloading
	}
	upgradeCalls := 0
	s.upgrade = func() (bool, string, error) { upgradeCalls++; return true, "1.1.0", nil }

	s.once()

	assert.Equal(t, 1, upgradeCalls, "upgrade must have been applied")
	assert.Equal(t, 0, *respawnCalls, "must not hand off while a session is now active")
	assert.Equal(t, 0, *stopCalls)

	// Next tick: session finished, upgrade must not run again (already applied).
	s.activeCount = func() int { return 0 }
	s.once()

	assert.Equal(t, 1, upgradeCalls, "must not re-download an upgrade already applied on disk")
	assert.Equal(t, 1, *respawnCalls)
	assert.Equal(t, 1, *stopCalls)
}

func TestAutoUpgradeSweeperOnce_RespawnFails_StaysConnectedAndRetriesWithoutReDownloading(t *testing.T) {
	s, stopCalls, _ := newTestAutoUpgradeSweeper()
	s.latest = func() (string, error) { return "1.1.0", nil }
	s.activeCount = func() int { return 0 }
	upgradeCalls := 0
	s.upgrade = func() (bool, string, error) { upgradeCalls++; return true, "1.1.0", nil }
	s.respawn = func() error { return fmt.Errorf("fork/exec: resource temporarily unavailable") }

	s.once()

	assert.Equal(t, 1, upgradeCalls)
	assert.Equal(t, 0, *stopCalls, "must never tear down the only connection on a failed respawn")

	// Retry tick: respawn succeeds this time, without re-downloading.
	respawned := false
	s.respawn = func() error { respawned = true; return nil }
	s.once()

	assert.Equal(t, 1, upgradeCalls, "retry must not re-download an upgrade already applied on disk")
	assert.True(t, respawned)
	assert.Equal(t, 1, *stopCalls, "must tear down only after respawn actually succeeds")
}

func TestAutoUpgradeSweeperRun_StopsOnContextCancel(t *testing.T) {
	s, _, _ := newTestAutoUpgradeSweeper()
	// A long interval proves the goroutine exits on cancellation, not on a tick.
	s.interval = time.Hour
	s.latest = func() (string, error) {
		t.Fatal("Run must not check immediately; only the caller's own first-step check does that")
		return "", nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper must stop when ctx is cancelled")
	}
}
