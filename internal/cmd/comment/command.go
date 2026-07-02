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
