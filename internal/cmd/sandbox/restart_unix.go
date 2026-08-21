//go:build !windows

package sandbox

import (
	"fmt"
	"syscall"
)

// restartSelf replaces the current process image with the just-upgraded
// binary via execve, keeping the same pid, process group, and session.
//
// A spawn-then-exit restart (starting a child and calling os.Exit in the
// parent) breaks terminal job control: many shells track the foreground job
// by the pid they forked, so once that original pid exits, the shell
// reclaims the controlling terminal for itself — even though our new child,
// inheriting the old process group, is still running. The child is then a
// background process relative to the terminal, and entering raw mode for the
// TUI fails with EIO ("input/output error"). execve sidesteps this entirely:
// the pid never exits, so the shell never reassigns the terminal.
func restartSelf() error {
	path, argv, env, err := restartArgv()
	if err != nil {
		return err
	}
	if err := syscall.Exec(path, argv, env); err != nil { //nolint:gosec
		return fmt.Errorf("auto-upgrade: restart: %w", err)
	}
	return nil // unreached: a successful Exec never returns
}
