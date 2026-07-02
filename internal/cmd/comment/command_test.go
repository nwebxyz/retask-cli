package comment

import (
	"testing"

	"github.com/nwebxyz/retask-cli/internal/flags"
	commentv1 "github.com/nwebxyz/retask-cli/proto-gen/comment/v1"
)

func TestTaskNrn(t *testing.T) {
	n := taskNrn("task_abc123")
	if n.GetDomain() != "nweb" || n.GetService() != "retask-task" ||
		n.GetResourceType() != "task" || n.GetResourceId() != "task_abc123" {
		t.Fatalf("unexpected nrn: %+v", n)
	}
}

func TestParseSort(t *testing.T) {
	cases := map[string]commentv1.CommentsRequest_Sort{
		"":             commentv1.CommentsRequest_SORT_DEFAULT,
		"default":      commentv1.CommentsRequest_SORT_DEFAULT,
		"created-asc":  commentv1.CommentsRequest_SORT_CREATED_AT_ASC,
		"created-desc": commentv1.CommentsRequest_SORT_CREATED_AT_DESC,
	}
	for in, want := range cases {
		got, err := parseSort(in)
		if err != nil {
			t.Fatalf("parseSort(%q) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("parseSort(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseSort("bogus"); err == nil {
		t.Fatalf("parseSort(\"bogus\") expected error, got nil")
	}
}

func TestCreateRequiresFlags(t *testing.T) {
	gf := &flags.Global{} // no workspace id

	// missing --task
	cmd := newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--body", "<p>hi</p>"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing --task")
	}

	// missing --body
	cmd = newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--task", "t1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing --body")
	}

	// task+body present but no workspace id
	cmd = newCreateCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--task", "t1", "--body", "<p>hi</p>"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing workspace id")
	}
}

func TestListRequiresFlags(t *testing.T) {
	gf := &flags.Global{}

	// missing --task
	cmd := newListCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing --task")
	}

	// task present but no workspace id
	cmd = newListCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"--task", "t1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing workspace id")
	}
}
