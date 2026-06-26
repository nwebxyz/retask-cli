// internal/cmd/helpcmd/command.go
package helpcmd

import (
	"encoding/json"
	"os"

	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/version"
	"github.com/spf13/cobra"
)

func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "help-llm",
		Short: "Print machine-readable command manifest for LLM injection",
		Long: `Print a JSON manifest of all retask CLI commands, flags, and examples.
Designed to be injected into an LLM system prompt or tool definition.

Usage example:
  retask help-llm
  retask help-llm | jq '.commands[] | select(.command | contains("task"))'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest := buildManifest()
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(manifest)
		},
	}
	return cmd
}

type commandEntry struct {
	Command     string   `json:"command"`
	Description string   `json:"description"`
	Flags       []string `json:"flags,omitempty"`
	Example     string   `json:"example"`
}

// FlagsByCommand returns the documented flags for each command in the manifest,
// keyed by full command path (e.g. "retask sandbox connect"). Exposed so a test
// can verify the hand-maintained manifest stays in sync with the command tree.
func FlagsByCommand() map[string][]string {
	m := buildManifest()
	out := make(map[string][]string, len(m.Commands))
	for _, c := range m.Commands {
		out[c.Command] = c.Flags
	}
	return out
}

type manifest struct {
	CLI      string         `json:"cli"`
	Version  string         `json:"version"`
	Auth     authInfo       `json:"auth"`
	Commands []commandEntry `json:"commands"`
}

type authInfo struct {
	RequiredEnv []string `json:"required_env"`
	OptionalEnv []string `json:"optional_env"`
}

func buildManifest() manifest {
	return manifest{
		CLI:     "retask",
		Version: version.Version,
		Auth: authInfo{
			RequiredEnv: []string{"NWEB_API_KEY", "NWEB_WORKSPACE_ID"},
			OptionalEnv: []string{"NWEB_API_TOKEN", "NWEB_API_ENDPOINT", "RETASK_PROFILE", "RETASK_NO_PERSIST"},
		},
		Commands: []commandEntry{
			{Command: "retask auth login", Description: "Exchange PAT for JWT, save to profile", Example: "retask auth login"},
			{Command: "retask auth logout", Description: "Clear cached JWT from active profile", Example: "retask auth logout"},
			{Command: "retask auth whoami", Description: "Print identity and workspace membership for the active token. Output: user_nrn, workspace_id, jwt_expires, endpoint, workspace_member.{nrn, role, membership_status, display_name, name, email, joined_at}", Example: "retask auth whoami"},
			{Command: "retask auth pat list", Description: "List PATs for current user", Example: "retask auth pat list"},
			{Command: "retask auth pat create", Description: "Create a new PAT", Flags: []string{"--name", "--description", "--expires-at"}, Example: "retask auth pat create --name ci-bot"},
			{Command: "retask auth pat revoke", Description: "Revoke a PAT by ID", Example: "retask auth pat revoke <pat-id>"},
			{Command: "retask workspace list", Description: "List workspaces accessible to the user", Example: "retask workspace list"},
			{Command: "retask workspace get", Description: "Get a workspace by ID", Example: "retask workspace get <workspace-id>"},
			{Command: "retask workspace create", Description: "Create a workspace", Flags: []string{"--name", "--description", "--color"}, Example: "retask workspace create --name 'My Team'"},
			{Command: "retask workspace update", Description: "Update workspace fields", Flags: []string{"--name", "--description", "--color"}, Example: "retask workspace update <id> --name 'New Name'"},
			{Command: "retask workspace delete", Description: "Delete a workspace", Example: "retask workspace delete <workspace-id>"},
			{Command: "retask workspace member list", Description: "List workspace members", Example: "retask workspace member list <workspace-id>"},
			{Command: "retask workspace member invite", Description: "Invite a member by email", Flags: []string{"--email", "--role", "--display-name"}, Example: "retask workspace member invite <ws-id> --email user@example.com --role EDITOR"},
			{Command: "retask workspace member update", Description: "Update member role or display name", Flags: []string{"--role", "--display-name"}, Example: "retask workspace member update <ws-id> <member-id> --role ADMIN"},
			{Command: "retask workspace member remove", Description: "Remove a member from workspace", Example: "retask workspace member remove <ws-id> <member-id>"},
			{Command: "retask customer profile get", Description: "Get your customer profile", Example: "retask customer profile get"},
			{Command: "retask customer profile set", Description: "Update your customer profile", Flags: []string{"--name", "--email", "--timezone", "--theme"}, Example: "retask customer profile set --name Alice --timezone America/New_York"},
			{Command: "retask customer list", Description: "List customers (admin only)", Example: "retask customer list"},
			{Command: "retask customer get", Description: "Get a customer by ID", Example: "retask customer get <customer-id>"},
			{Command: "retask project list", Description: "List projects in workspace", Flags: []string{"--archived"}, Example: "retask project list"},
			{Command: "retask project get", Description: "Get a project by ID", Example: "retask project get <project-id>"},
			{Command: "retask project create", Description: "Create a project", Flags: []string{"--name", "--description", "--visibility", "--color", "--icon"}, Example: "retask project create --name Backend --visibility VISIBILITY_WORKSPACE_EDIT"},
			{Command: "retask project update", Description: "Update project fields", Flags: []string{"--name", "--description", "--visibility", "--color", "--icon"}, Example: "retask project update <id> --name 'New Name'"},
			{Command: "retask project archive", Description: "Archive a project", Example: "retask project archive <project-id>"},
			{Command: "retask project unarchive", Description: "Unarchive a project", Example: "retask project unarchive <project-id>"},
			{Command: "retask project delete", Description: "Delete a project", Example: "retask project delete <project-id>"},
			{Command: "retask project member list", Description: "List project members", Example: "retask project member list <project-id>"},
			{Command: "retask project member add", Description: "Add a workspace member to a project", Flags: []string{"--member-id", "--role"}, Example: "retask project member add <proj-id> --member-id <mem-id> --role MEMBER_ROLE_EDITOR"},
			{Command: "retask project member remove", Description: "Remove a member from a project", Example: "retask project member remove <proj-id> <member-id>"},
			{Command: "retask file list", Description: "List files", Flags: []string{"--project-id"}, Example: "retask file list --project-id <proj-id>"},
			{Command: "retask file get", Description: "Get a file by ID", Example: "retask file get <file-id>"},
			{Command: "retask file delete", Description: "Delete a file", Example: "retask file delete <file-id>"},
			{Command: "retask file signed-url", Description: "Get a signed download URL", Flags: []string{"--expires-in"}, Example: "retask file signed-url <file-id> --expires-in 1h"},
			{Command: "retask integration provider list", Description: "List integration providers", Example: "retask integration provider list"},
			{Command: "retask integration provider get", Description: "Get a provider by ID", Example: "retask integration provider get <provider-id>"},
			{Command: "retask integration list", Description: "List integrations", Flags: []string{"--provider-id"}, Example: "retask integration list --provider-id github"},
			{Command: "retask integration get", Description: "Get an integration by ID", Example: "retask integration get <integration-id>"},
			{Command: "retask integration set", Description: "Create or update an integration", Flags: []string{"--provider-id", "--level", "--access-token"}, Example: "retask integration set --provider-id github --access-token ghp_xxx"},
			{Command: "retask integration delete", Description: "Delete an integration", Example: "retask integration delete <integration-id>"},
			{Command: "retask integration github repos", Description: "List GitHub repos accessible via integration", Flags: []string{"--level"}, Example: "retask integration github repos"},
			{Command: "retask task list", Description: "List tasks. --assignee is repeatable and accepts workspace_member_nrns (format: nweb:workspace:member:<uuid>, obtainable from retask auth whoami or retask workspace member list)", Flags: []string{"--project-id", "--status", "--assignee", "--priority"}, Example: "retask task list --assignee nweb:workspace:member:<uuid1> --assignee nweb:workspace:member:<uuid2>"},
			{Command: "retask task get", Description: "Get a task by ID", Example: "retask task get <task-id>"},
			{Command: "retask task get-by-key", Description: "Get a task by key (e.g. ENG-42)", Example: "retask task get-by-key ENG-42"},
			{Command: "retask task create", Description: "Create a task. Requires workspace context (--workspace-id or NWEB_WORKSPACE_ID). --description accepts simple HTML. --parent-task-id makes the task a subtask of that task. --assignee is repeatable and takes workspace_member_nrns (format: nweb:workspace:member:<uuid>); --reporter takes the same NRN format and defaults to the creator", Flags: []string{"--project-id", "--title", "--description", "--priority", "--due-at", "--parent-task-id", "--assignee", "--reporter", "--estimation-points"}, Example: "retask task create --project-id <id> --title 'Fix bug' --priority HIGH --parent-task-id <parent-task-id>"},
			{Command: "retask task update", Description: "Partial update a task (only set flags change). --status takes a status ID and --task-type a task type ID, both of which must exist in the project's config. --assignee is repeatable and replaces all assignees (workspace_member_nrns, format: nweb:workspace:member:<uuid>); pass it empty to clear. --parent-task-id and --reporter accept an empty string to clear", Flags: []string{"--title", "--description", "--priority", "--status", "--task-type", "--assignee", "--parent-task-id", "--reporter", "--estimation-points", "--due-at"}, Example: "retask task update <id> --status <status-id> --priority HIGH"},
			{Command: "retask task delete", Description: "Delete a task", Example: "retask task delete <task-id>"},
			{Command: "retask task attachment add", Description: "Attach a file to a task", Example: "retask task attachment add <task-id> <file-id>"},
			{Command: "retask task attachment remove", Description: "Remove a file attachment from a task", Example: "retask task attachment remove <task-id> <file-id>"},
			{Command: "retask project-config get", Description: "Get Retask project config (statuses, types, kanban)", Example: "retask project-config get <project-id>"},
			{Command: "retask project-config set", Description: "Update Retask project config", Flags: []string{"--task-statuses", "--task-types", "--default-view"}, Example: "retask project-config set <project-id> --default-view TASK_VIEW_KANBAN"},
			{Command: "retask sandbox list", Description: "List sandboxes", Flags: []string{"--status", "--type"}, Example: "retask sandbox list --type PRIVATE"},
			{Command: "retask sandbox get", Description: "Get a sandbox by ID", Example: "retask sandbox get <sandbox-id>"},
			{Command: "retask sandbox create", Description: "Create a sandbox. Custom config via --env/--git-repo/--startup-command/--session-init-command/--shutdown-policy/--integration-provider-id, all mutually exclusive with --template-id", Flags: []string{"--name", "--type", "--template-id", "--env", "--git-repo", "--startup-command", "--session-init-command", "--shutdown-policy", "--integration-provider-id"}, Example: "retask sandbox create --name my-sandbox --session-init-command 'claude --dangerously-skip-permissions \"$SEED_PROMPT\"'"},
			{Command: "retask sandbox update", Description: "Update a sandbox", Flags: []string{"--name"}, Example: "retask sandbox update <id> --name new-name"},
			{Command: "retask sandbox stop", Description: "Stop a running sandbox", Example: "retask sandbox stop <sandbox-id>"},
			{Command: "retask sandbox delete", Description: "Delete a sandbox", Example: "retask sandbox delete <sandbox-id>"},
			{Command: "retask sandbox session list", Description: "List sessions", Flags: []string{"--sandbox-id", "--status"}, Example: "retask sandbox session list --sandbox-id <sb-id>"},
			{Command: "retask sandbox session get", Description: "Get a session by ID", Example: "retask sandbox session get <session-id>"},
			{Command: "retask sandbox session create", Description: "Create a sandbox session", Flags: []string{"--sandbox-id", "--name"}, Example: "retask sandbox session create --sandbox-id <sb-id>"},
			{Command: "retask sandbox session update", Description: "Partial update a session", Flags: []string{"--name", "--seed-nrn", "--seed-prompt"}, Example: "retask sandbox session update <id> --name \"My Session\""},
			{Command: "retask sandbox session stop", Description: "Stop a session", Example: "retask sandbox session stop <session-id>"},
			{Command: "retask sandbox session delete", Description: "Delete a session", Example: "retask sandbox session delete <session-id>"},
			{Command: "retask sandbox connect", Description: "Connect this machine as a Private VM sandbox (long-running)", Flags: []string{"--mode", "--auto-open", "--no-auto-respond"}, Example: "retask sandbox connect <sandbox-id>"},
			{Command: "retask sandbox attach", Description: "Attach terminal to a running local session", Example: "retask sandbox attach <session-id>"},
			{Command: "retask agent list", Description: "List agents", Flags: []string{"--role"}, Example: "retask agent list --role ROLE_TASK_PROCESSOR"},
			{Command: "retask agent get", Description: "Get an agent by ID", Example: "retask agent get <agent-id>"},
			{Command: "retask agent create", Description: "Create an agent", Flags: []string{"--name", "--role", "--description", "--sandbox-template-id"}, Example: "retask agent create --name 'Task Bot' --role ROLE_TASK_PROCESSOR"},
			{Command: "retask agent update", Description: "Update an agent", Flags: []string{"--name", "--role", "--description", "--sandbox-template-id"}, Example: "retask agent update <id> --name 'New Name'"},
			{Command: "retask agent delete", Description: "Delete an agent", Example: "retask agent delete <agent-id>"},
			{Command: "retask upgrade", Description: "Upgrade retask to the latest version", Example: "retask upgrade"},
			{Command: "retask help-llm", Description: "Print machine-readable command manifest for LLM injection", Example: "retask help-llm"},
		},
	}
}
