// internal/cmd/sandbox/autoupgrade.go
package sandbox

import (
	"context"
	"log/slog"
	"time"
)

// autoUpgradeCheckInterval is how often connect re-checks GitHub for a newer
// release once the initial "first step" upgrade (run before dialing the
// proxy) is done. Unlike the retention sweeper, this does not also check
// immediately at startup — that check already just happened.
const autoUpgradeCheckInterval = time.Hour

// autoUpgradeSweeper checks hourly for a newer release. Once one exists and
// no session is active, it applies the upgrade and, only once a replacement
// process is confirmed running, tears down this one — stop() is never called
// on a connection this sweeper cannot actually hand off, so a failed respawn
// leaves the current process connected rather than dropping the sandbox
// entirely. Fields are injectable so tests can drive once() without hitting
// the network, spawning a process, or corrupting the TUI's terminal output.
type autoUpgradeSweeper struct {
	currentVersion string
	latest         func() (version string, err error)
	upgrade        func() (upgraded bool, newVersion string, err error)
	respawn        func() error
	activeCount    func() int
	interval       time.Duration
	logger         *slog.Logger // may be nil
	stop           func()       // cancels connect's context, starting graceful shutdown

	// appliedPending is set once upgrade() has actually replaced the binary
	// on disk. Later ticks skip re-fetching and re-downloading (currentVersion
	// never changes without an actual process restart, so the check would
	// just find the same newer release again) and go straight to retrying
	// activeCount + respawn — cheap, and safe to retry hourly forever.
	appliedPending bool
	appliedVersion string
}

// Run checks every interval until ctx is cancelled. It does not check
// immediately: the caller already ran the same check as the first step of
// connect, right before this sweeper starts.
func (s *autoUpgradeSweeper) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.once()
		}
	}
}

func (s *autoUpgradeSweeper) once() {
	if !s.appliedPending {
		latest, err := s.latest()
		if err != nil {
			if s.logger != nil {
				s.logger.Error("auto_upgrade_check_failed", "error", err)
			}
			return
		}
		if latest == s.currentVersion {
			return
		}

		if n := s.activeCount(); n > 0 {
			if s.logger != nil {
				s.logger.Info("auto_upgrade_deferred", "latest", latest, "active_sessions", n)
			}
			return
		}

		upgraded, newVersion, err := s.upgrade()
		if err != nil {
			if s.logger != nil {
				s.logger.Error("auto_upgrade_failed", "error", err)
			}
			return
		}
		if !upgraded {
			return
		}
		s.appliedPending = true
		s.appliedVersion = newVersion
		if s.logger != nil {
			s.logger.Info("auto_upgrade_applied", "version", newVersion)
		}
	}

	// Re-check right before handing off: a session may have started during
	// the (possibly slow) download above, or since the last tick if an
	// earlier respawn attempt here failed.
	if n := s.activeCount(); n > 0 {
		if s.logger != nil {
			s.logger.Info("auto_upgrade_restart_deferred", "active_sessions", n)
		}
		return
	}

	if err := s.respawn(); err != nil {
		if s.logger != nil {
			s.logger.Error("auto_upgrade_restart_failed", "error", err, "version", s.appliedVersion)
		}
		return // stay connected on the old (already upgraded on disk) process; retry next tick
	}

	s.stop()
}
