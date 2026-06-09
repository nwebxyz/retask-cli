# Workspace ID Propagation Design

**Date:** 2026-06-09
**Status:** Approved

## Problem

Several commands accept `--workspace-id` / `NWEB_WORKSPACE_ID` via the global flags struct (`gf.WorkspaceID`) but never forward it to the gRPC request. The result is that list commands return cross-workspace results even when the operator has scoped their session to a specific workspace, and create commands silently omit `workspace_id` from the created entity.

## Scope

No changes to proto definitions, generated code, or the auth/client layers. All changes are in `internal/cmd/<service>/command.go` files only.

Commands with **no** workspace_id in their proto filter/entity (unchanged):
- `auth`, `workspace`, `customer`, `file`, `projectconfig`, `helpcmd`

## Design

### Approach

Apply workspace_id when non-empty — guard all usages with `if wsID != ""` (or `if gf.WorkspaceID != ""`). This matches the pattern already established by `sandbox create` and `task get-by-key` and avoids relying on undefined server behavior for empty-string inputs.

### List commands — forward `gf.WorkspaceID` to filter

When `gf.WorkspaceID` is non-empty, set the filter's `workspace_id` field. The filter struct is already initialized in all cases so this is a one-line addition per command.

| Command | Filter field |
|---|---|
| `project list` | `ProjectsRequest.Filter.WorkspaceId` |
| `task list` | `TasksRequest.Filter.WorkspaceId` |
| `agent list` | `AgentsRequest.Filter.WorkspaceId` |
| `sandbox list` | `SandboxesRequest.Filter.WorkspaceId` |
| `sandbox session list` | `SessionsRequest.Filter.WorkspaceId` |

Pattern:
```go
if gf.WorkspaceID != "" {
    filter.WorkspaceId = gf.WorkspaceID
}
```

For `project list`, the filter is nested inside the `if archived` block — workspace_id should be added in both branches.

### Create commands — local `--workspace-id` flag with global fallback

Add a local `--workspace-id` string flag to each create command. Resolve the effective workspace ID using:

```go
wsID := workspaceID          // local flag (empty if not provided)
if wsID == "" {
    wsID = gf.WorkspaceID    // global flag or NWEB_WORKSPACE_ID env
}
```

Set `wsID` on the entity field before calling `Set*`. No error if wsID is empty — the server determines default behavior in that case.

| Command | Entity field | Local flag |
|---|---|---|
| `project create` | `Project.WorkspaceId` | `--workspace-id` |
| `agent create` | `Agent.WorkspaceId` | `--workspace-id` |

`sandbox create` already implements this pattern — no change needed.

## Files Changed

- `internal/cmd/project/command.go` — `newListCommand`, `newCreateCommand`
- `internal/cmd/task/command.go` — `newListCommand`
- `internal/cmd/agent/command.go` — `newListCommand`, `newCreateCommand`
- `internal/cmd/sandbox/command.go` — `newListCommand`, `newSessionListCommand`

## Testing

No automated tests exist for command output. Verify by:
1. `go build ./...` passes with no errors
2. Manual smoke test with a valid `NWEB_WORKSPACE_ID` set — confirm list commands return workspace-scoped results
