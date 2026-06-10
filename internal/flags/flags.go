// internal/flags/flags.go
package flags

import (
	"os"

	"github.com/nwebxyz/retask-cli/internal/config"
)

// Global holds persistent flags available on every command.
type Global struct {
	Profile     string
	WorkspaceID string
	Pretty      bool
	Insecure    bool
	NoSave      bool
	ConfigPath  string
	Transport   string
	Verbose     bool
}

// ResolveWorkspaceID returns the effective workspace ID using priority:
// flag value → NWEB_WORKSPACE_ID env var → profile workspace_id.
func ResolveWorkspaceID(flag string, profile config.Profile) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv("NWEB_WORKSPACE_ID"); v != "" {
		return v
	}
	return profile.WorkspaceID
}
