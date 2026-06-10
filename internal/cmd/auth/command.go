// internal/cmd/auth/command.go
package auth

import (
	"context"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	authv1 "github.com/nwebxyz/retask-cli/proto-gen/auth/v1"
	authv1connect "github.com/nwebxyz/retask-cli/proto-gen/auth/v1/authv1connect"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication, tokens, and PATs",
	}
	cmd.AddCommand(
		newLoginCommand(gf),
		newLogoutCommand(gf),
		newWhoamiCommand(gf),
		newPatCommand(gf),
	)
	return cmd
}

func loadProfile(gf *flags.Global) (config.Profile, string, error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.Profile{}, path, err
	}
	return cfg.ActiveProfileData(gf.Profile), path, nil
}

func buildResolver(gf *flags.Global) (*auth.Resolver, error) {
	profile, cfgPath, err := loadProfile(gf)
	if err != nil {
		return nil, err
	}
	return auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, cfgPath, gf.NoSave, gf.Insecure), nil
}

func newLoginCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Exchange PAT for JWT and save to profile",
		Long: `Exchange a Personal Access Token (NWEB_API_KEY) for a JWT and save it to the active profile.

Usage example:
  retask auth login
  eval $(retask auth login --no-save)   # shared sandbox: session-scoped credentials

Environment:
  NWEB_API_KEY        Required. PAT starting with "nweb_pat_..."
  NWEB_WORKSPACE_ID   Required if not in profile or --workspace-id not set`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolver, err := buildResolver(gf)
			if err != nil {
				return err
			}
			jwt, err := resolver.Token(context.Background())
			if err != nil {
				return err
			}
			if gf.NoSave {
				wsID := gf.WorkspaceID
				if wsID == "" {
					wsID = os.Getenv("NWEB_WORKSPACE_ID")
				}
				fmt.Print(auth.ExportEnv(jwt, wsID))
				return nil
			}
			return output.Print(gf.Pretty, map[string]string{"status": "logged in"})
		},
	}
}

func newLogoutCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear cached JWT from active profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := gf.ConfigPath
			if path == "" {
				path = config.DefaultConfigPath()
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			p := cfg.ActiveProfileData(gf.Profile)
			p.CachedJWT = ""
			p.JWTExpiresAt = time.Time{}
			name := gf.Profile
			if name == "" {
				name = cfg.ActiveProfile
			}
			cfg.SetProfile(name, p)
			if err := cfg.Save(path); err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "logged out"})
		},
	}
}

func newWhoamiCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print current token claims (workspace, expiry)",
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, _, err := loadProfile(gf)
			if err != nil {
				return err
			}
			if profile.CachedJWT == "" && os.Getenv("NWEB_API_TOKEN") == "" {
				return fmt.Errorf("not logged in. Run: retask auth login")
			}
			return output.Print(gf.Pretty, map[string]any{
				"workspace_id": profile.WorkspaceID,
				"jwt_expires":  profile.JWTExpiresAt.Format(time.RFC3339),
				"endpoint":     profile.Endpoint,
			})
		},
	}
}

func newPatCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pat",
		Short: "Manage Personal Access Tokens",
	}
	cmd.AddCommand(newPatListCommand(gf), newPatCreateCommand(gf), newPatRevokeCommand(gf))
	return cmd
}

func newPatListCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List PATs for current user",
		Long: `List Personal Access Tokens for the authenticated user.

Usage example:
  retask auth pat list

Output fields: pat_id, name, masked_value, scopes, expires_at, last_used_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolver, err := buildResolver(gf)
			if err != nil {
				return err
			}
			jwt, err := resolver.Token(context.Background())
			if err != nil {
				return err
			}
			profile, _, _ := loadProfile(gf)
			httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
			baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
			resp, err := authv1connect.NewAuthServiceClient(httpClient, baseURL, client.Options(gf.Transport)...).GetPats(
				context.Background(), connect.NewRequest(&authv1.PatsRequest{}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Pats)
		},
	}
}

func newPatCreateCommand(gf *flags.Global) *cobra.Command {
	var name, description, expiresAt string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new PAT",
		Long: `Create a new Personal Access Token.

Usage example:
  retask auth pat create --name "ci-bot" --description "CI pipeline token"
  retask auth pat create --name "temp" --expires-at 2026-12-31T00:00:00Z

Flags:
  --name string         Required. Display name for the PAT
  --description string  Optional description
  --expires-at string   Optional expiry in RFC3339 (e.g. 2026-12-31T00:00:00Z). Absent = no expiry`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			resolver, err := buildResolver(gf)
			if err != nil {
				return err
			}
			jwt, err := resolver.Token(context.Background())
			if err != nil {
				return err
			}
			profile, _, _ := loadProfile(gf)

			req := &authv1.CreatePatRequest{
				Name:        name,
				Description: description,
				WorkspaceId: gf.WorkspaceID,
			}
			if expiresAt != "" {
				t, err := time.Parse(time.RFC3339, expiresAt)
				if err != nil {
					return fmt.Errorf("--expires-at must be RFC3339 (e.g. 2026-12-31T00:00:00Z): %w", err)
				}
				req.ExpiresAt = timestamppb.New(t)
			}
			httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
			baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
			resp, err := authv1connect.NewAuthServiceClient(httpClient, baseURL, client.Options(gf.Transport)...).CreatePat(
				context.Background(), connect.NewRequest(req))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]any{
				"pat":       resp.Msg.Pat,
				"raw_token": resp.Msg.RawToken,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "PAT display name (required)")
	cmd.Flags().StringVar(&description, "description", "", "PAT description")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "Expiry in RFC3339 (absent = no expiry)")
	return cmd
}

func newPatRevokeCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <pat-id>",
		Short: "Revoke a PAT by ID",
		Long: `Revoke (soft-delete) a Personal Access Token.

Usage example:
  retask auth pat revoke pat_abc123`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolver, err := buildResolver(gf)
			if err != nil {
				return err
			}
			jwt, err := resolver.Token(context.Background())
			if err != nil {
				return err
			}
			profile, _, _ := loadProfile(gf)
			httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
			baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
			_, err = authv1connect.NewAuthServiceClient(httpClient, baseURL, client.Options(gf.Transport)...).RevokePat(
				context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "revoked", "pat_id": args[0]})
		},
	}
}
