// internal/cmd/sandbox/autoupgrade.go
package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nwebxyz/retask-cli/internal/cmd/upgrade"
)

// parseAutoUpgrade parses the --auto-upgrade flag, which additionally accepts
// "off" on top of the plain duration syntax parseDuration already handles —
// the same shape as --retention. The duration doubles as the recurring check
// interval once connect is running; the initial check is a separate explicit
// first step run before any interval has elapsed.
//
// Zero is rejected: elsewhere in this command a bare "0" means "act
// immediately" (--older-than 0 wipes everything right now), which would be a
// confusing reading of an interval that instead ends up meaning "check
// continuously". Disabling is spelled "off".
func parseAutoUpgrade(s string) (interval time.Duration, enabled bool, err error) {
	if strings.EqualFold(strings.TrimSpace(s), "off") {
		return 0, false, nil
	}
	d, err := parseDuration(s)
	if err != nil {
		return 0, false, err
	}
	if d == 0 {
		return 0, false, fmt.Errorf(`invalid --auto-upgrade %q: use "off" to disable auto-upgrade`, s)
	}
	return d, true, nil
}

// autoUpgradeChecker periodically checks for a newer retask release while
// connect is running. A newer release is only applied when no session is
// active locally — upgrading mid-session would replace the binary out from
// under a running agent's supervising process, so an update found while
// sessions are active is deferred to the next check instead.
//
// checkLatest and upgrade are fields, not direct calls into the upgrade
// package, so tests can fake both the network call and the binary patch.
type autoUpgradeChecker struct {
	interval          time.Duration
	hasActiveSessions func() bool
	logger            *slog.Logger // may be nil
	checkLatest       func() (latest string, upToDate bool, err error)
	upgrade           func() (didUpgrade bool, err error)
	// onUpgraded is called once a new binary has been applied to disk. It
	// signals the caller to wind down and restart into the new version.
	onUpgraded func()
}

func newAutoUpgradeChecker(interval time.Duration, hasActiveSessions func() bool, logger *slog.Logger, onUpgraded func()) *autoUpgradeChecker {
	return &autoUpgradeChecker{
		interval:          interval,
		hasActiveSessions: hasActiveSessions,
		logger:            logger,
		checkLatest:       upgrade.CheckLatestVersion,
		upgrade:           upgrade.Run,
		onUpgraded:        onUpgraded,
	}
}

// Run checks every interval until ctx is cancelled. Unlike the retention
// sweeper, it does not also check immediately at startup: connect's caller
// already runs the upgrade as an explicit first step before this loop starts.
func (c *autoUpgradeChecker) Run(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.once()
		}
	}
}

func (c *autoUpgradeChecker) once() {
	latest, upToDate, err := c.checkLatest()
	if err != nil {
		c.logWarn("auto_upgrade_check_failed", "error", err)
		return
	}
	if upToDate {
		return
	}
	if c.hasActiveSessions() {
		c.logInfo("auto_upgrade_deferred", "latest", latest, "reason", "active sessions")
		return
	}
	didUpgrade, err := c.upgrade()
	if err != nil {
		c.logWarn("auto_upgrade_failed", "latest", latest, "error", err)
		return
	}
	if !didUpgrade {
		return
	}
	c.logInfo("auto_upgrade_applied", "version", latest)
	if c.onUpgraded != nil {
		c.onUpgraded()
	}
}

func (c *autoUpgradeChecker) logInfo(msg string, args ...any) {
	if c.logger != nil {
		c.logger.Info(msg, args...)
	}
}

func (c *autoUpgradeChecker) logWarn(msg string, args ...any) {
	if c.logger != nil {
		c.logger.Warn(msg, args...)
	}
}

// restartSelf re-executes the current binary with the same arguments and
// environment it was originally started with — including any flags a caller
// appended later, like --profile — so a hot-applied upgrade takes effect. It
// hands stdio to the child and exits the current process once the child has
// started. os/exec rather than syscall.Exec so this works unmodified on
// Windows too.
func restartSelf() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("auto-upgrade: locate running binary: %w", err)
	}
	cmd := exec.Command(execPath, os.Args[1:]...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("auto-upgrade: restart: %w", err)
	}
	os.Exit(0)
	return nil // unreached
}
