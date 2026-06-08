# Retask Sandbox Service — Architecture Design

> Proto package: `retask.sandbox.v1`
> Proto file: `proto/retask/sandbox/v1/sandbox.proto`
> Events file: `proto/retask/sandbox/v1/event/event.proto`

## Overview

The Sandbox Service manages cloud and private sandbox VMs for vibe-coding with AI agents (Claude Code, Codex, etc.). It supports multiple concurrent sessions per sandbox, configurable environments via templates, and secure secret injection via the [Secret Manager service](./secret-manager.md).

This service is **pure CRUD with write-path enrichment** — it stores entity metadata and, on write, calls `secret-manager` to create/delete secrets. It does not manage VM lifecycle directly. A separate **Sandbox Proxy** service handles WebSocket connections, PTY streaming, VM provisioning, and session status reporting.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                          FE (xterm UI)                      │
│                                                             │
│  REST/gRPC calls              WebSocket (PTY stream)        │
│       │                              │                      │
└───────┼──────────────────────────────┼──────────────────────┘
        │                              │
        ▼                              ▼
┌──────────────────┐          ┌──────────────────┐
│  Sandbox Service │◄─────────│  Sandbox Proxy   │
│  (retask/sandbox)│  gRPC    │                  │
│                  │  (BE-BE) │  Manages VMs,    │
│  Pure CRUD +     │          │  PTYs, WebSocket │
│  secret-manager  │          │  connections     │
│  + auth-service  │          │                  │
│  calls (write    │          │                  │
│  path & runtime) │          │                  │
└────────┬─────────┘          └───────┬──────────┘
         │                            │
         │ gRPC (BE-BE)               │ gRPC (BE-BE)
         ▼                            ▼
┌──────────────────┐          ┌──────────────────┐
│ Secret Manager   │          │ Task Service     │
│ (secret/v1)      │          │ (retask/task/v1) │
│                  │          │                  │
│ Global service,  │          │ Proxy calls      │
│ stores encrypted │          │ GetTask for      │
│ values           │          │ session context  │
└──────────────────┘          └──────────────────┘

┌──────────────────┐
│ Auth Service     │
│                  │
│ Issues one-time  │
│ short-lived      │
│ WebSocket tokens │
└──────────────────┘
```

### Communication Rules

| From | To | Auth | Purpose |
|---|---|---|---|
| FE | Sandbox Service | user token | CRUD for sandboxes, sessions, templates |
| FE | Sandbox Service | user token | ConnectSession → returns WebSocket URL with one-time token |
| FE | Sandbox Proxy | one-time token (query param) | PTY streaming via WebSocket |
| Proxy | Sandbox Service | user token (forwarded) | GetSandbox, GetSession, GetSessions |
| Proxy | Sandbox Service | service token | ReportSessionStatus, GetSessionRuntime |
| Proxy | Task Service | service token | GetTask (when a session's `seed_nrn` references a task) |
| Sandbox Service | Auth Service | service token | Issue one-time short-lived WebSocket token |
| Sandbox Service | Secret Manager | service token | SetSecret, GetSecret, DeleteSecret, CopySecret (write path) + AccessSecrets (runtime resolution for GetSessionRuntime) |

### Session Connection Flow

The FE cannot send custom headers when opening a WebSocket (`new WebSocket(url)` has no header support). Instead, `ConnectSession` returns a fully-qualified WebSocket URL with a one-time, short-lived token embedded as a query parameter.

```
FE calls ConnectSession(session_id)
  → Sandbox Service verifies session access (sharing rules)
  → Sandbox Service resolves connection URL based on sandbox type:
      - TYPE_CLOUD:   proxy-managed URL
      - TYPE_PRIVATE: user-supplied URL from Sandbox.PrivateConnection
  → Sandbox Service calls Auth Service (BE-BE, service token)
      to issue a one-time short-lived token scoped to this session
  → Returns SessionConnectionResponse:
      - url:        wss://host/path?token=<one-time-token>
      - expires_at: token expiry timestamp
  → FE opens WebSocket before expires_at: new WebSocket(response.url)
  → Proxy validates the token on connection (one-time use, rejects replays)
```

### Communication Model

No pubsub between sandbox ↔ proxy ↔ secret-manager for runtime operations. All runtime communication is synchronous gRPC. The proxy reports session state changes to the sandbox service via `ReportSessionStatus` (event-driven on status transitions, not on a timer).

Pubsub events (`SandboxStatusChanged`, `SessionStatusChanged`) are published by the sandbox service on state transitions and consumed by other services. Cascade deletion also uses pubsub (see [Events & Cascades](#events--cascades)).

## Entities

### Sandbox

A sandbox is a VM (cloud or user-supplied) owned by a single user. Default sharing is PRIVATE — but the owner can choose to share via the `workspace.v1.Sharing` component.

```protobuf
message Sandbox {
  enum Type {
    TYPE_CLOUD   = 0;   // managed VM provisioned by the platform
    TYPE_PRIVATE = 1;   // user-supplied host (local machine, own VM)
  }

  enum Status {
    STATUS_UNKNOWN      = 0;   // just created, VM never started
    STATUS_PROVISIONING = 1;   // cloud VM spinning up (cloud only; private skips this)
    STATUS_READY        = 2;   // VM healthy, no active sessions
    STATUS_RUNNING      = 3;   // VM healthy, has active sessions
    STATUS_STOPPED      = 4;   // VM shut down — explicitly stopped by user via StopSandbox
    STATUS_ERROR        = 5;   // VM unhealthy
    STATUS_IDLE         = 6;   // VM removed — auto-shutdown triggered by session timeout policy
  }

  message MachineType {
    string machine_type_id = 1;
    string name            = 2;
  }

  message Config {
    enum ShutdownPolicy {
      SHUTDOWN_POLICY_ON_IDLE_NO_USER_ACTIONS = 0;   // default — shut down when all sessions timeout and none have need_user_action=YES
      SHUTDOWN_POLICY_ON_IDLE                 = 1;   // shut down when all sessions timeout, regardless of user actions
      SHUTDOWN_POLICY_NEVER                   = 2;   // never auto-shutdown
    }

    message EnvVar {
      message SecretValue {
        // transient: populated by FE on write, stripped before DB storage (bson:"-"),
        // re-injected by proxy at runtime via secret-manager.AccessSecrets
        string          value        = 1;
        .common.v1.Nrn secret_nrn   = 2;   // populated by sandbox svc after SetSecret
        string          masked_value = 3;   // cached for display
      }
      string key         = 1;
      string description = 2;
      // Exactly one of `plain` or `secret` is set. Modelled as two optional fields
      // (not a oneof) so the record round-trips through BSON without custom codecs.
      string      plain  = 3;
      SecretValue secret = 4;
    }

    message SystemPrompt {
      // When true, the platform-injected default system prompt is not applied.
      bool   skip_default = 1;
      string extra        = 2;   // appended after the default prompt, or used alone when skip_default=true
    }

    repeated EnvVar      env_vars                 = 1;
    repeated GitRepo     git_repos                = 2;   // all entries cloned at session start
    string               startup_command          = 3;
    string               session_init_command     = 4;
    SystemPrompt         system_prompt            = 5;
    bool                 passthrough_credentials  = 6;
    repeated string      integration_provider_ids = 7;
    ShutdownPolicy       shutdown_policy          = 8;
  }

  message PrivateConnection {
    message Header {
      string key   = 1;
      string value = 2;
    }
    string          url     = 1;
    repeated Header headers = 2;
  }

  message Report {
    uint32                    total_session_count        = 1;
    uint32                    running_session_count      = 2;
    uint32                    total_runtime_seconds      = 3;
    string                    latest_session_id          = 4;
    google.protobuf.Timestamp latest_session_started_at = 5;
    google.protobuf.Timestamp latest_session_active_at  = 6;
  }

  string                    sandbox_id         = 1;
  string                    workspace_id       = 2;
  .common.v1.Nrn            owner_nrn          = 3;
  string                    name               = 4;
  Type                      type               = 5;
  Status                    status             = 6;
  MachineType               machine_type       = 7;
  Config                    config             = 8;
  string                    source_template_id = 9;
  PrivateConnection         private_connection = 10;
  Report                    report             = 11;
  .workspace.v1.Sharing     sharing            = 12;
  // Audit
  .common.v1.Nrn            created_by_nrn     = 13;
  google.protobuf.Timestamp created_at         = 14;
  .common.v1.Nrn            updated_by_nrn     = 15;
  google.protobuf.Timestamp updated_at         = 16;
  // Soft delete
  bool                      is_deleted         = 17;
  .common.v1.DeletionInfo   deletion_info      = 18;
}
```

**Sandbox status is derived from session states** — never explicitly reported by the proxy. The sandbox service recomputes status on every `ReportSessionStatus` call:

| Sessions state | Derived sandbox status |
|---|---|
| Any session `ACTIVE` | `STATUS_RUNNING` |
| No `ACTIVE` sessions, at least one `IDLE` | `STATUS_READY` |
| All sessions `TIMEOUT` | Evaluate shutdown policy (see [VM Shutdown Logic](#vm-shutdown-logic-on-timeout)) |
| No sessions | `STATUS_READY` |

### Session

A session is a terminal shell inside a sandbox. Created via `NewSession`. Runtime state (status, message content, insights) is updated by the proxy via `ReportSessionStatus`.

```protobuf
message Session {
  enum Status {
    STATUS_ACTIVE  = 0;   // PTY is open and active
    STATUS_IDLE    = 1;   // no recent activity (proxy-defined short window)
    STATUS_TIMEOUT = 2;   // no activity for 15 minutes
    STATUS_STOPPED = 3;   // explicitly stopped by user
  }

  enum Mode {
    MODE_SCRATCH                = 0;
    MODE_MANUAL_TASK_PLANNING   = 1;
    MODE_MANUAL_TASK_PROCESSING = 2;
    MODE_AUTO_TASK_PLANNING     = 3;
    MODE_AUTO_TASK_PROCESSING   = 4;
  }

  message Insights {
    enum NeedUserAction {
      NEED_USER_ACTION_UNKNOWN = 0;
      NEED_USER_ACTION_YES     = 1;
      NEED_USER_ACTION_NO      = 2;
    }
    message ActionItem {
      string          question    = 1;
      repeated string suggestions = 2;   // short action labels the user can tap to send as reply
    }
    NeedUserAction       need_user_action = 1;
    repeated ActionItem  action_items     = 2;   // populated when need_user_action = YES
  }

  // Identity
  string         session_id   = 1;
  string         workspace_id = 2;
  string         sandbox_id   = 3;
  string         agent_id     = 4;   // optional; when set, session is agent-driven
  .common.v1.Nrn owner_nrn    = 5;

  // Core metadata
  string                name        = 6;
  Status                status      = 7;
  Mode                  mode        = 8;
  .common.v1.Nrn        seed_nrn    = 9;    // optional anchor (task, project, etc.)
  string                seed_prompt = 10;   // optional; write-once on NewSession
  .workspace.v1.Sharing sharing     = 11;

  // Runtime state — updated by proxy via ReportSessionStatus
  string                    last_user_message      = 12;
  google.protobuf.Timestamp last_user_message_at   = 13;
  string                    last_system_message    = 14;
  google.protobuf.Timestamp last_system_message_at = 15;
  google.protobuf.Timestamp last_active_at         = 16;

  // LLM-extracted insights — populated async after IDLE transition
  Insights insights = 17;

  // Lifecycle
  google.protobuf.Timestamp started_at = 18;
  google.protobuf.Timestamp ended_at   = 19;

  // Audit
  .common.v1.Nrn            created_by_nrn = 20;
  google.protobuf.Timestamp created_at     = 21;
  .common.v1.Nrn            updated_by_nrn = 22;
  google.protobuf.Timestamp updated_at     = 23;

  // Soft delete
  bool                    is_deleted    = 24;
  .common.v1.DeletionInfo deletion_info = 25;
}
```

### SandboxTemplate

A reusable configuration blueprint for sandboxes. Users pick a template when creating a sandbox and choose to use the config as-is or override it. References `Sandbox.Config` for the configuration shape (including `shutdown_policy`).

```protobuf
message SandboxTemplate {
  string                    sandbox_template_id = 1;
  string                    workspace_id        = 2;
  .common.v1.Nrn            owner_nrn           = 3;
  string                    name                = 4;
  string                    description         = 5;
  Sandbox.Config            config              = 6;
  .workspace.v1.Sharing     sharing             = 7;
  .common.v1.Nrn            scope_nrn           = 8;   // NRN of scoped resource (e.g. agent NRN); unset = standalone
  // Audit
  .common.v1.Nrn            created_by_nrn      = 9;
  google.protobuf.Timestamp created_at          = 10;
  .common.v1.Nrn            updated_by_nrn      = 11;
  google.protobuf.Timestamp updated_at          = 12;
  // Soft delete
  bool                      is_deleted          = 13;
  .common.v1.DeletionInfo   deletion_info       = 14;
}
```

### SandboxActivity

An append-only activity log for a sandbox. Written internally by the sandbox service — users can only read. Stored in its own collection (not embedded in Sandbox) because it grows unbounded.

```protobuf
message SandboxActivity {
  enum Type {
    TYPE_UNKNOWN                  = 0;
    TYPE_CREATED                  = 1;
    TYPE_FORKED_TEMPLATE          = 2;
    TYPE_CONFIG_UPDATED           = 3;
    TYPE_TEMPLATE_SYNCED          = 4;   // cascade from SandboxTemplateConfigChanged
    TYPE_STATUS_CHANGED           = 5;
    TYPE_SESSION_STARTED          = 6;
    TYPE_SESSION_RUNTIME_RESOLVED = 7;   // proxy fetched runtime via GetSessionRuntime
    TYPE_SESSION_STOPPED          = 8;
  }

  string                    sandbox_activity_id = 1;
  string                    sandbox_id          = 2;
  Type                      type                = 3;
  string                    description         = 4;
  map<string, string>       metadata            = 5;   // type-specific data (session_id, old_status, etc.)
  .common.v1.Nrn            actor_nrn           = 6;
  google.protobuf.Timestamp occurred_at         = 7;
}
```

## RPC Surface

### Service Definition

```protobuf
service SandboxService {
  // === Sandbox CRUD ===
  rpc GetSandboxes(SandboxesRequest) returns (SandboxesResponse) {
    option (google.api.http) = {get: "/retask/v1/sandboxes"};
  }
  rpc GetSandbox(.common.v1.Id) returns (Sandbox) {
    option (google.api.http) = {get: "/retask/v1/sandboxes/{id}"};
  }
  rpc SetSandbox(Sandbox) returns (.common.v1.Id) {
    option (google.api.http) = {post: "/retask/v1/sandboxes" body: "*"};
  }
  rpc StopSandbox(.common.v1.Id) returns (.common.v1.Empty) {
    option (google.api.http) = {post: "/retask/v1/sandboxes/{id}:stop"};
  }
  rpc DeleteSandbox(.common.v1.Id) returns (.common.v1.Empty) {
    option (google.api.http) = {delete: "/retask/v1/sandboxes/{id}"};
  }

  // === Session ===
  rpc GetSessions(SessionsRequest) returns (SessionsResponse) {
    option (google.api.http) = {get: "/retask/v1/sessions"};
  }
  rpc GetSession(.common.v1.Id) returns (Session) {
    option (google.api.http) = {get: "/retask/v1/sessions/{id}"};
  }
  rpc NewSession(NewSessionRequest) returns (.common.v1.Id) {
    option (google.api.http) = {post: "/retask/v1/sessions" body: "*"};
  }
  rpc SetPartialSession(.common.v1.PartialData) returns (.common.v1.Id) {
    option (google.api.http) = {patch: "/retask/v1/sessions/{id}" body: "*"};
  }
  rpc ConnectSession(.common.v1.Id) returns (SessionConnectionResponse) {
    option (google.api.http) = {post: "/retask/v1/sessions/{id}:connect"};
  }
  // BE-BE only (service-token auth). Used by sandbox proxy to fetch the
  // materialized runtime view — Session + Sandbox with secret plaintext
  // re-injected into Config.EnvVar.SecretValue.value.
  rpc GetSessionRuntime(.common.v1.Id) returns (SessionRuntime) {
    option (google.api.http) = {get: "/retask/v1/sessions/{id}:runtime"};
  }
  rpc StopSession(.common.v1.Id) returns (.common.v1.Empty) {
    option (google.api.http) = {post: "/retask/v1/sessions/{id}:stop"};
  }
  rpc DeleteSession(.common.v1.Id) returns (.common.v1.Empty) {
    option (google.api.http) = {delete: "/retask/v1/sessions/{id}"};
  }
  // BE-BE only (service-token auth). Called by proxy on any session status
  // transition. Triggers async LLM extraction on IDLE and VM shutdown
  // evaluation on TIMEOUT.
  rpc ReportSessionStatus(ReportSessionStatusRequest) returns (.common.v1.Empty) {
    option (google.api.http) = {post: "/retask/v1/sessions/{session_id}:report-status" body: "*"};
  }

  // === SandboxTemplate CRUD ===
  rpc GetSandboxTemplates(SandboxTemplatesRequest) returns (SandboxTemplatesResponse) {
    option (google.api.http) = {get: "/retask/v1/sandbox-templates"};
  }
  rpc GetSandboxTemplate(.common.v1.Id) returns (SandboxTemplate) {
    option (google.api.http) = {get: "/retask/v1/sandbox-templates/{id}"};
  }
  rpc SetSandboxTemplate(SandboxTemplate) returns (.common.v1.Id) {
    option (google.api.http) = {post: "/retask/v1/sandbox-templates" body: "*"};
  }
  rpc DeleteSandboxTemplate(.common.v1.Id) returns (.common.v1.Empty) {
    option (google.api.http) = {delete: "/retask/v1/sandbox-templates/{id}"};
  }

  // === Activity (read-only) ===
  rpc GetSandboxActivities(SandboxActivitiesRequest) returns (SandboxActivitiesResponse) {
    option (google.api.http) = {get: "/retask/v1/sandboxes/{sandbox_id}/activities"};
  }
}
```

### Request/Response Messages

```protobuf
// --- Sandbox ---
message SandboxesRequest {
  message Filter {
    string                  workspace_id = 1;
    repeated string         sandbox_ids  = 2;
    repeated Sandbox.Type   types        = 3;
    repeated Sandbox.Status statuses     = 4;
  }
  enum Sort {
    DEFAULT                        = 0;
    CREATED_AT_ASC                 = 1;
    CREATED_AT_DESC                = 2;
    LATEST_SESSION_STARTED_AT_ASC  = 3;
    LATEST_SESSION_STARTED_AT_DESC = 4;
    LATEST_SESSION_ACTIVE_AT_ASC   = 5;
    LATEST_SESSION_ACTIVE_AT_DESC  = 6;
  }
  Filter filter                    = 1;
  Sort sort                        = 2;
  .common.v1.Pagination pagination = 3;
}
message SandboxesResponse {
  repeated Sandbox sandboxes = 1;
}

// --- Session ---
message SessionsRequest {
  message Filter {
    string                  workspace_id = 1;
    repeated string         session_ids  = 2;
    repeated string         sandbox_ids  = 3;
    repeated Session.Status statuses     = 4;
    repeated Session.Mode   modes        = 5;
    repeated string         seed_nrns    = 6;
  }
  enum Sort {
    DEFAULT         = 0;
    CREATED_AT_ASC  = 1;
    CREATED_AT_DESC = 2;
    STARTED_AT_ASC  = 3;
    STARTED_AT_DESC = 4;
    LAST_ACTIVE_AT_ASC  = 5;
    LAST_ACTIVE_AT_DESC = 6;
  }
  Filter filter                    = 1;
  Sort sort                        = 2;
  .common.v1.Pagination pagination = 3;
}
message SessionsResponse {
  repeated Session sessions = 1;
}

message NewSessionRequest {
  string         agent_id    = 1;
  string         sandbox_id  = 2;
  Session.Mode   mode        = 3;
  string         name        = 4;
  .common.v1.Nrn seed_nrn    = 5;
  string         seed_prompt = 6;
}

message SessionConnectionResponse {
  string                    url        = 1;   // WebSocket URL with one-time token as query param
  google.protobuf.Timestamp expires_at = 2;   // token expiry — FE must connect before this time
}

// Materialized runtime view of a session, returned by GetSessionRuntime.
// Not a stored entity — assembled on each call from the current Session,
// Sandbox, and resolved secret values from secret-manager.
message SessionRuntime {
  Session session = 1;   // loaded verbatim; NotFound if soft-deleted
  Sandbox sandbox = 2;   // loaded verbatim with Config.EnvVar.SecretValue.value re-injected
}

message ReportSessionStatusRequest {
  string         workspace_id             = 1;
  string         sandbox_id               = 2;
  string         session_id               = 3;
  Session.Status status                   = 4;
  string         last_user_message        = 5;
  google.protobuf.Timestamp last_user_message_at   = 6;
  string         last_system_message      = 7;
  google.protobuf.Timestamp last_system_message_at = 8;
  google.protobuf.Timestamp observed_at            = 9;
}

// --- SandboxTemplate ---
message SandboxTemplatesRequest {
  message Filter {
    string                               workspace_id         = 1;
    repeated string                      sandbox_template_ids = 2;
    repeated .workspace.v1.Sharing.Scope scopes               = 3;
    bool                                 include_scoped       = 4;
  }
  enum Sort {
    DEFAULT         = 0;
    CREATED_AT_ASC  = 1;
    CREATED_AT_DESC = 2;
    NAME_ASC        = 3;
    NAME_DESC       = 4;
  }
  Filter filter                    = 1;
  Sort sort                        = 2;
  .common.v1.Pagination pagination = 3;
}
message SandboxTemplatesResponse {
  repeated SandboxTemplate sandbox_templates = 1;
}

// --- Activity ---
message SandboxActivitiesRequest {
  string                        sandbox_id = 1;
  repeated SandboxActivity.Type types      = 2;
  .common.v1.Pagination         pagination = 3;
}
message SandboxActivitiesResponse {
  repeated SandboxActivity activities = 1;
}
```

## Access Control

### Sharing Model

All entities (Sandbox, Session, SandboxTemplate) use the shared `workspace.v1.Sharing` component:

```protobuf
// proto/workspace/v1/sharing.proto
message Sharing {
  enum Scope {
    SCOPE_PRIVATE    = 0;
    SCOPE_RESTRICTED = 1;
    SCOPE_WORKSPACE  = 2;
  }
  enum Permission {
    PERMISSION_VIEW = 0;
    PERMISSION_EDIT = 1;
  }
  Scope scope                              = 1;
  Permission permission                    = 2;   // ignored when PRIVATE
  repeated .common.v1.Nrn shared_with_nrns = 3;
}
```

### Permission Matrix

| Sharing state | Who can view | Who can edit |
|---|---|---|
| PRIVATE | owner only | owner only |
| RESTRICTED + VIEW | owner + listed NRNs | owner only |
| RESTRICTED + EDIT | owner + listed NRNs | owner + listed NRNs |
| WORKSPACE + VIEW | all workspace members | owner only |
| WORKSPACE + EDIT | all workspace members | owner + workspace OWNER/ADMIN |

### Server-Side Filter Queries

| Scope | Query pattern |
|---|---|
| PRIVATE | `owner_nrn == caller` |
| RESTRICTED | `caller IN shared_with_nrns` (owner is always in the list) |
| WORKSPACE | `workspace_id == caller's workspace` |

### Key Rules

- **Default is PRIVATE.** All three entities default to owner-only access.
- **PRIVATE means PRIVATE.** Even workspace OWNER/ADMIN cannot access another user's PRIVATE sandbox, session, or template.
- **owner_nrn is NOT exposed on any filter.** The server always injects access-control filters based on the caller's identity and sharing rules.
- **ReportSessionStatus and GetSessionRuntime use service-account auth** (proxy only).

## Secret Integration

The sandbox service is the single gateway between the proxy and secret-manager. The proxy never talks to secret-manager directly; it calls `GetSessionRuntime` and receives a Sandbox with secrets already resolved inline.

## Secret Integration (Write Path)

When `SetSandbox` or `SetSandboxTemplate` is called with secret env vars, the sandbox service performs write-path enrichment:

### Creating/Updating Secrets

```
1. Load old config from DB
2. Diff old vs new EnvVars:
   - NEW secret (key not in old, or key exists with a fresh `value`):
     → call secret-manager.SetSecret(Secret{value, workspace_id, related_to_nrn, ...})
     → receive Id + masked_value
     → store secret_nrn + masked_value on the EnvVar
     → strip SecretValue.value (set to "")
   - REMOVED secret (key in old but not in new):
     → call secret-manager.DeleteSecret(old_secret_nrn)
     → immediate hard-delete (no grace period)
   - UNCHANGED secret (key in both, no new `value`):
     → keep existing secret_nrn + masked_value as-is
3. Save updated config to MongoDB (SecretValue.value must use bson:"-" tag)
```

### Template Fork (Deep Copy)

When creating a Sandbox from a SandboxTemplate (user chooses "Default from template"):

```
1. Read template config
2. For each secret EnvVar in template:
   → call secret-manager.AccessSecrets to get real values (BE-BE)
   → call secret-manager.SetSecret for each with related_to_nrn = sandbox NRN
     (NOT the template NRN — the new secrets belong to the sandbox)
   → store new secret_nrns + masked_values on the sandbox config
3. Set source_template_id = template ID
```

This ensures full secret isolation — template and sandbox never share secret entries.

### Write-Time Validation

`SetSandbox` must reject any `secret_nrn` that secret-manager doesn't recognize or that belongs to a different workspace. The BE-BE call to secret-manager doubles as a permission check.

### SecretValue Lifecycle

| Phase | `value` | `secret_nrn` | `masked_value` |
|---|---|---|---|
| FE → SetSandbox (create) | raw secret | empty | empty |
| Sandbox writes to DB | **stripped (bson:"-")** | populated | populated |
| FE ← GetSandbox (read) | **empty** | populated | populated |
| Proxy ← GetSessionRuntime (runtime) | **re-injected** (empty if unresolvable) | populated | populated |

The proxy receives the same `EnvVar` shape regardless of source — it reads `key` + either `plain` or `secret.value`. It does not talk to secret-manager itself.

## Secret Integration (Read Path — Runtime Resolution)

`GetSessionRuntime` is the proxy's runtime entry point. It is BE-BE only (service-token auth); user tokens are rejected. The proxy is trusted to have already validated end-user access via the one-time connect token issued by `ConnectSession`.

```
GetSessionRuntime(id):
  1. Load Session by id.
     → NotFound if not found OR is_deleted=true.
  2. Load Sandbox by session.sandbox_id.
     → NotFound if not found OR is_deleted=true.
  3. Walk sandbox.config.env_vars, collect resource_ids from every
     EnvVar.secret.secret_nrn. If none, skip step 4.
  4. Call secret-manager.AccessSecrets(
       workspace_id: session.workspace_id,
       secret_ids:   <collected>
     ).
     → On success, build {secret_id → plaintext} map.
     → On error (network, timeout, 5xx): log, proceed with empty map.
       Do NOT fail the RPC.
  5. Inject plaintext: for each EnvVar with a secret, set
     secret.value = map[secret_id] if present, else "".
     secret_nrn and masked_value are always preserved.
  6. Return SessionRuntime{session, sandbox}.
  7. Best-effort write a SandboxActivity of type
     TYPE_SESSION_RUNTIME_RESOLVED with metadata:
       - session_id
       - resolved_secret_count
       - missing_secret_nrns (comma-separated fully-qualified NRNs,
         empty string when all resolved)
     actor_nrn = sandbox proxy's service NRN. On failure, log and
     proceed — the runtime response is never held up or failed on
     activity-log errors.
```

**Idempotent, side-effect-free** apart from the best-effort activity write.

**No status gating.** The returned `session.status` and `sandbox.status` reflect current state verbatim.

**Unresolvable secrets don't fail the call.** A missing secret comes back with `value = ""` while `secret_nrn` + `masked_value` stay intact.

## Status Reporting

The proxy reports session state changes via `ReportSessionStatus` (service-auth). Calls are event-driven — the proxy calls once per session whenever that session's status transitions, not on a timer.

### Session Status Transitions

| Transition | Trigger |
|---|---|
| Any → `ACTIVE` | User sends a message |
| `ACTIVE` → `IDLE` | No user/system messages for a short window (proxy-defined) |
| `IDLE` → `TIMEOUT` | No activity for 15 minutes |
| Any → `STOPPED` | User calls `StopSession` |

### ReportSessionStatus Handler

```
ReportSessionStatus(req):
  1. Update Session: status, last_user_message, last_user_message_at,
     last_system_message, last_system_message_at, last_active_at.
  2. If status changed from previous: publish SessionStatusChanged event.
  3. Re-derive Sandbox.Status from all non-deleted sessions for this sandbox
     (see derivation table in Sandbox entity section).
     If sandbox status changed: update Sandbox.status, update
     Sandbox.Report.running_session_count, publish SandboxStatusChanged.
  4. If req.status == IDLE: launch deferred goroutine for LLM extraction.
  5. If req.status == TIMEOUT: evaluate VM shutdown policy.
  6. Return Empty immediately (proxy does not wait for goroutine).
```

### LLM Extraction (Async, on IDLE)

When a session transitions to `IDLE`, the handler launches a goroutine that:

```
goroutine:
  1. Call LLM with session.last_system_message as input.
  2. Extract: need_user_action (YES / NO / UNKNOWN) and action_items
     (list of { question, suggestions }).
  3. Write Session.insights to DB.
  4. On LLM error: log, leave Session.insights unchanged. No retry —
     the next IDLE transition triggers a fresh extraction.
```

The goroutine is fire-and-forget. `ReportSessionStatus` returns before extraction completes.

### VM Shutdown Logic (on TIMEOUT)

When a session transitions to `TIMEOUT`:

```
  1. Load all non-deleted sessions for sandbox_id.
  2. If any session status != TIMEOUT: no action.
  3. All sessions are TIMEOUT — check sandbox.config.shutdown_policy:
     - SHUTDOWN_POLICY_ON_IDLE_NO_USER_ACTIONS:
         If any session has insights.need_user_action == YES: no action.
         If insights is nil (extraction not yet complete or failed):
           treat as UNKNOWN — do not shut down.
         Otherwise: proceed to step 4.
     - SHUTDOWN_POLICY_ON_IDLE: proceed to step 4 unconditionally.
     - SHUTDOWN_POLICY_NEVER: no action.
  4. Set Sandbox.status = STATUS_IDLE.
     Publish SandboxStatusChanged (old → STATUS_IDLE).
     (Actual VM teardown is handled by the proxy on SandboxStatusChanged.)
```

### Sandbox.Report Denormalization

`Sandbox.Report` stores denormalized counters updated atomically by the sandbox service:

- `NewSession` → increments `total_session_count`, updates `latest_session_*`
- `ReportSessionStatus` → updates `running_session_count` (count of STATUS_ACTIVE sessions) when sandbox status is re-derived
- `StopSession` → no change to Report (next ReportSessionStatus will update running count)

### Session Stop Flow

```
User clicks "Stop" in FE
  → FE calls StopSession(session_id)
  → Sandbox service sets status=STATUS_STOPPED, ended_at=now()
  → Proxy on next status change or reconnect attempt: sees STATUS_STOPPED → tears down PTY
```

### Sandbox Stop Flow

```
User clicks "Stop Sandbox" in FE
  → FE calls StopSandbox(sandbox_id)
  → Sandbox service sets status=STATUS_STOPPED, publishes SandboxStatusChanged
  → Proxy consumes SandboxStatusChanged → tears down VM and all PTYs
```

## Events & Cascades

### Events Published

Proto file: `proto/retask/sandbox/v1/event/event.proto`

```protobuf
message SandboxCreated {
  string                    sandbox_id     = 1;
  string                    workspace_id   = 2;
  .common.v1.Nrn            created_by_nrn = 3;
  google.protobuf.Timestamp created_at     = 4;
  google.protobuf.Timestamp occurred_at    = 5;
}

message SandboxDeleted {
  string                    sandbox_id     = 1;
  string                    workspace_id   = 2;
  .common.v1.Nrn            deleted_by_nrn = 3;
  google.protobuf.Timestamp deleted_at     = 4;
  google.protobuf.Timestamp occurred_at    = 5;
}

message SandboxStatusChanged {
  string                    sandbox_id   = 1;
  string                    workspace_id = 2;
  Sandbox.Status            old_status   = 3;
  Sandbox.Status            new_status   = 4;
  google.protobuf.Timestamp occurred_at  = 5;
}

message SandboxTemplateCreated {
  string                    sandbox_template_id = 1;
  string                    workspace_id        = 2;
  .common.v1.Nrn            created_by_nrn      = 3;
  google.protobuf.Timestamp created_at          = 4;
  google.protobuf.Timestamp occurred_at         = 5;
}

message SandboxTemplateDeleted {
  string                    sandbox_template_id = 1;
  string                    workspace_id        = 2;
  .common.v1.Nrn            deleted_by_nrn      = 3;
  google.protobuf.Timestamp deleted_at          = 4;
  google.protobuf.Timestamp occurred_at         = 5;
}

message SandboxTemplateConfigChanged {
  string                    sandbox_template_id = 1;
  string                    workspace_id        = 2;
  .common.v1.Nrn            updated_by_nrn      = 3;
  google.protobuf.Timestamp updated_at          = 4;
  google.protobuf.Timestamp occurred_at         = 5;
}

message SessionStatusChanged {
  string                    session_id   = 1;
  string                    sandbox_id   = 2;
  string                    workspace_id = 3;
  Session.Status            old_status   = 4;
  Session.Status            new_status   = 5;
  google.protobuf.Timestamp occurred_at  = 6;
}
```

### Cascade Handlers

**WorkspaceDeleted → Sandbox Service:**
1. Soft-delete all SandboxTemplates → publish `SandboxTemplateDeleted` per template
2. Soft-delete all Sandboxes → publish `SandboxDeleted` per sandbox
3. Sessions and Activities cleaned up via `SandboxDeleted` self-listen

Ordering: templates first, then sandboxes. This ensures secrets are cleaned up even if sandbox deletion partially fails.

`deletion_info`: `source=CASCADE`, `parent_nrn=workspace NRN`

**SandboxDeleted → Sandbox Service (self-listen):**
- Soft-delete all Sessions for this sandbox
- Soft-delete all SandboxActivities for this sandbox

`deletion_info`: `source=CASCADE`, `parent_nrn=sandbox NRN`

**SandboxDeleted → Secret Manager:**
- Hard-delete all Secrets where `related_to_nrn` matches sandbox NRN

**SandboxTemplateDeleted → Secret Manager:**
- Hard-delete all Secrets where `related_to_nrn` matches template NRN

**SandboxTemplateConfigChanged → Sandbox Service (self-listen):**
- For every Sandbox where `source_template_id == sandbox_template_id && is_deleted == false`, replace `config` with the current template `config` and append a `TYPE_TEMPLATE_SYNCED` SandboxActivity.
- Sandboxes whose `source_template_id` has been cleared are skipped automatically.
- `updated_by_nrn` on the cascaded write is the event's `updated_by_nrn`.

**WorkspaceDeleted → Secret Manager (belt + suspenders):**
- Hard-delete all Secrets where `workspace_id` matches

### Events NOT Included (v1)

- `SessionCreated` / `SessionStopped` — no external consumers. Can be added later.

## Implementation Notes for Go Service

- `SecretValue.value` must use `bson:"-"` tag to prevent raw secrets from ever being written to MongoDB.
- `owner_nrn` must always be included in `shared_with_nrns` when `sharing.scope = RESTRICTED`. Enforced server-side on `SetSandbox` / `SetSandboxTemplate`.
- `source_template_id` on Sandbox must be cleared whenever any field in `config` is modified. Clearing also opts the sandbox out of future `SandboxTemplateConfigChanged` cascades.
- `SetSandboxTemplate` must publish `SandboxTemplateConfigChanged` only when the persisted `config` differs from the prior stored `config`. Metadata-only edits must not publish the event.
- `SandboxActivity` entries are written internally — there is no `SetSandboxActivity` RPC.
- `NewSession` is create-only — no upsert semantics.
- Sessions are flat resources (`/retask/v1/sessions`), not nested under sandboxes, because they need to be queried across sandboxes.
- Activities ARE nested (`/retask/v1/sandboxes/{sandbox_id}/activities`) because they are always scoped to a single sandbox.
- **Sandbox status is never explicitly reported by the proxy.** It is derived from session states on every `ReportSessionStatus` call and written only when it changes.
- **LLM extraction uses a fire-and-forget goroutine.** If extraction fails, `Session.insights` is not updated and remains at its previous value (or nil). The next IDLE transition triggers a fresh attempt.
- **`SHUTDOWN_POLICY_ON_IDLE_NO_USER_ACTIONS` treats nil insights as UNKNOWN** — if LLM extraction has not completed or failed when TIMEOUT fires, the session is conservatively treated as potentially needing user action and the VM is not shut down.
- `ReportSessionStatus` uses service-token auth (proxy only). User tokens must be rejected.
