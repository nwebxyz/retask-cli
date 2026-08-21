//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
)

// restartSelf spawns the just-upgraded binary as a new process with the same
// arguments and environment, then exits. Windows has no execve equivalent
// that replaces the current process image in place (see restart_unix.go for
// why that matters), so this is a plain spawn-and-exit instead.
func restartSelf() error {
	path, argv, env, err := restartArgv()
	if err != nil {
		return err
	}
	cmd := exec.Command(path, argv[1:]...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("auto-upgrade: restart: %w", err)
	}
	os.Exit(0)
	return nil // unreached
}
