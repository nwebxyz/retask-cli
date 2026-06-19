package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Claims holds the subset of JWT payload fields used by the CLI.
type Claims struct {
	Sub         string `json:"sub"`
	WorkspaceID string `json:"nweb:workspace_id"`
	SourcePatID string `json:"nweb:source_pat_id"`
	Exp         int64  `json:"exp"`
}

// ExpiresAt returns the token expiry as a time.Time.
func (c Claims) ExpiresAt() time.Time {
	if c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0).UTC()
}

// ParseClaims decodes the payload segment of a JWT without verifying the signature.
func ParseClaims(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("malformed JWT: expected 3 segments, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("malformed JWT payload: %w", err)
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, fmt.Errorf("malformed JWT payload: %w", err)
	}
	return c, nil
}
