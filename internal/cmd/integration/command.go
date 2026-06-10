// internal/cmd/integration/command.go
package integration

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
	integrationv1 "github.com/nwebxyz/retask-cli/proto-gen/integration/v1"
	integrationv1connect "github.com/nwebxyz/retask-cli/proto-gen/integration/v1/integrationv1connect"
)

// NewCommand returns the top-level "integration" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Manage integrations and providers",
	}
	cmd.AddCommand(
		newProviderCommand(gf),
		newListCommand(gf),
		newGetCommand(gf),
		newSetCommand(gf),
		newDeleteCommand(gf),
		newGithubCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns an IntegrationServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (integrationv1connect.IntegrationServiceClient, func(), error) {
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
	return integrationv1connect.NewIntegrationServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}

// ── integration provider ──────────────────────────────────────────────────────

func newProviderCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage integration providers",
	}
	cmd.AddCommand(
		newProviderListCommand(gf),
		newProviderGetCommand(gf),
	)
	return cmd
}

func newProviderListCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List integration providers",
		Long: `List all available integration providers.

Usage example:
  retask integration provider list
  retask integration provider list --pretty

Output fields: provider_id, name, logo, disable_oauth_flow, disable_access_token, oauth_authorize_url, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetProviders(context.Background(), connectrpc.NewRequest(&integrationv1.ProvidersRequest{}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Providers)
		},
	}
}

func newProviderGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <provider-id>",
		Short: "Get an integration provider by ID",
		Long: `Fetch a single integration provider by its string ID (e.g. "github", "anthropic").

Usage example:
  retask integration provider get github

Output fields: provider_id, name, logo, disable_oauth_flow, disable_access_token, oauth_authorize_url, sandbox_env_vars, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetProvider(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

// ── integration list ──────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	var providerID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List integrations",
		Long: `List integrations for the current workspace.

Usage examples:
  retask integration list
  retask integration list --provider-id github
  retask integration list --pretty

Flags:
  --provider-id string  Filter by provider ID (e.g. "github")

Output fields: integration_id, workspace_id, provider_id, level, owner_member_id, access_level, external_account, expires_at, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			filter := &integrationv1.IntegrationsRequest_Filter{
				WorkspaceId: gf.WorkspaceID,
			}
			if providerID != "" {
				filter.ProviderIds = []string{providerID}
			}

			resp, err := svc.GetIntegrations(context.Background(), connectrpc.NewRequest(&integrationv1.IntegrationsRequest{
				Filter: filter,
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Integrations)
		},
	}
	cmd.Flags().StringVar(&providerID, "provider-id", "", "Filter by provider ID (e.g. \"github\")")
	return cmd
}

// ── integration get ───────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <integration-id>",
		Short: "Get an integration by ID",
		Long: `Fetch a single integration by its ID.

Usage example:
  retask integration get intg_abc123

Output fields: integration_id, workspace_id, provider_id, level, owner_member_id, access_level, external_account, expires_at, credentials, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetIntegration(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

// ── integration set ───────────────────────────────────────────────────────────

func newSetCommand(gf *flags.Global) *cobra.Command {
	var providerID, levelStr, accessToken string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Create or update an integration",
		Long: `Create or update an integration for the current workspace.

Uniqueness is on (workspace_id, provider_id, level, owner_member_id). A second
call for the same tuple replaces the existing row and rotates the secrets.

Usage examples:
  retask integration set --provider-id github --level LEVEL_MEMBER --access-token ghp_xxx
  retask integration set --provider-id anthropic --level LEVEL_WORKSPACE --access-token sk-ant-xxx

Flags:
  --provider-id string   Required. Provider ID (e.g. "github", "anthropic")
  --level string         Required. Integration level: LEVEL_WORKSPACE, LEVEL_MEMBER
  --access-token string  Required. Access token for the provider

Output fields: id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if providerID == "" {
				return fmt.Errorf("--provider-id is required")
			}
			if levelStr == "" {
				return fmt.Errorf("--level is required")
			}
			if accessToken == "" {
				return fmt.Errorf("--access-token is required")
			}

			levelVal, ok := integrationv1.Integration_Level_value[levelStr]
			if !ok {
				return fmt.Errorf("invalid --level %q. Valid values: LEVEL_WORKSPACE, LEVEL_MEMBER", levelStr)
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			resp, err := svc.SetIntegration(context.Background(), connectrpc.NewRequest(&integrationv1.Integration{
				WorkspaceId: gf.WorkspaceID,
				ProviderId:  providerID,
				Level:       integrationv1.Integration_Level(levelVal),
				Credentials: &integrationv1.Integration_Credentials{
					AccessToken: accessToken,
				},
			}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&providerID, "provider-id", "", "Provider ID (e.g. \"github\") (required)")
	cmd.Flags().StringVar(&levelStr, "level", "", "Integration level: LEVEL_WORKSPACE, LEVEL_MEMBER (required)")
	cmd.Flags().StringVar(&accessToken, "access-token", "", "Access token for the provider (required)")
	return cmd
}

// ── integration delete ────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <integration-id>",
		Short: "Hard-delete an integration",
		Long: `Hard-delete an integration by ID. This removes the integration row and
deletes the underlying token secrets from secret-manager.

Usage example:
  retask integration delete intg_abc123

Output fields: status, integration_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteIntegration(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "integration_id": args[0]})
		},
	}
}

// ── integration github ────────────────────────────────────────────────────────

func newGithubCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "GitHub-specific integration commands",
	}
	cmd.AddCommand(newGithubReposCommand(gf))
	return cmd
}

func newGithubReposCommand(gf *flags.Global) *cobra.Command {
	var levelStr string
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "List GitHub repositories accessible via the caller's integration",
		Long: `Resolve the caller's GitHub integration and return the repository catalog.
If --level is set, uses that integration exactly; otherwise defaults to
MEMBER-first with WORKSPACE fallback.

Usage examples:
  retask integration github repos
  retask integration github repos --level LEVEL_WORKSPACE
  retask integration github repos --pretty

Flags:
  --level string  Optional. Integration level to use: LEVEL_WORKSPACE, LEVEL_MEMBER

Output fields: name, clone_url, default_branch, private`,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &integrationv1.GithubReposRequest{
				WorkspaceId: gf.WorkspaceID,
			}

			if cmd.Flags().Changed("level") {
				levelVal, ok := integrationv1.Integration_Level_value[levelStr]
				if !ok {
					return fmt.Errorf("invalid --level %q. Valid values: LEVEL_WORKSPACE, LEVEL_MEMBER", levelStr)
				}
				req.Level = integrationv1.Integration_Level(levelVal)
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			resp, err := svc.GetGithubRepos(context.Background(), connectrpc.NewRequest(req))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Repos)
		},
	}
	cmd.Flags().StringVar(&levelStr, "level", "", "Integration level: LEVEL_WORKSPACE, LEVEL_MEMBER")
	return cmd
}
