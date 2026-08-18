// internal/cmd/auth/command.go
package auth

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	"github.com/nwebxyz/retask-cli/internal/prompt"
	authv1 "github.com/nwebxyz/retask-cli/proto-gen/auth/v1"
	authv1connect "github.com/nwebxyz/retask-cli/proto-gen/auth/v1/authv1connect"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	workspacev1 "github.com/nwebxyz/retask-cli/proto-gen/workspace/v1"
	workspacev1connect "github.com/nwebxyz/retask-cli/proto-gen/workspace/v1/workspacev1connect"
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

If NWEB_API_KEY or NWEB_WORKSPACE_ID are not set, retask prompts for them
interactively (terminal only): it asks for your PAT (with a link to create
one), then — if no workspace is configured — exchanges a short-lived,
unsaved token to list your workspaces so you can pick one.

Usage example:
  retask auth login
  eval $(retask auth login --no-save)   # shared sandbox: session-scoped credentials

Environment:
  NWEB_API_KEY        PAT starting with "nweb_pat_...". Prompted for if empty and running in a terminal.
  NWEB_WORKSPACE_ID   Workspace to scope the JWT to. Prompted for (pick from a list) if empty and running in a terminal.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, _, err := loadProfile(gf)
			if err != nil {
				return err
			}

			needsExchange := os.Getenv("NWEB_API_TOKEN") == "" && !hasValidCachedJWT(profile)

			if needsExchange && os.Getenv("NWEB_API_KEY") == "" && prompt.IsInteractive() {
				pat, err := promptForPAT(os.Stderr)
				if err != nil {
					return err
				}
				os.Setenv("NWEB_API_KEY", pat)
			}

			if needsExchange && flags.ResolveWorkspaceID(gf.WorkspaceID, profile) == "" && prompt.IsInteractive() {
				if pat := os.Getenv("NWEB_API_KEY"); pat != "" {
					wsID, err := pickWorkspace(context.Background(), profile, pat, gf.Insecure)
					if err != nil {
						return err
					}
					gf.WorkspaceID = wsID
				}
			}

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

// hasValidCachedJWT reports whether the profile already has a JWT with more
// than 5 minutes of validity left — mirrors auth.Resolver.Token's own cache
// check (internal/auth/token.go), so login never prompts when it wouldn't
// have exchanged anyway.
func hasValidCachedJWT(p config.Profile) bool {
	return p.CachedJWT != "" && time.Now().Add(5*time.Minute).Before(p.JWTExpiresAt)
}

// promptForPAT asks the user for their PAT on stdin, masking the input.
func promptForPAT(out io.Writer) (pat string, err error) {
	fmt.Fprintln(out, "No NWEB_API_KEY set.")
	fmt.Fprintln(out, "Create a Personal Access Token: https://app.retask.work/access-tokens")
	fmt.Fprint(out, "Enter NWEB_API_KEY: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read NWEB_API_KEY: %w", err)
	}
	pat = strings.TrimSpace(string(raw))
	if pat == "" {
		return "", fmt.Errorf("NWEB_API_KEY is required")
	}
	return pat, nil
}

// pickWorkspace exchanges pat for a short-lived, unscoped token (never
// cached or persisted), uses it to list the caller's workspaces, and — if
// there's more than one — lets the user pick interactively.
func pickWorkspace(ctx context.Context, profile config.Profile, pat string, insecure bool) (workspaceID string, err error) {
	fmt.Fprintln(os.Stderr, "No NWEB_WORKSPACE_ID set. Looking up your workspaces...")
	tmpJWT, _, err := auth.ExchangePAT(ctx, profile.Endpoint, pat, "", insecure)
	if err != nil {
		return "", fmt.Errorf("list workspaces: %w", err)
	}

	httpClient := client.New(tmpJWT, insecure, false)
	baseURL := client.BaseURL(profile.Endpoint, insecure)
	wsSvc := workspacev1connect.NewWorkspaceServiceClient(httpClient, baseURL, connect.WithGRPC())
	resp, err := wsSvc.GetWorkspaces(ctx, connect.NewRequest(&workspacev1.WorkspacesRequest{}))
	if err != nil {
		return "", fmt.Errorf("list workspaces: %w", err)
	}

	workspaces := resp.Msg.GetWorkspaces()
	if len(workspaces) == 0 {
		return "", fmt.Errorf("no workspaces found for this account")
	}
	if len(workspaces) == 1 {
		fmt.Fprintf(os.Stderr, "Using workspace %q (%s)\n", workspaces[0].GetName(), workspaces[0].GetWorkspaceId())
		return workspaces[0].GetWorkspaceId(), nil
	}

	items := make([]prompt.Item, len(workspaces))
	for i, w := range workspaces {
		items[i] = prompt.Item{ID: w.GetWorkspaceId(), Name: w.GetName(), Detail: w.GetWorkspaceId()}
	}
	fmt.Fprintln(os.Stderr, "Select a workspace (up/down to move, enter to choose):")
	return prompt.SelectOne(os.Stderr, items)
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
		Short: "Print identity and workspace membership for the active token",
		Long: `Print identity and workspace membership for the active token.

Usage example:
  retask auth whoami

Output fields: user_nrn, workspace_id, jwt_expires, endpoint, workspace_member.{nrn, role, membership_status, display_name, name, email, joined_at}`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			profile, _, err := loadProfile(gf)
			if err != nil {
				return err
			}
			resolver, err := buildResolver(gf)
			if err != nil {
				return err
			}
			jwt, err := resolver.Token(ctx)
			if err != nil {
				return err
			}
			claims, err := auth.ParseClaims(jwt)
			if err != nil {
				return err
			}
			wsID := claims.WorkspaceID
			if wsID == "" {
				return fmt.Errorf("workspace_id not found in token")
			}

			httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
			baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
			wsSvc := workspacev1connect.NewWorkspaceServiceClient(httpClient, baseURL, client.Options(gf.Transport)...)

			resp, err := wsSvc.GetWorkspaceMembers(ctx, connect.NewRequest(&workspacev1.WorkspaceMembersRequest{
				WorkspaceId: wsID,
				UserNrns:    []string{claims.Sub},
			}))
			if err != nil {
				return err
			}

			jwtExpires := profile.JWTExpiresAt
			if jwtExpires.IsZero() {
				jwtExpires = claims.ExpiresAt()
			}

			out := whoamiOutput{
				UserNrn:     claims.Sub,
				WorkspaceID: wsID,
				JWTExpires:  jwtExpires.Format(time.RFC3339),
				Endpoint:    profile.Endpoint,
			}
			if len(resp.Msg.Members) > 0 {
				m := resp.Msg.Members[0]
				snap := memberSnapshot{
					Nrn:              "nweb:workspace:member:" + m.WorkspaceMemberId,
					Role:             workspacev1.WorkspaceMemberRole_name[int32(m.Role)],
					MembershipStatus: workspacev1.MembershipStatus_name[int32(m.MembershipStatus)],
					DisplayName:      m.DisplayName,
				}
				if p := m.MemberProfile; p != nil {
					snap.Name = p.Name
					snap.Email = p.Email
				}
				if m.JoinedAt != nil {
					snap.JoinedAt = m.JoinedAt.AsTime().Format(time.RFC3339)
				}
				out.WorkspaceMember = &snap
			}
			return output.Print(gf.Pretty, out)
		},
	}
}

type whoamiOutput struct {
	UserNrn         string          `json:"user_nrn"`
	WorkspaceID     string          `json:"workspace_id"`
	JWTExpires      string          `json:"jwt_expires"`
	Endpoint        string          `json:"endpoint"`
	WorkspaceMember *memberSnapshot `json:"workspace_member,omitempty"`
}

type memberSnapshot struct {
	Nrn              string `json:"nrn"`
	Role             string `json:"role"`
	MembershipStatus string `json:"membership_status"`
	DisplayName      string `json:"display_name,omitempty"`
	Name             string `json:"name,omitempty"`
	Email            string `json:"email,omitempty"`
	JoinedAt         string `json:"joined_at,omitempty"`
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
