// internal/cmd/workspace/command.go
package workspace

import (
	"context"
	"fmt"

	connectrpc "connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	workspacev1 "github.com/nwebxyz/retask-cli/proto-gen/workspace/v1"
	workspacev1connect "github.com/nwebxyz/retask-cli/proto-gen/workspace/v1/workspacev1connect"
)

// NewCommand returns the top-level "workspace" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspaces and members",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newCreateCommand(gf),
		newUpdateCommand(gf),
		newDeleteCommand(gf),
		newMemberCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a WorkspaceServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (workspacev1connect.WorkspaceServiceClient, func(), error) {
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
	return workspacev1connect.NewWorkspaceServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}

// ── workspace list ────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all workspaces",
		Long: `List all workspaces accessible to the authenticated user.

Usage example:
  retask workspace list
  retask workspace list --pretty

Output fields: workspace_id, name, description, color, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetWorkspaces(context.Background(), connectrpc.NewRequest(&workspacev1.WorkspacesRequest{}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Workspaces)
		},
	}
}

// ── workspace get ─────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <workspace-id>",
		Short: "Get a workspace by ID",
		Long: `Fetch a single workspace by its ID.

Usage example:
  retask workspace get ws_abc123

Output fields: workspace_id, name, description, color, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetWorkspace(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

// ── workspace create ──────────────────────────────────────────────────────────

func newCreateCommand(gf *flags.Global) *cobra.Command {
	var name, description, color string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new workspace",
		Long: `Create a new workspace.

Usage example:
  retask workspace create --name "My Team" --description "Shared workspace" --color "#4287f5"

Flags:
  --name string         Required. Workspace display name
  --description string  Optional description
  --color string        Optional hex colour (e.g. #4287f5)

Output fields: workspace_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetWorkspace(context.Background(), connectrpc.NewRequest(&workspacev1.Workspace{
				Name:        name,
				Description: description,
				Color:       color,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"workspace_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Workspace display name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Workspace description")
	cmd.Flags().StringVar(&color, "color", "", "Workspace color (hex, e.g. #4287f5)")
	return cmd
}

// ── workspace update ──────────────────────────────────────────────────────────

func newUpdateCommand(gf *flags.Global) *cobra.Command {
	var name, description, color string
	cmd := &cobra.Command{
		Use:   "update <workspace-id>",
		Short: "Update an existing workspace",
		Long: `Update name, description, or color of an existing workspace.

Only explicitly provided flags are sent; omitted flags keep the server value.

Usage example:
  retask workspace update ws_abc123 --name "New Name"
  retask workspace update ws_abc123 --color "#ff0000"

Flags:
  --name string         New display name
  --description string  New description
  --color string        New color (hex)

Output fields: workspace_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			// Fetch existing to preserve unset fields.
			existingResp, err := svc.GetWorkspace(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("name") {
				existingResp.Msg.Name = name
			}
			if cmd.Flags().Changed("description") {
				existingResp.Msg.Description = description
			}
			if cmd.Flags().Changed("color") {
				existingResp.Msg.Color = color
			}

			resp, err := svc.SetWorkspace(context.Background(), connectrpc.NewRequest(existingResp.Msg))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"workspace_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New display name")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&color, "color", "", "New color (hex)")
	return cmd
}

// ── workspace delete ──────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <workspace-id>",
		Short: "Delete a workspace",
		Long: `Soft-delete a workspace by ID.

Usage example:
  retask workspace delete ws_abc123

Output fields: status, workspace_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteWorkspace(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "workspace_id": args[0]})
		},
	}
}

// ── workspace member ──────────────────────────────────────────────────────────

func newMemberCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "member",
		Short: "Manage workspace members",
	}
	cmd.AddCommand(
		newMemberListCommand(gf),
		newMemberInviteCommand(gf),
		newMemberUpdateCommand(gf),
		newMemberRemoveCommand(gf),
	)
	return cmd
}

func newMemberListCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list <workspace-id>",
		Short: "List members of a workspace",
		Long: `List all members of a workspace.

Usage example:
  retask workspace member list ws_abc123

Output fields: workspace_member_id, workspace_id, role, invited_email, display_name, membership_status, created_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetWorkspaceMembers(context.Background(), connectrpc.NewRequest(&workspacev1.WorkspaceMembersRequest{
				WorkspaceId: args[0],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Members)
		},
	}
}

func newMemberInviteCommand(gf *flags.Global) *cobra.Command {
	var email, role, displayName string
	cmd := &cobra.Command{
		Use:   "invite <workspace-id>",
		Short: "Invite a user to a workspace",
		Long: `Invite a user to a workspace by email.

Usage example:
  retask workspace member invite ws_abc123 --email user@example.com --role EDITOR
  retask workspace member invite ws_abc123 --email user@example.com --role VIEWER --display-name "Alice"

Flags:
  --email string         Required. Email of the user to invite
  --role string          Required. Role: VIEWER, EDITOR, ADMIN, OWNER
  --display-name string  Optional display name set by the inviter

Output fields: status`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if email == "" {
				return fmt.Errorf("--email is required")
			}
			if role == "" {
				return fmt.Errorf("--role is required")
			}
			v, ok := workspacev1.WorkspaceMemberRole_value[role]
			if !ok {
				return fmt.Errorf("invalid --role %q. Valid values: VIEWER, EDITOR, ADMIN, OWNER", role)
			}
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.InviteWorkspaceMember(context.Background(), connectrpc.NewRequest(&workspacev1.InviteWorkspaceMemberRequest{
				WorkspaceId:  args[0],
				InvitedEmail: email,
				DisplayName:  displayName,
				Role:         workspacev1.WorkspaceMemberRole(v),
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "invited", "invited_email": email})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Email of the user to invite (required)")
	cmd.Flags().StringVar(&role, "role", "", "Role: VIEWER, EDITOR, ADMIN, OWNER (required)")
	cmd.Flags().StringVar(&displayName, "display-name", "", "Display name set by the inviter")
	return cmd
}

func newMemberUpdateCommand(gf *flags.Global) *cobra.Command {
	var role, displayName string
	cmd := &cobra.Command{
		Use:   "update <workspace-id> <member-id>",
		Short: "Update a workspace member's role or display name",
		Long: `Update the role or display name of a workspace member.

Only explicitly provided flags are sent; omitted flags keep their current value.

Usage example:
  retask workspace member update ws_abc123 mbr_xyz --role ADMIN
  retask workspace member update ws_abc123 mbr_xyz --display-name "Bob"

Flags:
  --role string          New role: VIEWER, EDITOR, ADMIN, OWNER
  --display-name string  New display name

Output fields: status`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &workspacev1.UpdateWorkspaceMemberRequest{
				WorkspaceId:       args[0],
				WorkspaceMemberId: args[1],
			}

			if cmd.Flags().Changed("role") {
				v, ok := workspacev1.WorkspaceMemberRole_value[role]
				if !ok {
					return fmt.Errorf("invalid --role %q. Valid values: VIEWER, EDITOR, ADMIN, OWNER", role)
				}
				req.Role = workspacev1.WorkspaceMemberRole(v)
			}
			if cmd.Flags().Changed("display-name") {
				req.DisplayName = displayName
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.UpdateWorkspaceMember(context.Background(), connectrpc.NewRequest(req))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "updated", "workspace_member_id": args[1]})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "New role: VIEWER, EDITOR, ADMIN, OWNER")
	cmd.Flags().StringVar(&displayName, "display-name", "", "New display name")
	return cmd
}

func newMemberRemoveCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <workspace-id> <member-id>",
		Short: "Remove a member from a workspace",
		Long: `Remove a member from a workspace.

Usage example:
  retask workspace member remove ws_abc123 mbr_xyz

Output fields: status, workspace_member_id`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.RemoveWorkspaceMember(context.Background(), connectrpc.NewRequest(&workspacev1.RemoveWorkspaceMemberRequest{
				WorkspaceId:       args[0],
				WorkspaceMemberId: args[1],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "removed", "workspace_member_id": args[1]})
		},
	}
}
