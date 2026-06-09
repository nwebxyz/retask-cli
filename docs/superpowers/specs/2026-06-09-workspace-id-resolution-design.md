# Workspace ID Resolution Design

**Date:** 2026-06-09
**Status:** Approved

## Problem

`gf.WorkspaceID` is populated in `main.go`'s `PersistentPreRunE` using only flag → `NWEB_WORKSPACE_ID` env. The config profile's `workspace_id` field is never consulted for this purpose. As a result, list and create commands that depend on `gf.WorkspaceID` silently miss workspace scoping when the workspace ID is stored in the profile but not provided via flag or env.

The full intended priority chain is:
1. `--workspace-id` command flag
2. `NWEB_WORKSPACE_ID` environment variable
3. Config profile `workspace_id`

`auth.Resolver.resolveWorkspaceID()` already implements this chain internally, but it is only called during PAT exchange — not for filter/create use cases.

## Scope

Two files only. No changes to connect() functions, gRPC filter code, or proto definitions.

## Design

### New function: `flags.ResolveWorkspaceID`

```go
// internal/flags/flags.go
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

- Returns the highest-priority non-empty workspace ID from the three-tier chain.
- Returns empty string if none of the three sources provides a value — callers treat this as "no workspace scoping" (existing behaviour).
- No error return — this is a pure resolution function.
- Reads env directly so the resolution is always fresh (not dependent on when PersistentPreRunE ran relative to env changes).

Note: `flags` package must import `config` for `config.Profile`. This is a new import but not a cycle — `flags` does not currently import `config`, and `config` does not import `flags`.

### Update: `main.go` `PersistentPreRunE`

After the existing env overrides, load the config profile and call `ResolveWorkspaceID`:

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
        // Config missing or unreadable — fall back to flag/env only
        gf.WorkspaceID = flags.ResolveWorkspaceID(gf.WorkspaceID, config.Profile{})
    }

    return nil
}
```

Key points:
- The existing `if gf.WorkspaceID == "" { gf.WorkspaceID = os.Getenv("NWEB_WORKSPACE_ID") }` line is **removed** — `ResolveWorkspaceID` handles it.
- Config load errors are silently ignored: a missing `~/.config/retask/config.yaml` is a valid state (new user, CI environment).
- `gf.Profile` env override is applied before `cfg.ActiveProfileData(gf.Profile)` so the correct profile is selected.

## Files Changed

- `internal/flags/flags.go` — add `ResolveWorkspaceID`, add `config` and `os` imports
- `cmd/retask/main.go` — update `PersistentPreRunE`, add `config` import

## Testing

```bash
go test ./...           # existing tests must pass
go build ./...          # must compile cleanly
```

Functional verification: set `workspace_id` in `~/.config/retask/config.yaml` under the active profile, run `retask project list --pretty`, confirm it filters by that workspace without setting any flag or env var.
