package skillcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func TestSkillCommandPrintsMarkdown(t *testing.T) {
	cmd := NewCommand(&flags.Global{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := buf.String()
	if !strings.HasPrefix(out, "# retask CLI") {
		t.Errorf("skill output should start with %q, got: %.40q", "# retask CLI", out)
	}
}
