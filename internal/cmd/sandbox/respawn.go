// internal/cmd/sandbox/respawn.go
package sandbox

import (
	"os"
	"os/exec"
)

// executablePath resolves the path to the currently running binary. It is a
// package variable so tests can point respawn at a harmless binary instead
// of re-launching the real test binary.
var executablePath = os.Executable

// respawn starts a new retask process with the same executable, arguments,
// and environment as the current one, inheriting stdio so a TUI or headless
// session continues uninterrupted in the same terminal. It does not wait for
// the child: the caller is expected to be mid-shutdown and to exit shortly
// after, handing the terminal over.
func respawn() error {
	execPath, err := executablePath()
	if err != nil {
		return err
	}
	cmd := exec.Command(execPath, os.Args[1:]...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Start()
}
