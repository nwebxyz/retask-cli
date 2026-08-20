package auth

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

// Neither of these tests touches a TTY (go test's stdin isn't one), so
// prompt.IsInteractive() is false and both new interactive code paths in
// newLoginCommand must be skipped entirely — falling straight through to
// the same errors the resolver already produced before this change. If
// either test hangs, a new stdin read has escaped its TTY gate.

func TestLoginCommandNonInteractiveMissingPAT(t *testing.T) {
	t.Setenv("NWEB_API_TOKEN", "")
	t.Setenv("NWEB_API_KEY", "")
	t.Setenv("NWEB_WORKSPACE_ID", "")

	gf := &flags.Global{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")}
	cmd := NewCommand(gf)
	cmd.SetArgs([]string{"login"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "NWEB_API_KEY") {
		t.Errorf("error = %v, want it to mention NWEB_API_KEY", err)
	}
}

func TestLoginCommandNonInteractiveMissingWorkspace(t *testing.T) {
	t.Setenv("NWEB_API_TOKEN", "")
	t.Setenv("NWEB_API_KEY", "nweb_pat_test")
	t.Setenv("NWEB_WORKSPACE_ID", "")

	gf := &flags.Global{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")}
	cmd := NewCommand(gf)
	cmd.SetArgs([]string{"login"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "workspace ID") {
		t.Errorf("error = %v, want it to mention workspace ID", err)
	}
}
