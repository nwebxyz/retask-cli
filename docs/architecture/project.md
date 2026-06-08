# Project Service — Architecture Design

> Proto package: `project.v1`
> Proto file: `proto/project/v1/project.proto`
> Events file: `proto/project/v1/event/event.proto`
> HTTP base: `/v1/projects`

## Overview

The Project service is a **global platform service** that manages projects as a universal namespace and access-control layer. Any application on the NWEB platform (retask, future apps) that needs to organise work under named, permissioned containers uses this service.

The service owns:
- Project CRUD (name, key, description, visibility, archiving, soft-delete)
- Project membership (explicit per-user roles: VIEWER / EDITOR / ADMIN)
- Permission resolution (`CheckProjectPermissions`)
- Workspace-member sync (implicit roles derived from workspace role + project visibility)

**The service deliberately owns nothing app-specific.** Task workflows, external integrations, and any other product-level config are owned by extension services that reference `project_id` as a foreign key.

## Why Global vs App-Specific Split

Projects are a shared primitive — every application needs a named, permissioned container scoped to a workspace. But each application has its own domain data that sits on top:

| Layer | Owner | Examples |
|---|---|---|
| Global project | `project/` service | name, key, members, visibility, permissions |
| Retask extension | `retask/project/` service | task statuses, task types, external project links |
| Future app extension | future service | app-specific config keyed by `project_id` |

Keeping app-specific data in separate extension services means:
- The global `project/v1/` proto has **zero imports from any app namespace** — a new app onboards without touching the global service
- Each extension service can evolve its schema independently
- The global service can be deployed and scaled independently of any single app

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                    FE / CLI / BE                     │
└───────────┬──────────────────────────┬───────────────┘
            │ user calls               │ retask-specific calls
            ▼                          ▼
┌───────────────────────┐   ┌──────────────────────────┐
│   project/ (global)   │   │  retask/project/          │
│                       │   │  (retask extension)       │
│  CRUD, members,       │   │                           │
│  permissions,         │   │  ProjectConfig            │
│  visibility           │   │  (task_statuses,          │
│                       │   │   task_types)             │
│  project/v1/          │   │                           │
│  zero retask imports  │   │  ExternalProject          │
│                       │   │  (GitHub, Linear links)   │
└──────────┬────────────┘   └──────────────┬────────────┘
           │                               │
           │ emits                         │ listens
           ▼                              │
┌───────────────────────┐                 │
│  project.v1.event     │◄────────────────┘
│  ProjectCreated       │
│  ProjectDeleted       │
└──────────┬────────────┘
           │
           ▼
  other services that
  cascade on project delete
  (retask/task, retask/sandbox, ...)
```

### Communication Rules

| From | To | Auth | Purpose |
|---|---|---|---|
| FE / CLI | `project/` | user token | project CRUD, member management |
| FE / CLI | `retask/project/` | user token | get/set task config, external project links |
| `project/` | `workspace/` | service account | resolve workspace member role for permission checks |
| `project/` | `quota/` | service account | check `totalProjectsPerWorkspace` on create |
| `retask/project/` | — | — | no outbound gRPC calls; event-driven only |
| `retask/task/` | `project/` | service account | `CheckProjectPermissions` |
| `retask/sandbox/` | `project/` | service account | `CheckProjectPermissions` |

### Event Flow

**Events consumed by `project/` (global):**
- `workspace.v1.event.WorkspaceMemberUpdated` → sync workspace member snapshot on project members
- `workspace.v1.event.WorkspaceMemberDeleted` → remove project members for that workspace member
- `workspace.v1.event.WorkspaceDeleted` → cascade soft-delete all projects in workspace, emit `ProjectDeleted` per project

**Events emitted by `project/` (global):**
- `project.v1.event.ProjectCreated` — on project creation
- `project.v1.event.ProjectDeleted` — on project deletion (direct or cascade from workspace)

**Events consumed by `retask/project/` (extension):**
- `project.v1.event.ProjectDeleted` → cascade soft-delete `ProjectConfig` + `ExternalProject`

## Entities

### Project (global)

```protobuf
message Project {
  string project_id   = 1;   // primary key
  string workspace_id = 2;
  string key          = 3;   // short unique slug per workspace
  string name         = 4;
  string description  = 5;
  string color        = 6;
  string icon         = 7;
  Visibility visibility = 8;
  bool is_archived      = 9;
  // audit + soft-delete + computed user_access
}

enum Visibility {
  VISIBILITY_WORKSPACE_EDIT = 0;  // all workspace members can edit
  VISIBILITY_WORKSPACE_VIEW = 1;  // all workspace members can view
  VISIBILITY_RESTRICTED     = 2;  // explicit members only
}
```

### MemberRole

```protobuf
enum MemberRole {
  MEMBER_ROLE_VIEWER = 0;
  MEMBER_ROLE_EDITOR = 1;
  MEMBER_ROLE_ADMIN  = 2;
}
```

Effective role = `max(implicit_role, explicit_role)`. Implicit role is derived from workspace role + project visibility (see `project/auth/`). Archived projects cap non-admin roles at VIEWER.

### ProjectConfig (retask extension)

1:1 with Project. Primary key is `project_id`. Stores retask-specific project configuration.

```protobuf
message ProjectConfig {
  string project_id                            = 1;   // primary key = project_id FK
  repeated retask.common.v1.TaskStatus task_statuses = 2;
  repeated retask.common.v1.TaskType   task_types    = 3;
  // audit + soft-delete
}
```

Soft-deleted by cascade when `project.v1.event.ProjectDeleted` is received.

### ExternalProject (retask extension)

Links a project to an external tracker (GitHub repo, Linear project, etc.). Many-to-one with Project.

```protobuf
message ExternalProject {
  string external_project_id = 1;
  string project_id          = 2;   // FK
  retask.common.v1.SourceType source_type = 3;
  string source_id  = 4;
  string source_url = 5;
  string name       = 6;
  string description  = 7;
  // sync timestamps, audit + soft-delete
}
```

Soft-deleted by cascade when `project.v1.event.ProjectDeleted` is received.

## RPC Surface

### `project/v1/` — Global ProjectService

| RPC | HTTP | Notes |
|---|---|---|
| `GetProjects` | `GET /v1/projects` | filter by workspace, archived state, owner |
| `GetProject` | `GET /v1/projects/{id}` | |
| `GetProjectByKey` | `GET /v1/projects:by-key` | workspace_id + key lookup |
| `SetProject` | `POST /v1/projects` | create or update |
| `ArchiveProject` | `POST /v1/projects/{id}:archive` | |
| `UnarchiveProject` | `POST /v1/projects/{id}:unarchive` | |
| `DeleteProject` | `DELETE /v1/projects/{id}` | soft-delete |
| `GetProjectMembers` | `GET /v1/projects/{project_id}/members` | |
| `SetProjectMember` | `POST /v1/projects/{project_id}/members` | upsert explicit role |
| `RemoveProjectMember` | `DELETE /v1/projects/{project_id}/members` | |
| `CheckProjectPermissions` | `POST /v1/projects:check-permissions` | batch role resolution |

### `retask/project/v1/` — RetaskProjectService

| RPC | HTTP | Notes |
|---|---|---|
| `GetProjectConfig` | `GET /retask/v1/projects/{id}/config` | returns ProjectConfig for project |
| `SetProjectConfig` | `POST /retask/v1/projects/{project_id}/config` | upsert; returns project_id |
| `GetExternalProjects` | `GET /retask/v1/projects/{project_id}/external-projects` | |
| `SetExternalProject` | `POST /retask/v1/external-projects` | |
| `DeleteExternalProject` | `DELETE /retask/v1/external-projects/{id}` | soft-delete |

## Access Control

Permission resolution uses a two-layer model:

1. **Implicit role** — derived from workspace role + project visibility:
   - Workspace OWNER/ADMIN → project ADMIN (all visibility modes)
   - Workspace EDITOR → project EDITOR if `VISIBILITY_WORKSPACE_EDIT`, VIEWER otherwise
   - Workspace VIEWER → project VIEWER if `VISIBILITY_WORKSPACE_EDIT` or `VISIBILITY_WORKSPACE_VIEW`
   - `VISIBILITY_RESTRICTED` → no implicit role for non-admin workspace members

2. **Explicit role** — stored `ProjectMember` record overrides implicit if higher

Effective role = `max(implicit, explicit)`. Archived projects cap non-admin effective roles at VIEWER.

Resolution logic lives in `project/auth/` (Go package, not a separate service). Previously at `retask/common/auth/`, moved to `project/auth/` when the service was promoted.

## Out of Scope

- Task management — owned by `retask/task/`
- External project sync scheduling — owned by the integration service
- App-specific project config beyond retask — future extension services add their own
