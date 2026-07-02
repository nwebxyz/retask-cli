# `retask comment` Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a top-level `retask comment` command wrapping `comment.v1.CommentService` (list/get/create/update/delete + attachment add/remove), scoped to retask tasks.

**Architecture:** New `internal/cmd/comment` package mirroring the existing `internal/cmd/task` package (same `connect()` pattern, cobra subcommand layout, and `attachment add/remove <id> <file-id>` shape). `--task <id>` is mapped to the target NRN `nweb:retask-task:task:<id>`. Comment `body` is raw HTML, stored verbatim. Wired into `cmd/retask/main.go`, documented in the `help-llm` manifest and the skill file.

**Tech Stack:** Go, cobra, connectrpc, protobuf-generated `proto-gen/comment/v1` (already present in the repo).

**Spec:** `docs/superpowers/specs/2026-07-02-retask-comment-command-design.md`

---

## File Structure

- **Create** `internal/cmd/comment/command.go` — package `comment`; `NewCommand`, `connect`, `domain` const, `taskNrn`/`parseNrn`/`parseSort` helpers, all subcommands. One focused file, same size/shape as `internal/cmd/task/command.go`.
- **Create** `internal/cmd/comment/command_test.go` — unit tests for `taskNrn`, `parseSort`, and flag-validation ordering (no network).
- **Modify** `cmd/retask/main.go` — import `commentcmd` and `root.AddCommand(commentcmd.NewCommand(gf))`.
- **Modify** `internal/cmd/helpcmd/command.go` — add manifest entries (keeps `main_test.go` drift check green).
- **Modify** `.bin/sync_proto.sh`, `.bin/build_proto.sh` — add `comment/v1` to the APPROVED lists (reproducible regen; proto is already generated).
- **Modify** `CLAUDE.md` — add `comment` row to the approved proto services table.
- **Modify** `skills/retask-cli.md` — add a Comments section documenting the HTML body + mention format.

## Key facts pinned during design (do not re-derive)

- Domain segment of the target NRN is the constant `"nweb"`.
- `SetComment` **edit** path (when `comment_id != ""`) uses **only `body`**; `target_nrn`/`parent_comment_id`/attachments come from the stored record. So `update` sends `{comment_id, body}` — no round-trip, no workspace/target.
- `SetComment` **create** path (empty `comment_id`) requires `workspace_id` and `target_nrn`.
- `list` (`GetComments`) requires `workspace_id` + `target_nrn` as filter scope. `parent_comment_id` has no proto presence: empty ⇒ top-level only; set ⇒ replies under that id.
- Generated symbols: `commentv1connect.NewCommentServiceClient(httpClient, baseURL, opts...)`; requests `commentv1.CommentsRequest{Filter,Sort}`, `commentv1.CommentsRequest_Filter{WorkspaceId,TargetNrn,ParentCommentId,CreatedByNrns}`, sort enum `commentv1.CommentsRequest_SORT_DEFAULT|SORT_CREATED_AT_ASC|SORT_CREATED_AT_DESC`; `commentv1.Comment{CommentId,WorkspaceId,TargetNrn,Body,ParentCommentId}`; `commentv1.AddCommentAttachmentRequest{CommentId,FileId}`; `commentv1.DeleteCommentAttachmentRequest{CommentId,FileId}`; `SetComment` returns `*commonv1.Id`.
- The `main_test.go` drift test requires: every runnable leaf command has a manifest entry, and the manifest's flag list (minus `--`/globals) exactly matches the command's declared non-global flags. Flag order does not matter (both sides are sorted).

---

## Task 1: Approve `comment/v1` in proto scripts and docs

**Files:**
- Modify: `.bin/sync_proto.sh:11-24`
- Modify: `.bin/build_proto.sh:4-18`
- Modify: `CLAUDE.md` (approved proto services table)

- [ ] **Step 1: Add `comment/v1` to `.bin/sync_proto.sh`**

In the `APPROVED=(` array, add the line (alphabetical, before `common/v1`):

```bash
APPROVED=(
  "auth/v1"
  "comment/v1"
  "common/v1"
  "customer/v1"
  "file/v1"
  "integration/v1"
  "project/v1"
  "retask/common/v1"
  "retask/agent/v1"
  "retask/project/v1"
  "retask/sandbox/v1"
  "retask/task/v1"
  "workspace/v1"
)
```

- [ ] **Step 2: Add `comment/v1` to `.bin/build_proto.sh`**

In the `APPROVED_SERVICES=(` array, add the line (alphabetical, before `common/v1`):

```bash
APPROVED_SERVICES=(
  "auth/v1"
  "comment/v1"
  "common/v1"
  "customer/v1"
  "file/v1"
  "integration/v1"
  "project/v1"
  "quota/v1"
  "retask/agent/v1"
  "retask/common/v1"
  "retask/project/v1"
  "retask/sandbox/v1"
  "retask/task/v1"
  "workspace/v1"
)
```

- [ ] **Step 3: Add the `comment` row to the `CLAUDE.md` approved proto services table**

In the "Approved proto services" table, add this row between the `auth` and `common` rows:

```markdown
| comment | `proto-gen/comment/v1` |
```

- [ ] **Step 4: Verify the repo still builds (generated code already present)**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add .bin/sync_proto.sh .bin/build_proto.sh CLAUDE.md
git commit -m "chore(comment): approve comment/v1 proto service"
```

---

## Task 2: Create the `comment` command package (helpers + subcommands)

**Files:**
- Create: `internal/cmd/comment/command.go`
- Test: `internal/cmd/comment/command_test.go`

This package is not yet wired into `main.go`, so the `main_test.go` drift check is unaffected until Task 3. `go build ./...` still passes (an exported, unused package compiles fine).

- [ ] **Step 1: Write the failing tests**

Create `internal/cmd/comment/command_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cmd/comment/`
Expected: FAIL — build error, `undefined: taskNrn`, `newCreateCommand`, etc. (package/command.go does not exist yet).

- [ ] **Step 3: Write `internal/cmd/comment/command.go`**

Create `internal/cmd/comment/command.go`:

```go
// internal/cmd/comment/command.go
package comment

import (
	"context"
	"fmt"
	"strings"

	connectrpc "connectrpc.com/connect"
	"github.com/spf13/cobra"

	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	commentv1 "github.com/nwebxyz/retask-cli/proto-gen/comment/v1"
	commentv1connect "github.com/nwebxyz/retask-cli/proto-gen/comment/v1/commentv1connect"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
)

// domain is the NRN domain segment for this deployment.
const domain = "nweb"

// NewCommand returns the top-level "comment" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Manage task comments",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newCreateCommand(gf),
		newUpdateCommand(gf),
		newDeleteCommand(gf),
		newAttachmentCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a CommentServiceClient plus a close
// function that must be deferred by the caller.
func connect(gf *flags.Global) (commentv1connect.CommentServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return commentv1connect.NewCommentServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}

// taskNrn builds the target NRN for a task comment: nweb:retask-task:task:<id>.
func taskNrn(taskID string) *commonv1.Nrn {
	return &commonv1.Nrn{
		Domain:       domain,
		Service:      "retask-task",
		ResourceType: "task",
		ResourceId:   taskID,
	}
}

// parseNrn parses "domain:service:resource_type:resource_id" into a commonv1.Nrn.
func parseNrn(s string) (*commonv1.Nrn, error) {
	parts := strings.SplitN(s, ":", 4)
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid NRN %q: expected domain:service:resource_type:resource_id", s)
	}
	return &commonv1.Nrn{
		Domain:       parts[0],
		Service:      parts[1],
		ResourceType: parts[2],
		ResourceId:   parts[3],
	}, nil
}

// parseSort maps a friendly --sort value to the CommentsRequest sort enum.
func parseSort(s string) (commentv1.CommentsRequest_Sort, error) {
	switch s {
	case "", "default":
		return commentv1.CommentsRequest_SORT_DEFAULT, nil
	case "created-asc":
		return commentv1.CommentsRequest_SORT_CREATED_AT_ASC, nil
	case "created-desc":
		return commentv1.CommentsRequest_SORT_CREATED_AT_DESC, nil
	default:
		return 0, fmt.Errorf("invalid --sort %q. Valid values: default, created-asc, created-desc", s)
	}
}

// ── comment list ──────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	var taskID, parentCommentID, sortStr string
	var createdBy []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List comments on a task",
		Long: `List comments on a task. Returns top-level comments by default; pass
--parent-comment-id to list the replies under a top-level comment.

The workspace ID is required and can be provided via the global --workspace-id
flag or NWEB_WORKSPACE_ID env var.

Usage examples:
  retask comment list --task task_abc123
  retask comment list --task task_abc123 --parent-comment-id cmt_top123
  retask comment list --task task_abc123 --sort created-asc

Flags:
  --task string                Required. Task ID whose comments to list
  --parent-comment-id string   List replies under this comment ID (default: top-level only)
  --sort string                Sort order: default, created-asc, created-desc
  --created-by string          Filter by author member NRN (repeatable, format: nweb:workspace:member:<uuid>)

Output fields: comment_id, workspace_id, target_nrn, parent_comment_id, body, mentioned_member_nrns, attachments, is_edited, created_by_nrn, created_at, updated_at, user_access`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" {
				return fmt.Errorf("--task is required")
			}
			if gf.WorkspaceID == "" {
				return fmt.Errorf("--workspace-id is required (or set NWEB_WORKSPACE_ID)")
			}
			sortVal, err := parseSort(sortStr)
			if err != nil {
				return err
			}

			filter := &commentv1.CommentsRequest_Filter{
				WorkspaceId:     gf.WorkspaceID,
				TargetNrn:       taskNrn(taskID),
				ParentCommentId: parentCommentID, // "" = top-level only
			}
			for _, c := range createdBy {
				nrn, err := parseNrn(c)
				if err != nil {
					return fmt.Errorf("invalid --created-by: %w", err)
				}
				filter.CreatedByNrns = append(filter.CreatedByNrns, nrn)
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetComments(context.Background(), connectrpc.NewRequest(&commentv1.CommentsRequest{
				Filter: filter,
				Sort:   sortVal,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Comments)
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "Task ID whose comments to list (required)")
	cmd.Flags().StringVar(&parentCommentID, "parent-comment-id", "", "List replies under this comment ID (default: top-level only)")
	cmd.Flags().StringVar(&sortStr, "sort", "", "Sort order: default, created-asc, created-desc")
	cmd.Flags().StringArrayVar(&createdBy, "created-by", nil, "Filter by author member NRN (repeatable, format: nweb:workspace:member:<uuid>)")
	return cmd
}

// ── comment get ───────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <comment-id>",
		Short: "Get a comment by ID",
		Long: `Fetch a single comment by its ID.

Usage example:
  retask comment get cmt_abc123

Output fields: comment_id, workspace_id, target_nrn, parent_comment_id, body, mentioned_member_nrns, attachments, is_edited, created_by_nrn, created_at, updated_at, user_access`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetComment(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

// ── comment create ──────────────────────────────────────────────────────────────

func newCreateCommand(gf *flags.Global) *cobra.Command {
	var taskID, body, parentCommentID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a comment on a task",
		Long: `Create a comment on a task.

--body is stored verbatim as HTML (TipTap getHTML() format), the same format as
task descriptions. To mention a workspace member, embed a mention span in the
body; the server parses mentions from the body on every write:

  <span data-type="mention" data-id="nweb:workspace:member:<uuid>">@Name</span>

Threading is one level deep: pass --parent-comment-id to reply to a TOP-LEVEL
comment (a reply cannot itself be replied to).

The workspace ID is required and can be provided via the global --workspace-id
flag or NWEB_WORKSPACE_ID env var.

Usage examples:
  retask comment create --task task_abc123 --body '<p>Looks good to me</p>'
  retask comment create --task task_abc123 --parent-comment-id cmt_top123 --body '<p>+1</p>'
  retask comment create --task task_abc123 --body '<p>cc <span data-type="mention" data-id="nweb:workspace:member:uuid">@Sam</span></p>'

Flags:
  --task string                Required. Task ID to comment on
  --body string                Required. Comment body as HTML
  --parent-comment-id string   Reply to this top-level comment ID

Output fields: comment_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" {
				return fmt.Errorf("--task is required")
			}
			if body == "" {
				return fmt.Errorf("--body is required")
			}
			if gf.WorkspaceID == "" {
				return fmt.Errorf("--workspace-id is required (or set NWEB_WORKSPACE_ID)")
			}

			comment := &commentv1.Comment{
				WorkspaceId:     gf.WorkspaceID,
				TargetNrn:       taskNrn(taskID),
				Body:            body,
				ParentCommentId: parentCommentID,
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetComment(context.Background(), connectrpc.NewRequest(comment))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"comment_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "Task ID to comment on (required)")
	cmd.Flags().StringVar(&body, "body", "", "Comment body as HTML (required)")
	cmd.Flags().StringVar(&parentCommentID, "parent-comment-id", "", "Reply to this top-level comment ID")
	return cmd
}

// ── comment update ──────────────────────────────────────────────────────────────

func newUpdateCommand(gf *flags.Global) *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "update <comment-id>",
		Short: "Edit a comment's body",
		Long: `Edit the body of an existing comment. Only the comment's author may edit it,
and editing sets the comment's "is_edited" indicator. --body is HTML, the same
format as create (see 'retask comment create --help' for the mention syntax).

Usage example:
  retask comment update cmt_abc123 --body '<p>Updated text</p>'

Flags:
  --body string   Required. New comment body as HTML

Output fields: comment_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if body == "" {
				return fmt.Errorf("--body is required")
			}
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetComment(context.Background(), connectrpc.NewRequest(&commentv1.Comment{
				CommentId: args[0],
				Body:      body,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"comment_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "New comment body as HTML (required)")
	return cmd
}

// ── comment delete ──────────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <comment-id>",
		Short: "Delete a comment",
		Long: `Soft-delete a comment by ID. Deleting a top-level comment cascade-deletes its
replies.

Usage example:
  retask comment delete cmt_abc123

Output fields: status, comment_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteComment(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "comment_id": args[0]})
		},
	}
}

// ── comment attachment ────────────────────────────────────────────────────────

func newAttachmentCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "Manage comment attachments",
	}
	cmd.AddCommand(
		newAttachmentAddCommand(gf),
		newAttachmentRemoveCommand(gf),
	)
	return cmd
}

func newAttachmentAddCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "add <comment-id> <file-id>",
		Short: "Add a file attachment to a comment",
		Long: `Attach a file (by file ID) to a comment. File IDs come from 'retask file'.

Usage example:
  retask comment attachment add cmt_abc123 file_xyz456

Output fields: comment_id, attachments`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.AddCommentAttachment(context.Background(), connectrpc.NewRequest(&commentv1.AddCommentAttachmentRequest{
				CommentId: args[0],
				FileId:    args[1],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

func newAttachmentRemoveCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <comment-id> <file-id>",
		Short: "Remove a file attachment from a comment",
		Long: `Remove an attached file (by file ID) from a comment.

Usage example:
  retask comment attachment remove cmt_abc123 file_xyz456

Output fields: comment_id, attachments`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.DeleteCommentAttachment(context.Background(), connectrpc.NewRequest(&commentv1.DeleteCommentAttachmentRequest{
				CommentId: args[0],
				FileId:    args[1],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cmd/comment/`
Expected: PASS (`ok  github.com/nwebxyz/retask-cli/internal/cmd/comment`).

- [ ] **Step 5: Verify the whole repo builds**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/comment/command.go internal/cmd/comment/command_test.go
git commit -m "feat(comment): add comment command package"
```

---

## Task 3: Wire into the CLI and document in the help-llm manifest

**Files:**
- Modify: `cmd/retask/main.go:9-21` (imports), `cmd/retask/main.go:80-92` (AddCommand block)
- Modify: `internal/cmd/helpcmd/command.go` (manifest `Commands` slice)

The manifest entries must be added together with the wiring so `cmd/retask/main_test.go`'s drift check passes in the same commit.

- [ ] **Step 1: Import the comment package in `main.go`**

Add to the import block (alphabetical, after the `authcmd` line):

```go
	commentcmd "github.com/nwebxyz/retask-cli/internal/cmd/comment"
```

- [ ] **Step 2: Register the command in `main.go`**

In the "Service commands registered here" block, add (after the `authcmd` line):

```go
	root.AddCommand(commentcmd.NewCommand(gf))
```

- [ ] **Step 3: Add manifest entries in `internal/cmd/helpcmd/command.go`**

In the `Commands: []commandEntry{ ... }` slice, add these entries immediately after the `retask task attachment remove` entry (any location works; grouping with task keeps it readable):

```go
			{Command: "retask comment list", Description: "List comments on a task (top-level by default). Requires workspace context (--workspace-id or NWEB_WORKSPACE_ID). --task is the task ID (mapped to the task NRN). Pass --parent-comment-id to list replies under a top-level comment. --created-by filters by author member NRN (repeatable, format: nweb:workspace:member:<uuid>). --sort: default, created-asc, created-desc", Flags: []string{"--task", "--parent-comment-id", "--sort", "--created-by"}, Example: "retask comment list --task <task-id>"},
			{Command: "retask comment get", Description: "Get a comment by ID", Example: "retask comment get <comment-id>"},
			{Command: "retask comment create", Description: "Create a comment on a task. Requires workspace context (--workspace-id or NWEB_WORKSPACE_ID). --body is stored verbatim as HTML (TipTap getHTML() format); mention a member by embedding <span data-type=\"mention\" data-id=\"nweb:workspace:member:<uuid>\">@Name</span> in the body (the server parses mentions from the body). --parent-comment-id replies to a top-level comment (replies are one level deep)", Flags: []string{"--task", "--body", "--parent-comment-id"}, Example: "retask comment create --task <task-id> --body '<p>Looks good</p>'"},
			{Command: "retask comment update", Description: "Edit a comment's body (only the author can edit; sets is_edited). --body is HTML, same format and mention syntax as create", Flags: []string{"--body"}, Example: "retask comment update <comment-id> --body '<p>Edited</p>'"},
			{Command: "retask comment delete", Description: "Delete a comment (soft-delete; deleting a top-level comment cascade-deletes its replies)", Example: "retask comment delete <comment-id>"},
			{Command: "retask comment attachment add", Description: "Attach a file to a comment (file IDs come from retask file)", Example: "retask comment attachment add <comment-id> <file-id>"},
			{Command: "retask comment attachment remove", Description: "Remove a file attachment from a comment", Example: "retask comment attachment remove <comment-id> <file-id>"},
```

- [ ] **Step 4: Run the full test suite (drift check must pass)**

Run: `go test ./...`
Expected: PASS for all packages, including `cmd/retask` (`TestHelpManifestMatchesCommandTree`). If it reports "flags drift" for a `retask comment …` command, make the manifest `Flags` list match the command's declared flags exactly.

- [ ] **Step 5: Smoke-check the command is wired**

Run: `go run ./cmd/retask comment --help`
Expected: usage listing subcommands `list`, `get`, `create`, `update`, `delete`, `attachment`.

Run: `go run ./cmd/retask help-llm | grep -c 'retask comment'`
Expected: `7`.

- [ ] **Step 6: Commit**

```bash
git add cmd/retask/main.go internal/cmd/helpcmd/command.go
git commit -m "feat(comment): wire comment command and document in help-llm"
```

---

## Task 4: Document comments in the skill file

**Files:**
- Modify: `skills/retask-cli.md`

- [ ] **Step 1: Add a Comments section**

Insert this section immediately before the `## Discovery` section in `skills/retask-cli.md`:

```markdown
## Comments
Comment on tasks with `retask comment`. Comments require workspace context
(`--workspace-id` or `NWEB_WORKSPACE_ID`) and target a task via `--task <task-id>`.

```bash
retask comment create --task <task-id> --body '<p>Looks good</p>'
retask comment list --task <task-id>                       # top-level comments
retask comment list --task <task-id> --parent-comment-id <cmt-id>   # replies
retask comment update <comment-id> --body '<p>Edited</p>'  # author only; sets is_edited
retask comment delete <comment-id>                         # cascades to replies
```

- `--body` is stored verbatim as **HTML** (TipTap `getHTML()` format), the same
  as task descriptions.
- **Mention a member** by embedding a span in the body (the server parses
  mentions from the body on every write):

  ```html
  <span data-type="mention" data-id="nweb:workspace:member:<uuid>">@Name</span>
  ```

- **Threading is one level:** `--parent-comment-id` replies to a top-level
  comment; a reply cannot be replied to.
- **Attachments** reference file IDs from `retask file`:

  ```bash
  retask comment attachment add <comment-id> <file-id>
  retask comment attachment remove <comment-id> <file-id>
  ```
```

- [ ] **Step 2: Verify build and tests still pass**

Run: `go build ./... && go test ./...`
Expected: no build output; all tests PASS.

- [ ] **Step 3: Commit**

```bash
git add skills/retask-cli.md
git commit -m "docs(comment): document comment command in skill file"
```

---

## Self-Review (completed by plan author)

**Spec coverage:** list/get/create/update/delete/attachment → Task 2; `--task`→NRN mapping (`taskNrn`) → Task 2; raw-HTML body → Task 2 `create`/`update`; threading semantics → Task 2 `list`/`create`; body-only `update` → Task 2; workspace-required errors → Task 2; output shapes → Task 2; documentation of HTML/mention/threading/attachments → Tasks 3 (help-llm) + 4 (skill); proto approval + CLAUDE.md → Task 1. All spec sections mapped.

**Placeholder scan:** none — every code and command step contains literal content.

**Type consistency:** helper names (`taskNrn`, `parseNrn`, `parseSort`, `connect`) and generated symbols (`commentv1.CommentsRequest_Filter`, `CommentsRequest_SORT_*`, `commentv1.Comment`, `Add/DeleteCommentAttachmentRequest`, `commentv1connect.NewCommentServiceClient`) are used identically across the test (Task 2 Step 1) and implementation (Task 2 Step 3). Manifest flag lists (Task 3) match the declared flags in Task 2.
