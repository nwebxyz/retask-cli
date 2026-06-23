# Custom config on `retask sandbox create` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional per-field flags to `retask sandbox create` so a human can configure a one-off sandbox inline (env vars, git repos, startup/session-init commands, shutdown policy, integration provider IDs) without first creating a template.

**Architecture:** Pure parsing/assembly helpers live in a new `internal/cmd/sandbox/configflags.go` (unit-tested in isolation). `newCreateCommand` in `command.go` wires the flags and calls `buildConfig`, which returns `nil` when no config flag is set (preserving today's bare-sandbox behavior) and errors when `--template-id` is combined with any config flag.

**Tech Stack:** Go, cobra (flags), connectrpc (transport), testify (tests), generated proto types under `proto-gen/`.

---

## Reference: exact generated types

- `sandboxv1.Sandbox_Config` — fields: `EnvVars []*Sandbox_Config_EnvVar`, `GitRepos []*integrationv1.GitRepo`, `StartupCommand string`, `SessionInitCommand string`, `IntegrationProviderIds []string`, `ShutdownPolicy Sandbox_Config_ShutdownPolicy`.
- `sandboxv1.Sandbox_Config_EnvVar` — fields used: `Key string`, `Plain string`.
- `sandboxv1.Sandbox_Config_ShutdownPolicy_value` — `map[string]int32` with keys `SHUTDOWN_POLICY_ON_IDLE_NO_USER_ACTIONS`, `SHUTDOWN_POLICY_ON_IDLE`, `SHUTDOWN_POLICY_NEVER`.
- `integrationv1.GitRepo` (import `integrationv1 "github.com/nwebxyz/retask-cli/proto-gen/integration/v1"`) — fields: `Url string`, `Branch string`, `TargetDir string`.

Run all package tests with: `go test ./internal/cmd/sandbox/...`

---

## Task 1: `parseEnvVar` helper

**Files:**
- Create: `internal/cmd/sandbox/configflags.go`
- Create: `internal/cmd/sandbox/configflags_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/sandbox/configflags_test.go`:

```go
package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEnvVar(t *testing.T) {
	t.Run("key and value", func(t *testing.T) {
		ev, err := parseEnvVar("FOO=bar")
		require.NoError(t, err)
		assert.Equal(t, "FOO", ev.Key)
		assert.Equal(t, "bar", ev.Plain)
	})
	t.Run("value contains equals (split on first only)", func(t *testing.T) {
		ev, err := parseEnvVar("FOO=a=b=c")
		require.NoError(t, err)
		assert.Equal(t, "FOO", ev.Key)
		assert.Equal(t, "a=b=c", ev.Plain)
	})
	t.Run("empty value is allowed", func(t *testing.T) {
		ev, err := parseEnvVar("FOO=")
		require.NoError(t, err)
		assert.Equal(t, "FOO", ev.Key)
		assert.Equal(t, "", ev.Plain)
	})
	t.Run("missing equals errors", func(t *testing.T) {
		_, err := parseEnvVar("FOO")
		require.Error(t, err)
	})
	t.Run("empty key errors", func(t *testing.T) {
		_, err := parseEnvVar("=bar")
		require.Error(t, err)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run TestParseEnvVar`
Expected: FAIL — `undefined: parseEnvVar`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/cmd/sandbox/configflags.go`:

```go
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
```

Note: `integrationv1` is imported now but first used in Task 2. If `go test` complains about an unused import before Task 2, temporarily add `var _ = integrationv1.GitRepo{}` — but the recommended path is to implement Task 1 and Task 2 back-to-back so the import is used. To keep Task 1 green on its own, omit the `integrationv1` import line here and add it in Task 2.

Use this Task-1-only version of the import block:

```go
import (
	"fmt"
	"strings"

	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run TestParseEnvVar`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/sandbox/configflags.go internal/cmd/sandbox/configflags_test.go
git commit -m "feat(sandbox): add parseEnvVar helper for create config flags

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `parseGitRepo` helper

**Files:**
- Modify: `internal/cmd/sandbox/configflags.go`
- Modify: `internal/cmd/sandbox/configflags_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/sandbox/configflags_test.go`:

```go
func TestParseGitRepo(t *testing.T) {
	t.Run("url only", func(t *testing.T) {
		r, err := parseGitRepo("url=https://github.com/org/repo")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo", r.Url)
		assert.Equal(t, "", r.Branch)
		assert.Equal(t, "", r.TargetDir)
	})
	t.Run("url branch dir", func(t *testing.T) {
		r, err := parseGitRepo("url=https://github.com/org/repo,branch=dev,dir=src")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo", r.Url)
		assert.Equal(t, "dev", r.Branch)
		assert.Equal(t, "src", r.TargetDir)
	})
	t.Run("ssh url with @ and colon", func(t *testing.T) {
		r, err := parseGitRepo("url=git@github.com:org/repo.git,branch=main")
		require.NoError(t, err)
		assert.Equal(t, "git@github.com:org/repo.git", r.Url)
		assert.Equal(t, "main", r.Branch)
	})
	t.Run("order independent", func(t *testing.T) {
		r, err := parseGitRepo("branch=dev,url=https://github.com/org/repo")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo", r.Url)
		assert.Equal(t, "dev", r.Branch)
	})
	t.Run("missing url errors", func(t *testing.T) {
		_, err := parseGitRepo("branch=dev")
		require.Error(t, err)
	})
	t.Run("unknown key errors", func(t *testing.T) {
		_, err := parseGitRepo("url=https://x,depth=1")
		require.Error(t, err)
	})
	t.Run("segment without equals errors", func(t *testing.T) {
		_, err := parseGitRepo("url=https://x,oops")
		require.Error(t, err)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run TestParseGitRepo`
Expected: FAIL — `undefined: parseGitRepo`.

- [ ] **Step 3: Write minimal implementation**

In `internal/cmd/sandbox/configflags.go`, change the import block to add `integrationv1`:

```go
import (
	"fmt"
	"strings"

	integrationv1 "github.com/nwebxyz/retask-cli/proto-gen/integration/v1"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)
```

Then append the function:

```go
// parseGitRepo parses a comma-separated key=value spec into a GitRepo.
// Required key: url. Optional keys: branch, dir (maps to target_dir).
// The url value may contain '@' and ':' (SSH URLs), which is why the spec is
// comma-delimited rather than positional. Example:
//   url=git@github.com:org/repo.git,branch=dev,dir=src
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run 'TestParseEnvVar|TestParseGitRepo'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/sandbox/configflags.go internal/cmd/sandbox/configflags_test.go
git commit -m "feat(sandbox): add parseGitRepo helper (SSH-safe key=value spec)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `parseShutdownPolicy` helper

**Files:**
- Modify: `internal/cmd/sandbox/configflags.go`
- Modify: `internal/cmd/sandbox/configflags_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/sandbox/configflags_test.go`:

```go
func TestParseShutdownPolicy(t *testing.T) {
	t.Run("valid values", func(t *testing.T) {
		cases := map[string]int32{
			"ON_IDLE_NO_USER_ACTIONS": 0,
			"ON_IDLE":                 1,
			"NEVER":                   2,
		}
		for in, want := range cases {
			p, err := parseShutdownPolicy(in)
			require.NoError(t, err, in)
			assert.Equal(t, want, int32(p), in)
		}
	})
	t.Run("invalid value errors", func(t *testing.T) {
		_, err := parseShutdownPolicy("SOMETIMES")
		require.Error(t, err)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run TestParseShutdownPolicy`
Expected: FAIL — `undefined: parseShutdownPolicy`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/cmd/sandbox/configflags.go`:

```go
// parseShutdownPolicy resolves a short policy name (e.g. "ON_IDLE") to the
// proto enum value (looked up as SHUTDOWN_POLICY_<name>).
func parseShutdownPolicy(s string) (sandboxv1.Sandbox_Config_ShutdownPolicy, error) {
	v, ok := sandboxv1.Sandbox_Config_ShutdownPolicy_value["SHUTDOWN_POLICY_"+s]
	if !ok {
		return 0, fmt.Errorf("invalid --shutdown-policy %q. Valid values: ON_IDLE_NO_USER_ACTIONS, ON_IDLE, NEVER", s)
	}
	return sandboxv1.Sandbox_Config_ShutdownPolicy(v), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run TestParseShutdownPolicy`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/sandbox/configflags.go internal/cmd/sandbox/configflags_test.go
git commit -m "feat(sandbox): add parseShutdownPolicy helper

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `buildConfig` assembler (with `--template-id` conflict)

**Files:**
- Modify: `internal/cmd/sandbox/configflags.go`
- Modify: `internal/cmd/sandbox/configflags_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/sandbox/configflags_test.go`:

```go
func TestBuildConfig(t *testing.T) {
	t.Run("nil when nothing set", func(t *testing.T) {
		cfg, err := buildConfig("", nil, nil, "", "", "", nil)
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})
	t.Run("builds config from flags", func(t *testing.T) {
		cfg, err := buildConfig(
			"",
			[]string{"FOO=bar"},
			[]string{"url=https://github.com/org/repo,branch=dev"},
			"echo startup",
			"echo init",
			"ON_IDLE",
			[]string{"github", "slack"},
		)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Len(t, cfg.EnvVars, 1)
		assert.Equal(t, "FOO", cfg.EnvVars[0].Key)
		assert.Equal(t, "bar", cfg.EnvVars[0].Plain)
		require.Len(t, cfg.GitRepos, 1)
		assert.Equal(t, "https://github.com/org/repo", cfg.GitRepos[0].Url)
		assert.Equal(t, "dev", cfg.GitRepos[0].Branch)
		assert.Equal(t, "echo startup", cfg.StartupCommand)
		assert.Equal(t, "echo init", cfg.SessionInitCommand)
		assert.Equal(t, int32(1), int32(cfg.ShutdownPolicy)) // ON_IDLE
		assert.Equal(t, []string{"github", "slack"}, cfg.IntegrationProviderIds)
	})
	t.Run("template id with config flag errors", func(t *testing.T) {
		_, err := buildConfig("tmpl_abc", []string{"FOO=bar"}, nil, "", "", "", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--template-id")
	})
	t.Run("template id alone is fine (no config)", func(t *testing.T) {
		cfg, err := buildConfig("tmpl_abc", nil, nil, "", "", "", nil)
		require.NoError(t, err)
		assert.Nil(t, cfg)
	})
	t.Run("propagates field parse errors", func(t *testing.T) {
		_, err := buildConfig("", []string{"NOEQUALS"}, nil, "", "", "", nil)
		require.Error(t, err)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/sandbox/ -run TestBuildConfig`
Expected: FAIL — `undefined: buildConfig`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/cmd/sandbox/configflags.go`:

```go
// buildConfig assembles a *Sandbox_Config from the create flags. It returns
// (nil, nil) when no config flag was set, so callers leave Sandbox.Config empty
// and preserve the bare-sandbox behavior.
//
// A config flag is "set" when its value is non-zero (non-empty slice or
// non-empty string). When templateID is non-empty, it is mutually exclusive
// with every config flag: the server forks the template's config on create, so
// config must be empty.
func buildConfig(
	templateID string,
	env, gitRepos []string,
	startupCmd, sessionInitCmd, shutdownPolicy string,
	integrationIDs []string,
) (*sandboxv1.Sandbox_Config, error) {
	hasConfig := len(env) > 0 || len(gitRepos) > 0 || startupCmd != "" ||
		sessionInitCmd != "" || shutdownPolicy != "" || len(integrationIDs) > 0
	if !hasConfig {
		return nil, nil
	}
	if templateID != "" {
		return nil, fmt.Errorf("cannot combine --template-id with config flags " +
			"(--env, --git-repo, --startup-command, --session-init-command, " +
			"--shutdown-policy, --integration-provider-id); the template's config is forked instead")
	}

	cfg := &sandboxv1.Sandbox_Config{}
	for _, e := range env {
		ev, err := parseEnvVar(e)
		if err != nil {
			return nil, err
		}
		cfg.EnvVars = append(cfg.EnvVars, ev)
	}
	for _, g := range gitRepos {
		repo, err := parseGitRepo(g)
		if err != nil {
			return nil, err
		}
		cfg.GitRepos = append(cfg.GitRepos, repo)
	}
	cfg.StartupCommand = startupCmd
	cfg.SessionInitCommand = sessionInitCmd
	if shutdownPolicy != "" {
		p, err := parseShutdownPolicy(shutdownPolicy)
		if err != nil {
			return nil, err
		}
		cfg.ShutdownPolicy = p
	}
	cfg.IntegrationProviderIds = integrationIDs
	return cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/sandbox/ -run TestBuildConfig`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/sandbox/configflags.go internal/cmd/sandbox/configflags_test.go
git commit -m "feat(sandbox): add buildConfig assembler with template-id conflict check

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Wire flags into `sandbox create`

**Files:**
- Modify: `internal/cmd/sandbox/command.go` (`newCreateCommand`, lines ~153-207)

- [ ] **Step 1: Replace `newCreateCommand` with the flag-wired version**

Replace the entire `newCreateCommand` function in `internal/cmd/sandbox/command.go` with:

```go
func newCreateCommand(gf *flags.Global) *cobra.Command {
	var name, templateID, sandboxType string
	var env, gitRepos, integrationIDs []string
	var startupCmd, sessionInitCmd, shutdownPolicy string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new sandbox",
		Long: `Create a new sandbox.

Usage examples:
  retask sandbox create --name "My Sandbox"
  retask sandbox create --name "My Sandbox" --type PRIVATE
  retask sandbox create --name "My Sandbox" --template-id tmpl_abc123
  retask sandbox create --name "My Sandbox" --env KEY=VALUE --git-repo url=https://github.com/org/repo
  retask sandbox create --name "My Sandbox" --session-init-command 'claude --append-system-prompt "$SYSTEM_PROMPT" --dangerously-skip-permissions'

Flags:
  --name string                      Required. Sandbox name
  --type string                      Optional. Sandbox type: CLOUD, PRIVATE (default CLOUD)
  --template-id string               Optional. Source template ID to fork config from.
                                     Mutually exclusive with the config flags below.
  --env KEY=VALUE                    Optional, repeatable. Plain env var (value may contain '=').
  --git-repo url=...[,branch=...][,dir=...]
                                     Optional, repeatable. Repo cloned at session start.
  --startup-command string           Optional. Command run at sandbox startup.
  --session-init-command string      Optional. Command run at each session start.
  --shutdown-policy string           Optional. Values: ON_IDLE_NO_USER_ACTIONS, ON_IDLE, NEVER
  --integration-provider-id string   Optional, repeatable/comma-separated. Integration provider ID.

Output fields: sandbox_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			sandbox := &sandboxv1.Sandbox{
				Name:        name,
				WorkspaceId: gf.WorkspaceID,
			}
			if templateID != "" {
				sandbox.SourceTemplateId = templateID
			}
			if cmd.Flags().Changed("type") {
				v, ok := sandboxv1.Sandbox_Type_value["TYPE_"+sandboxType]
				if !ok {
					return fmt.Errorf("invalid --type %q. Valid values: CLOUD, PRIVATE", sandboxType)
				}
				sandbox.Type = sandboxv1.Sandbox_Type(v)
			}

			cfg, err := buildConfig(templateID, env, gitRepos, startupCmd, sessionInitCmd, shutdownPolicy, integrationIDs)
			if err != nil {
				return err
			}
			sandbox.Config = cfg

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.SetSandbox(context.Background(), connectrpc.NewRequest(sandbox))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"sandbox_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Sandbox name (required)")
	cmd.Flags().StringVar(&sandboxType, "type", "", "Sandbox type: CLOUD, PRIVATE (default CLOUD)")
	cmd.Flags().StringVar(&templateID, "template-id", "", "Source template ID to fork config from (mutually exclusive with config flags)")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Plain env var KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&gitRepos, "git-repo", nil, "Git repo url=...[,branch=...][,dir=...] (repeatable)")
	cmd.Flags().StringVar(&startupCmd, "startup-command", "", "Command run at sandbox startup")
	cmd.Flags().StringVar(&sessionInitCmd, "session-init-command", "", "Command run at each session start")
	cmd.Flags().StringVar(&shutdownPolicy, "shutdown-policy", "", "Shutdown policy: ON_IDLE_NO_USER_ACTIONS, ON_IDLE, NEVER")
	cmd.Flags().StringSliceVar(&integrationIDs, "integration-provider-id", nil, "Integration provider ID (repeatable or comma-separated)")
	return cmd
}
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 3: Smoke-test the help output**

Run: `go run ./cmd/retask sandbox create --help`
Expected: help text lists `--env`, `--git-repo`, `--startup-command`, `--session-init-command`, `--shutdown-policy`, `--integration-provider-id`, and shows the `--session-init-command 'claude --append-system-prompt ...'` example.

- [ ] **Step 4: Smoke-test the conflict error (no network needed — fails before connect)**

Run: `go run ./cmd/retask sandbox create --name x --template-id tmpl_abc --env FOO=bar`
Expected: error containing `cannot combine --template-id with config flags`. (The error returns before any network call.)

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/cmd/sandbox/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/sandbox/command.go
git commit -m "feat(sandbox): support custom config flags on sandbox create

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Update the help-llm manifest

**Files:**
- Modify: `internal/cmd/helpcmd/command.go:125`

- [ ] **Step 1: Replace the `retask sandbox create` manifest line**

Find this line (currently `internal/cmd/helpcmd/command.go:125`):

```go
		{Command: "retask sandbox create", Description: "Create a sandbox", Flags: []string{"--name", "--type", "--template-id"}, Example: "retask sandbox create --name my-sandbox --type CLOUD"},
```

Replace it with:

```go
		{Command: "retask sandbox create", Description: "Create a sandbox. Custom config via --env/--git-repo/--startup-command/--session-init-command/--shutdown-policy/--integration-provider-id, all mutually exclusive with --template-id", Flags: []string{"--name", "--type", "--template-id", "--env", "--git-repo", "--startup-command", "--session-init-command", "--shutdown-policy", "--integration-provider-id"}, Example: "retask sandbox create --name my-sandbox --session-init-command 'claude --append-system-prompt \"$SYSTEM_PROMPT\" --dangerously-skip-permissions'"},
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 3: Verify the manifest renders the new flags and example**

Run: `go run ./cmd/retask help-llm`
Expected: the `retask sandbox create` entry lists the six new flags and shows the `--session-init-command 'claude --append-system-prompt "$SYSTEM_PROMPT" --dangerously-skip-permissions'` example.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/helpcmd/command.go
git commit -m "fix(help-llm): document sandbox create custom config flags

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Vet**

Run: `go vet ./...`
Expected: no output (success).

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 3: Full build**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 4: Confirm no stray changes**

Run: `git status`
Expected: clean working tree (all task commits made).

---

## Self-review notes (already reconciled)

- **Spec coverage:** every Config field in scope (env vars, git repos, startup/session-init commands, shutdown policy, integration provider IDs) maps to a flag in Task 5 and an assembler branch in Task 4. Deferred fields (secrets, system_prompt, passthrough_credentials) are intentionally absent.
- **`--template-id` conflict:** enforced and unit-tested in `buildConfig` (Task 4), surfaced before any network call (Task 5 Step 4).
- **help-llm example** includes the requested `--session-init-command 'claude --append-system-prompt "$SYSTEM_PROMPT" --dangerously-skip-permissions'` (Task 6).
- **Type consistency:** `buildConfig` signature is identical in Task 4 (definition) and Task 5 (call site): `buildConfig(templateID string, env, gitRepos []string, startupCmd, sessionInitCmd, shutdownPolicy string, integrationIDs []string)`.
