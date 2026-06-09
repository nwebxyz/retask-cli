// internal/auth/token_test.go
package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/config"
)

func TestDirectToken(t *testing.T) {
	t.Setenv("NWEB_API_TOKEN", "direct-jwt")
	r := &auth.Resolver{Profile: config.Profile{Endpoint: "localhost:443"}}
	tok, err := r.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "direct-jwt", tok)
}

func TestCachedJWTValid(t *testing.T) {
	t.Setenv("NWEB_API_TOKEN", "")
	r := &auth.Resolver{
		Profile: config.Profile{
			CachedJWT:    "cached-jwt",
			JWTExpiresAt: time.Now().Add(10 * time.Minute),
		},
	}
	tok, err := r.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cached-jwt", tok)
}

func TestCachedJWTExpired(t *testing.T) {
	t.Setenv("NWEB_API_TOKEN", "")
	t.Setenv("NWEB_API_KEY", "") // no PAT → should error
	r := &auth.Resolver{
		Profile: config.Profile{
			CachedJWT:    "expired-jwt",
			JWTExpiresAt: time.Now().Add(-1 * time.Minute),
		},
	}
	_, err := r.Token(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NWEB_API_KEY")
}

func TestMissingPAT(t *testing.T) {
	t.Setenv("NWEB_API_TOKEN", "")
	t.Setenv("NWEB_API_KEY", "")
	r := &auth.Resolver{Profile: config.Profile{}}
	_, err := r.Token(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NWEB_API_KEY")
}
