// internal/cmd/agent/command.go
package agent

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"nweb.xyz/retask-cli/internal/auth"
	"nweb.xyz/retask-cli/internal/client"
	"nweb.xyz/retask-cli/internal/config"
	"nweb.xyz/retask-cli/internal/flags"
	"nweb.xyz/retask-cli/internal/output"
	commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
	agentv1 "nweb.xyz/retask-cli/proto-gen/retask/agent/v1"
)

// NewCommand returns the top-level "agent" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newCreateCommand(gf),
		newUpdateCommand(gf),
		newDeleteCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns an AgentServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (agentv1.AgentServiceClient, func(), error) {
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
	return agentv1.NewAgentServiceClient(conn), func() { conn.Close() }, nil
}

// validRoles returns the set of valid role string values (excluding ROLE_UNKNOWN).
var validRoles = func() string {
	return "ROLE_UNKNOWN, ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR"
}()

// parseRole validates and converts a role string to the enum value.
func parseRole(s string) (agentv1.Agent_Role, error) {
	v, ok := agentv1.Agent_Role_value[s]
	if !ok {
		return 0, fmt.Errorf("invalid --role %q. Valid values: %s", s, validRoles)
	}
	return agentv1.Agent_Role(v), nil
}

// ── agent list ────────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agents",
		Long: `List agents with optional filters.

Usage examples:
  retask agent list
  retask agent list --role ROLE_TASK_PLANNER

Flags:
  --role string   Filter by role: ROLE_UNKNOWN, ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR

Output fields: agent_id, workspace_id, name, description, role, sandbox_template_id, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := &agentv1.AgentsRequest_Filter{}
			if cmd.Flags().Changed("role") {
				r, err := parseRole(role)
				if err != nil {
					return err
				}
				filter.Roles = []agentv1.Agent_Role{r}
			}

			if gf.WorkspaceID != "" {
				filter.WorkspaceId = gf.WorkspaceID
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			resp, err := svc.GetAgents(context.Background(), &agentv1.AgentsRequest{
				Filter: filter,
			})
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Agents)
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "Filter by role: ROLE_UNKNOWN, ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
	return cmd
}

// ── agent get ─────────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get an agent by ID",
		Long: `Fetch a single agent by its ID.

Usage example:
  retask agent get agent_abc123

Output fields: agent_id, workspace_id, name, description, role, sandbox_template_id, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			agent, err := svc.GetAgent(context.Background(), &commonv1.Id{Id: args[0]})
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, agent)
		},
	}
}

// ── agent create ──────────────────────────────────────────────────────────────

func newCreateCommand(gf *flags.Global) *cobra.Command {
	var name, role, description, sandboxTemplateID, workspaceID string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new agent",
		Long: `Create a new agent.

Usage examples:
  retask agent create --name "My Planner" --role ROLE_TASK_PLANNER
  retask agent create --name "Processor" --role ROLE_TASK_PROCESSOR --description "Handles tasks" --sandbox-template-id tmpl_abc123

Flags:
  --name string                 Required. Agent name
  --role string                 Required. Role: ROLE_UNKNOWN, ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR
  --description string          Optional description
  --sandbox-template-id string  Optional sandbox template ID
  --workspace-id string         Optional. Workspace ID (overrides global --workspace-id flag)

Output fields: agent_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if !cmd.Flags().Changed("role") {
				return fmt.Errorf("--role is required")
			}

			r, err := parseRole(role)
			if err != nil {
				return err
			}

			wsID := workspaceID
			if wsID == "" {
				wsID = gf.WorkspaceID
			}

			agent := &agentv1.Agent{
				Name:        name,
				Role:        r,
				Description: description,
				WorkspaceId: wsID,
			}
			if cmd.Flags().Changed("sandbox-template-id") {
				agent.SandboxTemplateId = sandboxTemplateID
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			id, err := svc.SetAgent(context.Background(), agent)
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"agent_id": id.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Agent name (required)")
	cmd.Flags().StringVar(&role, "role", "", "Agent role: ROLE_UNKNOWN, ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR (required)")
	cmd.Flags().StringVar(&description, "description", "", "Agent description")
	cmd.Flags().StringVar(&sandboxTemplateID, "sandbox-template-id", "", "Sandbox template ID")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace ID (overrides global flag and env var)")
	return cmd
}

// ── agent update ──────────────────────────────────────────────────────────────

func newUpdateCommand(gf *flags.Global) *cobra.Command {
	var name, role, description, sandboxTemplateID string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an existing agent",
		Long: `Update fields of an existing agent (fetch-then-patch; only changed flags are applied).

Usage examples:
  retask agent update agent_abc123 --name "New Name"
  retask agent update agent_abc123 --role ROLE_TASK_PROCESSOR --description "Updated"

Flags:
  --name string                 New agent name
  --role string                 New role: ROLE_UNKNOWN, ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR
  --description string          New description
  --sandbox-template-id string  New sandbox template ID

Output fields: agent_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			// Fetch existing to preserve unset fields.
			existing, err := svc.GetAgent(context.Background(), &commonv1.Id{Id: args[0]})
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("name") {
				existing.Name = name
			}
			if cmd.Flags().Changed("description") {
				existing.Description = description
			}
			if cmd.Flags().Changed("role") {
				r, err := parseRole(role)
				if err != nil {
					return err
				}
				existing.Role = r
			}
			if cmd.Flags().Changed("sandbox-template-id") {
				existing.SandboxTemplateId = sandboxTemplateID
			}

			id, err := svc.SetAgent(context.Background(), existing)
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"agent_id": id.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New agent name")
	cmd.Flags().StringVar(&role, "role", "", "New role: ROLE_UNKNOWN, ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().StringVar(&sandboxTemplateID, "sandbox-template-id", "", "New sandbox template ID")
	return cmd
}

// ── agent delete ──────────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an agent",
		Long: `Soft-delete an agent by ID.

Usage example:
  retask agent delete agent_abc123

Output fields: status, agent_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteAgent(context.Background(), &commonv1.Id{Id: args[0]})
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "agent_id": args[0]})
		},
	}
}
