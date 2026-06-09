# Workspace ID Propagation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Forward `--workspace-id` / `NWEB_WORKSPACE_ID` to list filters and create entity fields across all commands that support it in their proto definitions.

**Architecture:** All changes are one-liners in `RunE` closures — no new abstractions. List commands guard with `if gf.WorkspaceID != ""` before setting the filter field. Create commands add a local `--workspace-id` flag with `gf.WorkspaceID` as fallback, matching the existing pattern in `sandbox create`.

**Tech Stack:** Go, cobra, gRPC/protobuf

---

## Files

- Modify: `internal/cmd/project/command.go` — `newListCommand` (filter), `newCreateCommand` (entity + flag)
- Modify: `internal/cmd/task/command.go` — `newListCommand` (filter)
- Modify: `internal/cmd/agent/command.go` — `newListCommand` (filter), `newCreateCommand` (entity + flag)
- Modify: `internal/cmd/sandbox/command.go` — `newListCommand` (filter), `newSessionListCommand` (filter)

---

### Task 1: project list + project create

**Files:**
- Modify: `internal/cmd/project/command.go`

No unit tests exist for command RunE — verify with `go build`.

- [ ] **Step 1: Add workspace_id to project list filter**

In `newListCommand` (`internal/cmd/project/command.go`), the filter is built in two branches of an `if archived` block. Replace both filter struct literals to include `WorkspaceId`:

```go
// Replace the if/else block starting at ~line 87:
req := &projectv1.ProjectsRequest{}
if archived {
    req.Filter = &projectv1.ProjectsRequest_Filter{
        IsArchived:  commonv1.YesNo_Y,
        WorkspaceId: gf.WorkspaceID,
    }
} else {
    req.Filter = &projectv1.ProjectsRequest_Filter{
        IsArchived:  commonv1.YesNo_N,
        WorkspaceId: gf.WorkspaceID,
    }
}
```

(Setting an empty string is a no-op in proto3 — the server treats it as unset — so no guard is needed here.)

- [ ] **Step 2: Add --workspace-id flag to project create**

In `newCreateCommand`, make the following three changes:

1. Add `workspaceID` to the var block:
```go
var name, description, visibility, color, icon, workspaceID string
```

2. Resolve effective workspace ID and set it on the struct, just before `svc, close, err := connect(gf)`:
```go
wsID := workspaceID
if wsID == "" {
    wsID = gf.WorkspaceID
}

proj := &projectv1.Project{
    Name:        name,
    Description: description,
    Color:       color,
    Icon:        icon,
    WorkspaceId: wsID,
}
```

3. Register the flag at the bottom of `newCreateCommand`:
```go
cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace ID (overrides global flag and env var)")
```

Also update the `Long` description's Flags section to include:
```
  --workspace-id string  Optional. Workspace ID (overrides global --workspace-id flag)
```

- [ ] **Step 3: Build to verify**

```bash
go build ./...
```

Expected: no output (success).

- [ ] **Step 4: Verify project create help shows new flag**

```bash
./retask project create --help
```

Expected: `--workspace-id string` appears in the flags list.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/project/command.go
git commit -m "feat: propagate workspace_id in project list and project create"
```

---

### Task 2: task list

**Files:**
- Modify: `internal/cmd/task/command.go`

- [ ] **Step 1: Add workspace_id to task list filter**

In `newListCommand`, after the `priority` check and before `resp, err := svc.GetTasks(...)`, add:

```go
if gf.WorkspaceID != "" {
    filter.WorkspaceId = gf.WorkspaceID
}
```

The surrounding context for placement:
```go
if cmd.Flags().Changed("priority") {
    v, ok := taskv1.Task_Priority_value[priority]
    if !ok {
        return fmt.Errorf("invalid --priority %q. Valid values: UNKNOWN, LOW, MEDIUM, HIGH, URGENT", priority)
    }
    filter.Priority = taskv1.Task_Priority(v)
}

if gf.WorkspaceID != "" {          // ← add this block
    filter.WorkspaceId = gf.WorkspaceID
}

resp, err := svc.GetTasks(context.Background(), &taskv1.TasksRequest{
```

- [ ] **Step 2: Build to verify**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/task/command.go
git commit -m "feat: propagate workspace_id in task list"
```

---

### Task 3: agent list + agent create

**Files:**
- Modify: `internal/cmd/agent/command.go`

- [ ] **Step 1: Add workspace_id to agent list filter**

In `newListCommand`, after the `role` check and before `svc, close, err := connect(gf)`, add:

```go
if gf.WorkspaceID != "" {
    filter.WorkspaceId = gf.WorkspaceID
}
```

Surrounding context:
```go
if cmd.Flags().Changed("role") {
    r, err := parseRole(role)
    if err != nil {
        return err
    }
    filter.Roles = []agentv1.Agent_Role{r}
}

if gf.WorkspaceID != "" {          // ← add this block
    filter.WorkspaceId = gf.WorkspaceID
}

svc, close, err := connect(gf)
```

- [ ] **Step 2: Add --workspace-id flag to agent create**

In `newCreateCommand`, make the following three changes:

1. Add `workspaceID` to the var block:
```go
var name, role, description, sandboxTemplateID, workspaceID string
```

2. After resolving `r` from `parseRole` and before building the agent struct, add the wsID resolution. Then set `WorkspaceId` on the struct:
```go
wsID := workspaceID
if wsID == "" {
    wsID = gf.WorkspaceID
}

agent := &agentv1.Agent{
    Name:        name,
    Role:        r,
    Description: description,
    WorkspaceId: wsID,
}
```

3. Register the flag at the bottom of `newCreateCommand`:
```go
cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace ID (overrides global flag and env var)")
```

Also update the `Long` description's Flags section to include:
```
  --workspace-id string         Optional. Workspace ID (overrides global --workspace-id flag)
```

- [ ] **Step 3: Build to verify**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 4: Verify agent create help shows new flag**

```bash
./retask agent create --help
```

Expected: `--workspace-id string` appears in the flags list.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/agent/command.go
git commit -m "feat: propagate workspace_id in agent list and agent create"
```

---

### Task 4: sandbox list + sandbox session list

**Files:**
- Modify: `internal/cmd/sandbox/command.go`

- [ ] **Step 1: Add workspace_id to sandbox list filter**

In `newListCommand`, after the `type` check and before `svc, close, err := connect(gf)`, add:

```go
if gf.WorkspaceID != "" {
    filter.WorkspaceId = gf.WorkspaceID
}
```

Surrounding context:
```go
if cmd.Flags().Changed("type") {
    v, ok := sandboxv1.Sandbox_Type_value["TYPE_"+sandboxType]
    if !ok {
        return fmt.Errorf("invalid --type %q. Valid values: CLOUD, PRIVATE", sandboxType)
    }
    filter.Types = []sandboxv1.Sandbox_Type{sandboxv1.Sandbox_Type(v)}
}

if gf.WorkspaceID != "" {          // ← add this block
    filter.WorkspaceId = gf.WorkspaceID
}

svc, close, err := connect(gf)
```

- [ ] **Step 2: Add workspace_id to sandbox session list filter**

In `newSessionListCommand`, after the `status` check and before `svc, close, err := connect(gf)`, add:

```go
if gf.WorkspaceID != "" {
    filter.WorkspaceId = gf.WorkspaceID
}
```

Surrounding context:
```go
if cmd.Flags().Changed("status") {
    v, ok := sandboxv1.Session_Status_value["STATUS_"+status]
    if !ok {
        return fmt.Errorf("invalid --status %q. Valid values: ACTIVE, IDLE, TIMEOUT, STOPPED", status)
    }
    filter.Statuses = []sandboxv1.Session_Status{sandboxv1.Session_Status(v)}
}

if gf.WorkspaceID != "" {          // ← add this block
    filter.WorkspaceId = gf.WorkspaceID
}

svc, close, err := connect(gf)
```

- [ ] **Step 3: Build to verify**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/sandbox/command.go
git commit -m "feat: propagate workspace_id in sandbox list and session list"
```
