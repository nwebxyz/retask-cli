// internal/cmd/task/command.go
package task

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
	taskv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/task/v1"
	taskv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/task/v1/taskv1connect"
)

// NewCommand returns the top-level "task" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage tasks",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newGetByKeyCommand(gf),
		newCreateCommand(gf),
		newUpdateCommand(gf),
		newDeleteCommand(gf),
		newAttachmentCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a TaskServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (taskv1connect.TaskServiceClient, func(), error) {
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
	return taskv1connect.NewTaskServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}

// ── task list ─────────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	var projectID, priority string
	var statusIDs, assigneeNrns []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Long: `List tasks with optional filters.

Usage examples:
  retask task list
  retask task list --project-id proj_abc123
  retask task list --project-id proj_abc123 --priority HIGH
  retask task list --assignee "nweb:workspace:member:uuid1" --assignee "nweb:workspace:member:uuid2"

Flags:
  --project-id string   Filter by project ID
  --status string       Filter by status ID (repeatable)
  --assignee string     Filter by assignee NRN (repeatable, format: nweb:workspace:member:<uuid>)
  --priority string     Filter by priority: UNKNOWN, LOW, MEDIUM, HIGH, URGENT

Output fields: task_id, project_id, workspace_id, key, title, description, priority, status, due_at, assignee_nrns, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			filter := &taskv1.TasksRequest_Filter{}
			if projectID != "" {
				filter.ProjectIds = []string{projectID}
			}
			if len(statusIDs) > 0 {
				filter.StatusIds = statusIDs
			}
			if len(assigneeNrns) > 0 {
				filter.AssigneeNrns = assigneeNrns
			}
			if cmd.Flags().Changed("priority") {
				v, ok := taskv1.Task_Priority_value[priority]
				if !ok {
					return fmt.Errorf("invalid --priority %q. Valid values: UNKNOWN, LOW, MEDIUM, HIGH, URGENT", priority)
				}
				filter.Priority = taskv1.Task_Priority(v)
			}

			if gf.WorkspaceID != "" {
				filter.WorkspaceId = gf.WorkspaceID
			}

			resp, err := svc.GetTasks(context.Background(), connectrpc.NewRequest(&taskv1.TasksRequest{
				Filter: filter,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Tasks)
		},
	}
	cmd.Flags().StringVar(&projectID, "project-id", "", "Filter by project ID")
	cmd.Flags().StringArrayVar(&statusIDs, "status", nil, "Filter by status ID (repeatable)")
	cmd.Flags().StringArrayVar(&assigneeNrns, "assignee", nil, "Filter by assignee NRN (repeatable)")
	cmd.Flags().StringVar(&priority, "priority", "", "Filter by priority: UNKNOWN, LOW, MEDIUM, HIGH, URGENT")
	return cmd
}

// ── task get ──────────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <task-id>",
		Short: "Get a task by ID",
		Long: `Fetch a single task by its ID.

Usage example:
  retask task get task_abc123

Output fields: task_id, project_id, workspace_id, key, title, description, priority, status, due_at, assignee_nrns, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetTask(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

// ── task get-by-key ───────────────────────────────────────────────────────────

func newGetByKeyCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-by-key <key>",
		Short: "Get a task by its key",
		Long: `Fetch a single task by its human-readable key (e.g. PROJ-42).

The workspace ID is required and can be provided via the global --workspace-id flag or NWEB_WORKSPACE_ID env var.

Usage example:
  retask task get-by-key PROJ-42
  retask task get-by-key PROJ-42 --workspace-id ws_abc123

Output fields: task_id, project_id, workspace_id, key, title, description, priority, status, due_at, assignee_nrns, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if gf.WorkspaceID == "" {
				return fmt.Errorf("--workspace-id is required (or set NWEB_WORKSPACE_ID)")
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetTaskByKey(context.Background(), connectrpc.NewRequest(&taskv1.TaskByKeyRequest{
				WorkspaceId: gf.WorkspaceID,
				Key:         args[0],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
	return cmd
}

// ── task create ───────────────────────────────────────────────────────────────

func newCreateCommand(gf *flags.Global) *cobra.Command {
	var projectID, title, description, priority, dueAt string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new task",
		Long: `Create a new task in a project.

Usage examples:
  retask task create --project-id proj_abc123 --title "Fix login bug"
  retask task create --project-id proj_abc123 --title "New feature" --priority HIGH --due-at "2026-12-31T00:00:00Z"

Flags:
  --project-id string    Required. Project ID to create the task in
  --title string         Required. Task title
  --description string   Optional task description
  --priority string      Priority: UNKNOWN, LOW, MEDIUM, HIGH, URGENT
  --due-at string        Due date in RFC3339 format (e.g. 2026-12-31T00:00:00Z)

Output fields: task_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if projectID == "" {
				return fmt.Errorf("--project-id is required")
			}
			if title == "" {
				return fmt.Errorf("--title is required")
			}

			task := &taskv1.Task{
				ProjectId:   projectID,
				Title:       title,
				Description: description,
			}

			if cmd.Flags().Changed("priority") {
				v, ok := taskv1.Task_Priority_value[priority]
				if !ok {
					return fmt.Errorf("invalid --priority %q. Valid values: UNKNOWN, LOW, MEDIUM, HIGH, URGENT", priority)
				}
				task.Priority = taskv1.Task_Priority(v)
			}

			if cmd.Flags().Changed("due-at") {
				ts, err := parseTimestamp(dueAt)
				if err != nil {
					return fmt.Errorf("invalid --due-at %q: %w", dueAt, err)
				}
				task.DueAt = ts
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetTask(context.Background(), connectrpc.NewRequest(task))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"task_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&projectID, "project-id", "", "Project ID (required)")
	cmd.Flags().StringVar(&title, "title", "", "Task title (required)")
	cmd.Flags().StringVar(&description, "description", "", "Task description")
	cmd.Flags().StringVar(&priority, "priority", "", "Priority: UNKNOWN, LOW, MEDIUM, HIGH, URGENT")
	cmd.Flags().StringVar(&dueAt, "due-at", "", "Due date in RFC3339 format (e.g. 2026-12-31T00:00:00Z)")
	return cmd
}

// ── task update ───────────────────────────────────────────────────────────────

func newUpdateCommand(gf *flags.Global) *cobra.Command {
	var title, description, priority, statusID, assigneeNrn, dueAt string
	cmd := &cobra.Command{
		Use:   "update <task-id>",
		Short: "Update an existing task",
		Long: `Update fields of an existing task using partial update (only changed fields are sent).

Usage examples:
  retask task update task_abc123 --title "Updated title"
  retask task update task_abc123 --priority URGENT --due-at "2026-12-31T00:00:00Z"

Flags:
  --title string         New task title
  --description string   New description
  --priority string      New priority: UNKNOWN, LOW, MEDIUM, HIGH, URGENT
  --status string        New status ID
  --assignee string      New assignee NRN (e.g. nweb:workspace:member:<uuid>)
  --due-at string        New due date in RFC3339 format

Output fields: task_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data := make(map[string]string)

			if cmd.Flags().Changed("title") {
				data["title"] = title
			}
			if cmd.Flags().Changed("description") {
				data["description"] = description
			}
			if cmd.Flags().Changed("priority") {
				_, ok := taskv1.Task_Priority_value[priority]
				if !ok {
					return fmt.Errorf("invalid --priority %q. Valid values: UNKNOWN, LOW, MEDIUM, HIGH, URGENT", priority)
				}
				data["priority"] = priority
			}
			if cmd.Flags().Changed("status") {
				data["status_id"] = statusID
			}
			if cmd.Flags().Changed("assignee") {
				data["assignee_nrn"] = assigneeNrn
			}
			if cmd.Flags().Changed("due-at") {
				data["due_at"] = dueAt
			}

			if len(data) == 0 {
				return fmt.Errorf("no fields to update: provide at least one flag")
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetPartialTask(context.Background(), connectrpc.NewRequest(&commonv1.PartialData{
				Id:   args[0],
				Data: data,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"task_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New task title")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&priority, "priority", "", "New priority: UNKNOWN, LOW, MEDIUM, HIGH, URGENT")
	cmd.Flags().StringVar(&statusID, "status", "", "New status ID")
	cmd.Flags().StringVar(&assigneeNrn, "assignee", "", "New assignee NRN (e.g. nweb:workspace:member:<uuid>)")
	cmd.Flags().StringVar(&dueAt, "due-at", "", "New due date in RFC3339 format")
	return cmd
}

// ── task delete ───────────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <task-id>",
		Short: "Delete a task",
		Long: `Soft-delete a task by ID.

Usage example:
  retask task delete task_abc123

Output fields: status, task_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteTask(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "task_id": args[0]})
		},
	}
}

// ── task attachment ───────────────────────────────────────────────────────────

func newAttachmentCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "Manage task attachments",
	}
	cmd.AddCommand(
		newAttachmentAddCommand(gf),
		newAttachmentRemoveCommand(gf),
	)
	return cmd
}

func newAttachmentAddCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "add <task-id> <file-id>",
		Short: "Add a file attachment to a task",
		Long: `Attach a file (by file ID) to a task.

Usage example:
  retask task attachment add task_abc123 file_xyz456

Output fields: task_id, attachments`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.AddTaskAttachment(context.Background(), connectrpc.NewRequest(&taskv1.AddTaskAttachmentRequest{
				TaskId: args[0],
				FileId: args[1],
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
		Use:   "remove <task-id> <file-id>",
		Short: "Remove a file attachment from a task",
		Long: `Remove an attached file (by file ID) from a task.

Usage example:
  retask task attachment remove task_abc123 file_xyz456

Output fields: task_id, attachments`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.DeleteTaskAttachment(context.Background(), connectrpc.NewRequest(&taskv1.DeleteTaskAttachmentRequest{
				TaskId: args[0],
				FileId: args[1],
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}
