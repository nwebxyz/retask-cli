// internal/config/profile_test.go
package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nweb.xyz/retask-cli/internal/config"
)

func TestLoadMissing(t *testing.T) {
	cfg, err := config.Load("/tmp/retask-test-nonexistent-abc123.yaml")
	require.NoError(t, err)
	p := cfg.ActiveProfileData("")
	assert.Equal(t, "api.nweb.app:443", p.Endpoint)
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	cfg := &config.Config{
		ActiveProfile: "default",
		Profiles: map[string]config.Profile{
			"default": {
				Endpoint:     "api.nweb.app:443",
				WorkspaceID:  "ws_abc",
				CachedJWT:    "tok123",
				JWTExpiresAt: exp,
			},
		},
	}
	require.NoError(t, cfg.Save(path))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	p := loaded.ActiveProfileData("default")
	assert.Equal(t, "ws_abc", p.WorkspaceID)
	assert.Equal(t, "tok123", p.CachedJWT)
	assert.Equal(t, exp, p.JWTExpiresAt)
}

func TestFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
	require.NoError(t, cfg.Save(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
