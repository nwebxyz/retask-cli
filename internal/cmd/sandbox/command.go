// internal/cmd/sandbox/command.go
package sandbox

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
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
	sandboxv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1/sandboxv1connect"
)

// NewCommand returns the top-level "sandbox" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes and sessions",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newCreateCommand(gf),
		newUpdateCommand(gf),
		newStopCommand(gf),
		newDeleteCommand(gf),
		newSessionCommand(gf),
		newConnectCommand(gf),
		newAttachCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a SandboxServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (sandboxv1connect.SandboxServiceClient, func(), error) {
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
	return sandboxv1connect.NewSandboxServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}

// ── sandbox list ──────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	var status, sandboxType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sandboxes",
		Long: `List sandboxes with optional filters.

Usage examples:
  retask sandbox list
  retask sandbox list --status READY
  retask sandbox list --type CLOUD

Flags:
  --status string   Filter by status: UNKNOWN, PROVISIONING, READY, RUNNING, STOPPED, ERROR, IDLE
  --type string     Filter by type: CLOUD, PRIVATE

Output fields: sandbox_id, workspace_id, name, type, status, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := &sandboxv1.SandboxesRequest_Filter{}

			if cmd.Flags().Changed("status") {
				v, ok := sandboxv1.Sandbox_Status_value["STATUS_"+status]
				if !ok {
					return fmt.Errorf("invalid --status %q. Valid values: UNKNOWN, PROVISIONING, READY, RUNNING, STOPPED, ERROR, IDLE", status)
				}
				filter.Statuses = []sandboxv1.Sandbox_Status{sandboxv1.Sandbox_Status(v)}
			}
			if cmd.Flags().Changed("type") {
				v, ok := sandboxv1.Sandbox_Type_value["TYPE_"+sandboxType]
				if !ok {
					return fmt.Errorf("invalid --type %q. Valid values: CLOUD, PRIVATE", sandboxType)
				}
				filter.Types = []sandboxv1.Sandbox_Type{sandboxv1.Sandbox_Type(v)}
			}

			if gf.WorkspaceID != "" {
				filter.WorkspaceId = gf.WorkspaceID
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			resp, err := svc.GetSandboxes(context.Background(), connectrpc.NewRequest(&sandboxv1.SandboxesRequest{
				Filter: filter,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Sandboxes)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by status: UNKNOWN, PROVISIONING, READY, RUNNING, STOPPED, ERROR, IDLE")
	cmd.Flags().StringVar(&sandboxType, "type", "", "Filter by type: CLOUD, PRIVATE")
	return cmd
}

// ── sandbox get ───────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a sandbox by ID",
		Long: `Fetch a single sandbox by its ID.

Usage example:
  retask sandbox get sandbox_abc123

Output fields: sandbox_id, workspace_id, name, type, status, config, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetSandbox(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

// ── sandbox create ────────────────────────────────────────────────────────────

func newCreateCommand(gf *flags.Global) *cobra.Command {
	var name, templateID, sandboxType string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new sandbox",
		Long: `Create a new sandbox.

Usage examples:
  retask sandbox create --name "My Sandbox"
  retask sandbox create --name "My Sandbox" --type PRIVATE
  retask sandbox create --name "My Sandbox" --template-id tmpl_abc123

Flags:
  --name string           Required. Sandbox name
  --type string           Optional. Sandbox type: CLOUD, PRIVATE (default CLOUD)
  --template-id string    Optional. Source template ID to fork config from

Output fields: sandbox_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			sandbox := &sandboxv1.Sandbox{
				Name:        name,
				WorkspaceId: gf.WorkspaceID,
			}
			if templateID != "" {
				sandbox.SourceTemplateId = templateID
			}
			if cmd.Flags().Changed("type") {
				v, ok := sandboxv1.Sandbox_Type_value["TYPE_"+sandboxType]
				if !ok {
					return fmt.Errorf("invalid --type %q. Valid values: CLOUD, PRIVATE", sandboxType)
				}
				sandbox.Type = sandboxv1.Sandbox_Type(v)
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetSandbox(context.Background(), connectrpc.NewRequest(sandbox))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"sandbox_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Sandbox name (required)")
	cmd.Flags().StringVar(&sandboxType, "type", "", "Sandbox type: CLOUD, PRIVATE (default CLOUD)")
	cmd.Flags().StringVar(&templateID, "template-id", "", "Source template ID to fork config from")
	return cmd
}

// ── sandbox update ────────────────────────────────────────────────────────────

func newUpdateCommand(gf *flags.Global) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an existing sandbox",
		Long: `Update fields of an existing sandbox using partial update (only changed fields are sent).

Usage examples:
  retask sandbox update sandbox_abc123 --name "New Name"

Flags:
  --name string   New sandbox name

Output fields: sandbox_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("name") {
				return fmt.Errorf("no fields to update: provide at least one flag")
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			// SetSandbox is full-replace: fetch first, apply changes, then set.
			existing, err := svc.GetSandbox(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("name") {
				existing.Msg.Name = name
			}
			resp, err := svc.SetSandbox(context.Background(), connectrpc.NewRequest(existing.Msg))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"sandbox_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New sandbox name")
	return cmd
}

// ── sandbox stop ──────────────────────────────────────────────────────────────

func newStopCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a sandbox",
		Long: `Stop a running sandbox by ID.

Usage example:
  retask sandbox stop sandbox_abc123

Output fields: status, sandbox_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.StopSandbox(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "stopped", "sandbox_id": args[0]})
		},
	}
}

// ── sandbox delete ────────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a sandbox",
		Long: `Soft-delete a sandbox by ID.

Usage example:
  retask sandbox delete sandbox_abc123

Output fields: status, sandbox_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteSandbox(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "sandbox_id": args[0]})
		},
	}
}

// ── sandbox session ───────────────────────────────────────────────────────────

func newSessionCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage sandbox sessions",
	}
	cmd.AddCommand(
		newSessionListCommand(gf),
		newSessionGetCommand(gf),
		newSessionCreateCommand(gf),
		newSessionUpdateCommand(gf),
		newSessionStopCommand(gf),
		newSessionDeleteCommand(gf),
	)
	return cmd
}

// ── sandbox session list ──────────────────────────────────────────────────────

func newSessionListCommand(gf *flags.Global) *cobra.Command {
	var sandboxID, status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions",
		Long: `List sandbox sessions with optional filters.

Usage examples:
  retask sandbox session list
  retask sandbox session list --sandbox-id sandbox_abc123
  retask sandbox session list --status ACTIVE

Flags:
  --sandbox-id string   Filter by sandbox ID
  --status string       Filter by status: ACTIVE, IDLE, TIMEOUT, STOPPED

Output fields: session_id, sandbox_id, workspace_id, name, status, mode, started_at, ended_at, created_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := &sandboxv1.SessionsRequest_Filter{}

			if cmd.Flags().Changed("sandbox-id") {
				filter.SandboxIds = []string{sandboxID}
			}
			if cmd.Flags().Changed("status") {
				v, ok := sandboxv1.Session_Status_value["STATUS_"+status]
				if !ok {
					return fmt.Errorf("invalid --status %q. Valid values: ACTIVE, IDLE, TIMEOUT, STOPPED", status)
				}
				filter.Statuses = []sandboxv1.Session_Status{sandboxv1.Session_Status(v)}
			}

			if gf.WorkspaceID != "" {
				filter.WorkspaceId = gf.WorkspaceID
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			resp, err := svc.GetSessions(context.Background(), connectrpc.NewRequest(&sandboxv1.SessionsRequest{
				Filter: filter,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Sessions)
		},
	}
	cmd.Flags().StringVar(&sandboxID, "sandbox-id", "", "Filter by sandbox ID")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status: ACTIVE, IDLE, TIMEOUT, STOPPED")
	return cmd
}

// ── sandbox session get ───────────────────────────────────────────────────────

func newSessionGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a session by ID",
		Long: `Fetch a single sandbox session by its ID.

Usage example:
  retask sandbox session get session_abc123

Output fields: session_id, sandbox_id, workspace_id, name, status, mode, started_at, ended_at, created_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetSession(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

// ── sandbox session create ────────────────────────────────────────────────────

func newSessionCreateCommand(gf *flags.Global) *cobra.Command {
	var sandboxID, name string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new session",
		Long: `Create a new sandbox session.

Usage examples:
  retask sandbox session create --sandbox-id sandbox_abc123
  retask sandbox session create --sandbox-id sandbox_abc123 --name "My Session"

Flags:
  --sandbox-id string   Required. Sandbox ID to start a session in
  --name string         Optional. Session name

Output fields: session_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sandboxID == "" {
				return fmt.Errorf("--sandbox-id is required")
			}

			req := &sandboxv1.NewSessionRequest{
				SandboxId: sandboxID,
				Name:      name,
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			resp, err := svc.NewSession(context.Background(), connectrpc.NewRequest(req))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"session_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&sandboxID, "sandbox-id", "", "Sandbox ID (required)")
	cmd.Flags().StringVar(&name, "name", "", "Session name")
	return cmd
}

// ── sandbox session update ────────────────────────────────────────────────────

func newSessionUpdateCommand(gf *flags.Global) *cobra.Command {
	var name, seedNRN, seedPrompt string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an existing session",
		Long: `Update fields of an existing session using partial update (only changed fields are sent).

Usage examples:
  retask sandbox session update session_abc123 --name "My Session"
  retask sandbox session update session_abc123 --seed-prompt "Focus on the auth module"
  retask sandbox session update session_abc123 --seed-nrn "nweb:retask-task:task:<uuid>"

Flags:
  --name string          New session name
  --seed-nrn string      Seed NRN (empty string clears it)
  --seed-prompt string   Seed prompt text

Output fields: session_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data := make(map[string]string)

			if cmd.Flags().Changed("name") {
				data["name"] = name
			}
			if cmd.Flags().Changed("seed-nrn") {
				data["seed_nrn"] = seedNRN
			}
			if cmd.Flags().Changed("seed-prompt") {
				data["seed_prompt"] = seedPrompt
			}

			if len(data) == 0 {
				return fmt.Errorf("no fields to update: provide at least one flag")
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetPartialSession(context.Background(), connectrpc.NewRequest(&commonv1.PartialData{
				Id:   args[0],
				Data: data,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"session_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New session name")
	cmd.Flags().StringVar(&seedNRN, "seed-nrn", "", "Seed NRN (empty string clears it)")
	cmd.Flags().StringVar(&seedPrompt, "seed-prompt", "", "Seed prompt text")
	return cmd
}

// ── sandbox session stop ──────────────────────────────────────────────────────

func newSessionStopCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a session",
		Long: `Stop a running sandbox session by ID.

Usage example:
  retask sandbox session stop session_abc123

Output fields: status, session_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.StopSession(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "stopped", "session_id": args[0]})
		},
	}
}

// ── sandbox session delete ────────────────────────────────────────────────────

func newSessionDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a session",
		Long: `Soft-delete a sandbox session by ID.

Usage example:
  retask sandbox session delete session_abc123

Output fields: status, session_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteSession(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "session_id": args[0]})
		},
	}
}
