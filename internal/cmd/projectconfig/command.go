// internal/cmd/projectconfig/command.go
package projectconfig

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	retaskcommonv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/common/v1"
	retaskprojectv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/project/v1"
)

// NewCommand returns the top-level "project-config" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project-config",
		Short: "Manage Retask project configuration (task statuses, task types, default view)",
	}
	cmd.AddCommand(
		newGetCommand(gf),
		newSetCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a RetaskProjectServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (retaskprojectv1.RetaskProjectServiceClient, func(), error) {
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
	conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
	if err != nil {
		return nil, nil, err
	}
	return retaskprojectv1.NewRetaskProjectServiceClient(conn), func() { conn.Close() }, nil
}

// ── project-config get ────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <project-id>",
		Short: "Get the project configuration",
		Long: `Fetch the Retask project configuration for a project.

Usage example:
  retask project-config get proj_abc123
  retask project-config get proj_abc123 --pretty

Output fields: project_id, task_statuses, task_types, default_task_view, kanban_config, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			cfg, err := svc.GetProjectConfig(context.Background(), &commonv1.Id{Id: args[0]})
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, cfg)
		},
	}
}

// ── project-config set ────────────────────────────────────────────────────────

func newSetCommand(gf *flags.Global) *cobra.Command {
	var taskStatusesJSON, taskTypesJSON, defaultView string

	cmd := &cobra.Command{
		Use:   "set <project-id>",
		Short: "Set project configuration fields",
		Long: `Update the Retask project configuration. Fetches the existing config first,
then applies only the flags that are explicitly provided.

Usage examples:
  retask project-config set proj_abc123 --default-view TASK_VIEW_KANBAN
  retask project-config set proj_abc123 --task-statuses '[{"status_id":"s1","name":"Todo"},{"status_id":"s2","name":"Done","is_done":true}]'
  retask project-config set proj_abc123 --task-types '[{"type_id":"t1","name":"Bug"},{"type_id":"t2","name":"Feature"}]'

Flags:
  --task-statuses string   JSON array of TaskStatus objects
  --task-types string      JSON array of TaskType objects
  --default-view string    Default task view: TASK_VIEW_KANBAN, TASK_VIEW_TASKS

Output fields: project_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID := args[0]

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			// Fetch existing config to preserve unset fields.
			existing, err := svc.GetProjectConfig(context.Background(), &commonv1.Id{Id: projectID})
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("task-statuses") {
				var statuses []*retaskcommonv1.TaskStatus
				if err := json.Unmarshal([]byte(taskStatusesJSON), &statuses); err != nil {
					return fmt.Errorf("invalid --task-statuses JSON: %w", err)
				}
				existing.TaskStatuses = statuses
			}

			if cmd.Flags().Changed("task-types") {
				var types []*retaskcommonv1.TaskType
				if err := json.Unmarshal([]byte(taskTypesJSON), &types); err != nil {
					return fmt.Errorf("invalid --task-types JSON: %w", err)
				}
				existing.TaskTypes = types
			}

			if cmd.Flags().Changed("default-view") {
				v, ok := retaskprojectv1.ProjectConfig_TaskView_value[defaultView]
				if !ok {
					return fmt.Errorf("invalid --default-view %q. Valid values: TASK_VIEW_KANBAN, TASK_VIEW_TASKS", defaultView)
				}
				existing.DefaultTaskView = retaskprojectv1.ProjectConfig_TaskView(v)
			}

			id, err := svc.SetProjectConfig(context.Background(), existing)
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"project_id": id.Id})
		},
	}

	cmd.Flags().StringVar(&taskStatusesJSON, "task-statuses", "", "JSON array of TaskStatus objects")
	cmd.Flags().StringVar(&taskTypesJSON, "task-types", "", "JSON array of TaskType objects")
	cmd.Flags().StringVar(&defaultView, "default-view", "", "Default task view: TASK_VIEW_KANBAN, TASK_VIEW_TASKS")
	return cmd
}
