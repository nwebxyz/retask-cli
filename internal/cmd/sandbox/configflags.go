// internal/cmd/sandbox/configflags.go
package sandbox

import (
	"fmt"
	"strings"

	integrationv1 "github.com/nwebxyz/retask-cli/proto-gen/integration/v1"
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

// parseGitRepo parses a comma-separated key=value spec into a GitRepo.
// Required key: url. Optional keys: branch, dir (maps to target_dir).
// The url value may contain '@' and ':' (SSH URLs), which is why the spec is
// comma-delimited rather than positional. Example:
//
//	url=git@github.com:org/repo.git,branch=dev,dir=src
func parseGitRepo(s string) (*integrationv1.GitRepo, error) {
	repo := &integrationv1.GitRepo{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, found := strings.Cut(part, "=")
		if !found {
			return nil, fmt.Errorf("invalid --git-repo segment %q: expected key=value", part)
		}
		switch key {
		case "url":
			repo.Url = value
		case "branch":
			repo.Branch = value
		case "dir":
			repo.TargetDir = value
		default:
			return nil, fmt.Errorf("invalid --git-repo key %q: valid keys are url, branch, dir", key)
		}
	}
	if repo.Url == "" {
		return nil, fmt.Errorf("invalid --git-repo %q: url is required", s)
	}
	return repo, nil
}
