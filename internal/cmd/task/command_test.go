package task

import (
	"strings"
	"testing"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func TestCreateRequiresFlags(t *testing.T) {
	gf := &flags.Global{} // no workspace id

	// missing --project-id
	cmd := newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--title", "t"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing --project-id")
	}

	// missing --title
	cmd = newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--project-id", "p1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing --title")
	}

	// project+title present but no workspace id
	cmd = newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--project-id", "p1", "--title", "t"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing workspace id")
	}
}

func TestCreateAcceptsStatusAndTaskTypeFlags(t *testing.T) {
	gf := &flags.Global{} // no workspace id, so RunE fails before dialing out

	cmd := newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{
		"--project-id", "p1",
		"--title", "t",
		"--status", "status_todo",
		"--task-type", "type_bug",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for missing workspace id")
	}
	if !strings.Contains(err.Error(), "workspace-id") {
		t.Fatalf("expected --status/--task-type to be recognized flags and fail on missing workspace id, got: %v", err)
	}
}

func TestCreateInvalidPriority(t *testing.T) {
	gf := &flags.Global{WorkspaceID: "ws1"}

	cmd := newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--project-id", "p1", "--title", "t", "--priority", "bogus"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for invalid --priority")
	}
}
