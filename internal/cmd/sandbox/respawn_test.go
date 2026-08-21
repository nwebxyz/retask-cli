package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRespawnStartsTheResolvedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/true on windows")
	}

	orig := executablePath
	origArgs := os.Args
	t.Cleanup(func() {
		executablePath = orig
		os.Args = origArgs
	})

	executablePath = func() (string, error) { return "/usr/bin/true", nil }
	os.Args = []string{"retask", "sandbox", "connect", "sb-1"}

	err := respawn()

	require.NoError(t, err, "must start the resolved executable without waiting for it to exit")
}

func TestRespawnPropagatesExecutablePathError(t *testing.T) {
	orig := executablePath
	t.Cleanup(func() { executablePath = orig })

	executablePath = func() (string, error) { return "", fmt.Errorf("executable path unknown") }

	err := respawn()

	assert.ErrorContains(t, err, "executable path unknown")
}
