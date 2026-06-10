// internal/cmd/project/command.go
package project

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
	projectv1 "github.com/nwebxyz/retask-cli/proto-gen/project/v1"
	projectv1connect "github.com/nwebxyz/retask-cli/proto-gen/project/v1/projectv1connect"
)

// NewCommand returns the top-level "project" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects and project members",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newCreateCommand(gf),
		newUpdateCommand(gf),
		newArchiveCommand(gf),
		newUnarchiveCommand(gf),
		newDeleteCommand(gf),
		newMemberCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a ProjectServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (projectv1connect.ProjectServiceClient, func(), error) {
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
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return projectv1connect.NewProjectServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}

// ── project list ──────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	var archived bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Long: `List projects accessible to the authenticated user.

Usage examples:
  retask project list
  retask project list --archived
  retask project list --pretty

Flags:
  --archived    Show only archived projects (default: show non-archived)

Output fields: project_id, workspace_id, key, name, description, color, icon, visibility, is_archived, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			req := &projectv1.ProjectsRequest{}
			if archived {
				req.Filter = &projectv1.ProjectsRequest_Filter{
					IsArchived:  commonv1.YesNo_Y,
					WorkspaceId: gf.WorkspaceID,
				}
			} else {
				req.Filter = &projectv1.ProjectsRequest_Filter{
					IsArchived:  commonv1.YesNo_N,
					WorkspaceId: gf.WorkspaceID,
				}
			}

			resp, err := svc.GetProjects(context.Background(), connectrpc.NewRequest(req))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Projects)
		},
	}
	cmd.Flags().BoolVar(&archived, "archived", false, "Show only archived projects")
	return cmd
}

// ── project get ───────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <project-id>",
		Short: "Get a project by ID",
		Long: `Fetch a single project by its ID.

Usage example:
  retask project get proj_abc123

Output fields: project_id, workspace_id, key, name, description, color, icon, visibility, is_archived, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			proj, err := svc.GetProject(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, proj.Msg)
		},
	}
}

// ── project create ────────────────────────────────────────────────────────────

func newCreateCommand(gf *flags.Global) *cobra.Command {
	var name, description, visibility, color, icon, workspaceID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new project",
		Long: `Create a new project in the workspace.

Usage example:
  retask project create --name "My Project"
  retask project create --name "Private" --visibility VISIBILITY_RESTRICTED --color "#ff5722"

Flags:
  --name string          Required. Project display name
  --description string   Optional description
  --visibility string    Visibility: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED (default: VISIBILITY_WORKSPACE_EDIT)
  --color string         Optional hex color (e.g. #4287f5)
  --icon string          Optional icon identifier
  --workspace-id string  Optional. Workspace ID (overrides global --workspace-id flag)

Output fields: project_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			wsID := workspaceID
			if wsID == "" {
				wsID = gf.WorkspaceID
			}

			proj := &projectv1.Project{
				Name:        name,
				Description: description,
				Color:       color,
				Icon:        icon,
				WorkspaceId: wsID,
			}

			if cmd.Flags().Changed("visibility") {
				v, ok := projectv1.Visibility_value[visibility]
				if !ok {
					return fmt.Errorf("invalid --visibility %q. Valid values: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED", visibility)
				}
				proj.Visibility = projectv1.Visibility(v)
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetProject(context.Background(), connectrpc.NewRequest(proj))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"project_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Project display name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Project description")
	cmd.Flags().StringVar(&visibility, "visibility", "", "Visibility: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED")
	cmd.Flags().StringVar(&color, "color", "", "Project color (hex, e.g. #4287f5)")
	cmd.Flags().StringVar(&icon, "icon", "", "Project icon identifier")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace ID (overrides global flag and env var)")
	return cmd
}

// ── project update ────────────────────────────────────────────────────────────

func newUpdateCommand(gf *flags.Global) *cobra.Command {
	var name, description, visibility, color, icon string
	cmd := &cobra.Command{
		Use:   "update <project-id>",
		Short: "Update an existing project",
		Long: `Update name, description, visibility, color, or icon of an existing project.

Only explicitly provided flags are sent; omitted flags keep the server value.

Usage example:
  retask project update proj_abc123 --name "New Name"
  retask project update proj_abc123 --visibility VISIBILITY_RESTRICTED

Flags:
  --name string         New display name
  --description string  New description
  --visibility string   New visibility: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED
  --color string        New color (hex)
  --icon string         New icon identifier

Output fields: project_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			// Fetch existing to preserve unset fields.
			existingResp, err := svc.GetProject(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
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
			if cmd.Flags().Changed("icon") {
				existingResp.Msg.Icon = icon
			}
			if cmd.Flags().Changed("visibility") {
				v, ok := projectv1.Visibility_value[visibility]
				if !ok {
					return fmt.Errorf("invalid --visibility %q. Valid values: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED", visibility)
				}
				existingResp.Msg.Visibility = projectv1.Visibility(v)
			}

			resp, err := svc.SetProject(context.Background(), connectrpc.NewRequest(existingResp.Msg))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"project_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New display name")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&visibility, "visibility", "", "New visibility: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED")
	cmd.Flags().StringVar(&color, "color", "", "New color (hex)")
	cmd.Flags().StringVar(&icon, "icon", "", "New icon identifier")
	return cmd
}

// ── project archive ───────────────────────────────────────────────────────────

func newArchiveCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "archive <project-id>",
		Short: "Archive a project",
		Long: `Archive a project by ID. Archived projects are hidden from default list views.

Usage example:
  retask project archive proj_abc123

Output fields: status, project_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.ArchiveProject(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "archived", "project_id": args[0]})
		},
	}
}

// ── project unarchive ─────────────────────────────────────────────────────────

func newUnarchiveCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "unarchive <project-id>",
		Short: "Unarchive a project",
		Long: `Restore an archived project by ID.

Usage example:
  retask project unarchive proj_abc123

Output fields: status, project_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.UnarchiveProject(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "unarchived", "project_id": args[0]})
		},
	}
}

// ── project delete ────────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <project-id>",
		Short: "Delete a project",
		Long: `Soft-delete a project by ID.

Usage example:
  retask project delete proj_abc123

Output fields: status, project_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteProject(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "project_id": args[0]})
		},
	}
}

// ── project member ────────────────────────────────────────────────────────────

func newMemberCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "member",
		Short: "Manage project members",
	}
	cmd.AddCommand(
		newMemberListCommand(gf),
		newMemberAddCommand(gf),
		newMemberRemoveCommand(gf),
	)
	return cmd
}

func newMemberListCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list <project-id>",
		Short: "List members of a project",
		Long: `List all members of a project.

Usage example:
  retask project member list proj_abc123

Output fields: project_member_id, project_id, role, member (workspace_member_id, display_name), created_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetProjectMembers(context.Background(), connectrpc.NewRequest(&projectv1.ProjectMembersRequest{
				ProjectId: args[0],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Members)
		},
	}
}

func newMemberAddCommand(gf *flags.Global) *cobra.Command {
	var memberID, role string
	cmd := &cobra.Command{
		Use:   "add <project-id>",
		Short: "Add a member to a project",
		Long: `Add a workspace member to a project with the specified role.

Usage example:
  retask project member add proj_abc123 --member-id mbr_xyz --role MEMBER_ROLE_EDITOR
  retask project member add proj_abc123 --member-id mbr_xyz --role MEMBER_ROLE_VIEWER

Flags:
  --member-id string  Required. Workspace member ID to add
  --role string       Required. Role: MEMBER_ROLE_VIEWER, MEMBER_ROLE_EDITOR, MEMBER_ROLE_ADMIN

Output fields: status`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if memberID == "" {
				return fmt.Errorf("--member-id is required")
			}
			if role == "" {
				return fmt.Errorf("--role is required")
			}
			v, ok := projectv1.MemberRole_value[role]
			if !ok {
				return fmt.Errorf("invalid --role %q. Valid values: MEMBER_ROLE_VIEWER, MEMBER_ROLE_EDITOR, MEMBER_ROLE_ADMIN", role)
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.SetProjectMember(context.Background(), connectrpc.NewRequest(&projectv1.ProjectMember{
				ProjectId: args[0],
				Role:      projectv1.MemberRole(v),
				Member: &projectv1.WorkspaceMemberSnapshot{
					WorkspaceMemberId: memberID,
				},
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "added", "project_id": args[0], "member_id": memberID})
		},
	}
	cmd.Flags().StringVar(&memberID, "member-id", "", "Workspace member ID to add (required)")
	cmd.Flags().StringVar(&role, "role", "", "Role: MEMBER_ROLE_VIEWER, MEMBER_ROLE_EDITOR, MEMBER_ROLE_ADMIN (required)")
	return cmd
}

func newMemberRemoveCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <project-id> <project-member-id>",
		Short: "Remove a member from a project",
		Long: `Remove a member from a project by their project member ID.

Usage example:
  retask project member remove proj_abc123 pm_xyz

Output fields: status, project_member_id`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.RemoveProjectMember(context.Background(), connectrpc.NewRequest(&projectv1.RemoveProjectMemberRequest{
				ProjectId:       args[0],
				ProjectMemberId: args[1],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "removed", "project_member_id": args[1]})
		},
	}
}
