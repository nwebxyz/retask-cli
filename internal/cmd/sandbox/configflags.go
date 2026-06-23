// internal/cmd/sandbox/configflags.go
package sandbox

import (
	"fmt"
	"strings"

	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// parseEnvVar parses a "KEY=VALUE" string into a plain (non-secret) env var.
// Only the first '=' separates key from value, so values may contain '='.
func parseEnvVar(s string) (*sandboxv1.Sandbox_Config_EnvVar, error) {
	key, value, found := strings.Cut(s, "=")
	if !found {
		return nil, fmt.Errorf("invalid --env %q: expected KEY=VALUE", s)
	}
	if key == "" {
		return nil, fmt.Errorf("invalid --env %q: empty key", s)
	}
	return &sandboxv1.Sandbox_Config_EnvVar{Key: key, Plain: value}, nil
}
