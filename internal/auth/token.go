// internal/auth/token.go
package auth

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	authv1 "github.com/nwebxyz/retask-cli/proto-gen/auth/v1"
	authv1connect "github.com/nwebxyz/retask-cli/proto-gen/auth/v1/authv1connect"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
)

// Resolver resolves a valid JWT for API calls.
type Resolver struct {
	Profile     config.Profile
	ProfileName string
	WorkspaceID string // flag override
	NoPersist   bool
	ConfigPath  string
	Insecure    bool

	// OnTokenRefreshed is called after a successful PAT exchange.
	OnTokenRefreshed func(jwt string, expiresAt time.Time, workspaceID string)
}

// Token returns a valid JWT, exchanging the PAT if necessary.
// PAT is never stored; it is always read fresh from NWEB_API_KEY.
func (r *Resolver) Token(ctx context.Context) (string, error) {
	// 1. Ready-to-use JWT from env
	if tok := os.Getenv("NWEB_API_TOKEN"); tok != "" {
		return tok, nil
	}

	// 2. Cached JWT still valid (more than 5 min remaining)
	if r.Profile.CachedJWT != "" && time.Now().Add(5*time.Minute).Before(r.Profile.JWTExpiresAt) {
		return r.Profile.CachedJWT, nil
	}

	// 3. Need PAT to exchange
	pat := os.Getenv("NWEB_API_KEY")
	if pat == "" {
		return "", fmt.Errorf("NWEB_API_KEY is required (set your PAT). Run: retask auth login")
	}

	// 4. Resolve workspace_id
	wsID, err := r.resolveWorkspaceID()
	if err != nil {
		return "", err
	}

	// 5. Exchange PAT for JWT
	jwt, expiresAt, err := r.exchangePAT(ctx, pat, wsID)
	if err != nil {
		return "", fmt.Errorf("PAT exchange failed: %w", err)
	}

	// 6. Persist (unless --no-save)
	if r.OnTokenRefreshed != nil {
		r.OnTokenRefreshed(jwt, expiresAt, wsID)
	}

	return jwt, nil
}

func (r *Resolver) resolveWorkspaceID() (string, error) {
	// Priority: flag > env > profile > prompt (TTY only)
	if r.WorkspaceID != "" {
		return r.WorkspaceID, nil
	}
	if v := os.Getenv("NWEB_WORKSPACE_ID"); v != "" {
		return v, nil
	}
	if r.Profile.WorkspaceID != "" {
		return r.Profile.WorkspaceID, nil
	}

	// Interactive prompt only on TTY
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return "", fmt.Errorf(
			"workspace ID required. Set NWEB_WORKSPACE_ID, use --workspace-id, or run: retask auth login",
		)
	}

	fmt.Fprint(os.Stderr, "Enter workspace ID: ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		if id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("workspace ID is required")
}

func (r *Resolver) exchangePAT(ctx context.Context, pat, workspaceID string) (string, time.Time, error) {
	// PAT exchange never carries a JWT header — the PAT is in the request body.
	// Always use gRPC protocol regardless of NWEB_API_TRANSPORT (internal auth call).
	httpClient := client.New("", r.Insecure)
	baseURL := client.BaseURL(r.Profile.Endpoint, r.Insecure)
	authClient := authv1connect.NewAuthServiceClient(httpClient, baseURL, connect.WithGRPC())
	resp, err := authClient.ExchangePat(ctx, connect.NewRequest(&authv1.PatExchangeRequest{
		Token:       pat,
		WorkspaceId: workspaceID,
	}))
	if err != nil {
		return "", time.Time{}, err
	}
	var expiresAt time.Time
	if resp.Msg.ExpiresAt != nil {
		expiresAt = resp.Msg.ExpiresAt.AsTime()
	}
	return resp.Msg.Jwt, expiresAt, nil
}

// ExportEnv returns shell export lines for --no-save mode.
func ExportEnv(jwt, workspaceID string) string {
	return fmt.Sprintf(
		"export NWEB_API_TOKEN=%q\nexport NWEB_WORKSPACE_ID=%q\n# To apply: eval $(retask auth login --no-save)\n",
		jwt, workspaceID,
	)
}

// NewResolver builds a Resolver from global flags and a loaded profile.
func NewResolver(profile config.Profile, profileName, workspaceIDFlag, configPath string, noPersist, insecureFlag bool) *Resolver {
	r := &Resolver{
		Profile:     profile,
		ProfileName: profileName,
		WorkspaceID: workspaceIDFlag,
		NoPersist:   noPersist,
		ConfigPath:  configPath,
		Insecure:    insecureFlag,
	}
	if !noPersist {
		r.OnTokenRefreshed = func(jwt string, expiresAt time.Time, wsID string) {
			cfg, err := config.Load(configPath)
			if err != nil {
				return
			}
			name := cfg.ResolveProfileName(profileName)
			p := cfg.ActiveProfileData(name)
			p.CachedJWT = jwt
			p.JWTExpiresAt = expiresAt
			if wsID != "" {
				p.WorkspaceID = wsID
			}
			cfg.SetProfile(name, p)
			_ = cfg.Save(configPath)
		}
	}
	return r
}

// BearerToken formats a JWT as a Bearer auth header value.
func BearerToken(jwt string) string {
	return "Bearer " + jwt
}

// GetPATOnce returns NWEB_API_KEY without storing it anywhere.
// Only for auth commands that explicitly manage PATs.
func GetPATOnce() string {
	return os.Getenv("NWEB_API_KEY")
}

// EmptyProto returns a commonv1.Empty message (convenience).
func EmptyProto() *commonv1.Empty {
	return &commonv1.Empty{}
}
