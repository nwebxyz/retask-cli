# Workspace ID Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `flags.ResolveWorkspaceID` function that implements the flag → env → profile priority chain, and wire it into `PersistentPreRunE` so `gf.WorkspaceID` is fully resolved before any command runs.

**Architecture:** New pure function in the `flags` package takes the raw flag value and a loaded `config.Profile`, reads `NWEB_WORKSPACE_ID` from env, and returns the first non-empty value. `main.go`'s `PersistentPreRunE` loads config once, calls `ResolveWorkspaceID`, and stores the result back into `gf.WorkspaceID` — no changes to any command file.

**Tech Stack:** Go, cobra, `gopkg.in/yaml.v3` (via existing config package)

---

## Files

- Modify: `internal/flags/flags.go` — add `ResolveWorkspaceID`, add `os` and `config` imports
- Create: `internal/flags/flags_test.go` — unit tests for `ResolveWorkspaceID`
- Modify: `cmd/retask/main.go` — update `PersistentPreRunE`, add `config` import

---

### Task 1: `flags.ResolveWorkspaceID` — test + implement

**Files:**
- Modify: `internal/flags/flags.go`
- Create: `internal/flags/flags_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/flags/flags_test.go`:

```go
package flags_test

import (
	"testing"

	"nweb.xyz/retask-cli/internal/config"
	"nweb.xyz/retask-cli/internal/flags"
)

func TestResolveWorkspaceID_FlagWins(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "env-ws")
	profile := config.Profile{WorkspaceID: "profile-ws"}
	got := flags.ResolveWorkspaceID("flag-ws", profile)
	if got != "flag-ws" {
		t.Fatalf("expected flag-ws, got %q", got)
	}
}

func TestResolveWorkspaceID_EnvWinsOverProfile(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "env-ws")
	profile := config.Profile{WorkspaceID: "profile-ws"}
	got := flags.ResolveWorkspaceID("", profile)
	if got != "env-ws" {
		t.Fatalf("expected env-ws, got %q", got)
	}
}

func TestResolveWorkspaceID_ProfileFallback(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "")
	profile := config.Profile{WorkspaceID: "profile-ws"}
	got := flags.ResolveWorkspaceID("", profile)
	if got != "profile-ws" {
		t.Fatalf("expected profile-ws, got %q", got)
	}
}

func TestResolveWorkspaceID_AllEmpty(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "")
	profile := config.Profile{}
	got := flags.ResolveWorkspaceID("", profile)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/flags/...
```

Expected: compilation error — `flags.ResolveWorkspaceID` undefined.

- [ ] **Step 3: Implement `ResolveWorkspaceID`**

Replace the full contents of `internal/flags/flags.go` with:

```go
// internal/flags/flags.go
package flags

import (
	"os"

	"nweb.xyz/retask-cli/internal/config"
)

// Global holds persistent flags available on every command.
type Global struct {
	Profile     string
	WorkspaceID string
	Pretty      bool
	Insecure    bool
	NoSave      bool
	ConfigPath  string
}

// ResolveWorkspaceID returns the effective workspace ID using priority:
// flag value → NWEB_WORKSPACE_ID env var → profile workspace_id.
// Returns empty string if none of the sources provide a value.
func ResolveWorkspaceID(flag string, profile config.Profile) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv("NWEB_WORKSPACE_ID"); v != "" {
		return v
	}
	return profile.WorkspaceID
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/flags/...
```

Expected:
```
ok  	nweb.xyz/retask-cli/internal/flags
```

- [ ] **Step 5: Commit**

```bash
git add internal/flags/flags.go internal/flags/flags_test.go
git commit -m "feat: add flags.ResolveWorkspaceID with flag > env > profile priority"
```

---

### Task 2: Wire `ResolveWorkspaceID` into `PersistentPreRunE`

**Files:**
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Update `PersistentPreRunE`**

In `cmd/retask/main.go`, the current `PersistentPreRunE` block is:

```go
root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
    // Apply env overrides for flags not explicitly set
    if gf.Profile == "" {
        gf.Profile = os.Getenv("RETASK_PROFILE")
    }
    if gf.WorkspaceID == "" {
        gf.WorkspaceID = os.Getenv("NWEB_WORKSPACE_ID")
    }
    if os.Getenv("RETASK_NO_PERSIST") != "" {
        gf.NoSave = true
    }
    return nil
}
```

Replace it with:

```go
root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
    if gf.Profile == "" {
        gf.Profile = os.Getenv("RETASK_PROFILE")
    }
    if os.Getenv("RETASK_NO_PERSIST") != "" {
        gf.NoSave = true
    }

    // Resolve workspace ID: flag > env > profile
    configPath := gf.ConfigPath
    if configPath == "" {
        configPath = config.DefaultConfigPath()
    }
    if cfg, err := config.Load(configPath); err == nil {
        profile := cfg.ActiveProfileData(gf.Profile)
        gf.WorkspaceID = flags.ResolveWorkspaceID(gf.WorkspaceID, profile)
    } else {
        gf.WorkspaceID = flags.ResolveWorkspaceID(gf.WorkspaceID, config.Profile{})
    }

    return nil
}
```

- [ ] **Step 2: Add `config` import to `main.go`**

The imports block in `cmd/retask/main.go` currently is:

```go
import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	agentcmd "nweb.xyz/retask-cli/internal/cmd/agent"
	authcmd "nweb.xyz/retask-cli/internal/cmd/auth"
	customercmd "nweb.xyz/retask-cli/internal/cmd/customer"
	filecmd "nweb.xyz/retask-cli/internal/cmd/file"
	helpcmd "nweb.xyz/retask-cli/internal/cmd/helpcmd"
	integrationcmd "nweb.xyz/retask-cli/internal/cmd/integration"
	projectcmd "nweb.xyz/retask-cli/internal/cmd/project"
	projectconfigcmd "nweb.xyz/retask-cli/internal/cmd/projectconfig"
	sandboxcmd "nweb.xyz/retask-cli/internal/cmd/sandbox"
	taskcmd "nweb.xyz/retask-cli/internal/cmd/task"
	workspacecmd "nweb.xyz/retask-cli/internal/cmd/workspace"
	"nweb.xyz/retask-cli/internal/flags"
	"nweb.xyz/retask-cli/internal/version"
)
```

Replace with:

```go
import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	agentcmd "nweb.xyz/retask-cli/internal/cmd/agent"
	authcmd "nweb.xyz/retask-cli/internal/cmd/auth"
	customercmd "nweb.xyz/retask-cli/internal/cmd/customer"
	filecmd "nweb.xyz/retask-cli/internal/cmd/file"
	helpcmd "nweb.xyz/retask-cli/internal/cmd/helpcmd"
	integrationcmd "nweb.xyz/retask-cli/internal/cmd/integration"
	projectcmd "nweb.xyz/retask-cli/internal/cmd/project"
	projectconfigcmd "nweb.xyz/retask-cli/internal/cmd/projectconfig"
	sandboxcmd "nweb.xyz/retask-cli/internal/cmd/sandbox"
	taskcmd "nweb.xyz/retask-cli/internal/cmd/task"
	workspacecmd "nweb.xyz/retask-cli/internal/cmd/workspace"
	"nweb.xyz/retask-cli/internal/config"
	"nweb.xyz/retask-cli/internal/flags"
	"nweb.xyz/retask-cli/internal/version"
)
```

- [ ] **Step 3: Build and run full test suite**

```bash
go build ./... && go test ./...
```

Expected:
```
ok  	nweb.xyz/retask-cli/internal/auth
ok  	nweb.xyz/retask-cli/internal/config
ok  	nweb.xyz/retask-cli/internal/flags
ok  	nweb.xyz/retask-cli/internal/output
```

All other packages show `[no test files]` — that is expected.

- [ ] **Step 4: Commit**

```bash
git add cmd/retask/main.go
git commit -m "feat: resolve workspace ID from profile in PersistentPreRunE"
```
