# API Contracts

This is the **source of truth for all API definitions** across NWEB services. Protobuf definitions here are used to generate typed client/server code for each target service. Changes here propagate to all consumers via code generation.

## Structure

```
proto/
  common/v1/        # Shared types (Id, Empty, Pagination, Nrn, etc.)
  auth/v1/          # Auth / user
  workspace/v1/     # Workspace management and members
  subscription/v1/  # Subscription plans
  quota/v1/         # Quota management
  cron/v1/          # Cron configuration and events
  file/v1/          # File storage
  message/v1/       # Message routing
  echo/v1/          # Echo (test/debug)
  test/v1/          # Test service
  customer/v1/      # Customer management
  migration/v1/     # Database migrations
  ai/               # NWEB.AI specific services
    chat/v1/        # Chat/messaging
    credit/v1/      # Credit management
    payment/v1/     # Payment events
    project/v1/     # AI project management
    report/v1/      # Reporting
    ocr/v1/         # OCR processing
    agent/v1/       # AI agent
  ai-gateway/v1/    # AI gateway
  retask/           # Retask.work services
    common/v1/      # Retask shared types (TaskStatus, TaskType, SourceType)
    project/v1/     # Retask projects and members
    task/v1/        # Retask tasks
    sandbox/v1/     # Retask sandbox VMs and sessions
  secret/v1/        # Secret manager (global service)
```

### Architecture docs

```
docs/
  architecture/
    retask-sandbox.md    # Sandbox service design (entities, RPCs, events, access control)
    secret-manager.md    # Secret manager service design (entities, RPCs, data flow)
```

Each service directory may contain:
- `*.proto` — service definition and message types
- `event/` — PubSub event message definitions
- `command/` — internal command message definitions

## Workflow

1. Edit `.proto` files here as the source of truth
2. Run code generation to produce typed Go code in `api-contracts-gen/`
3. Services import generated code — never hand-edit generated files

This repo is embedded as a git subtree under `api-contracts/` in multiple service repositories. To sync from any of those repos:

```bash
# Pull latest from api-contracts repo
git subtree pull --prefix api-contracts git@github.com:nwebxyz/api-contracts.git main --squash

# Push changes back to api-contracts repo
git subtree push --prefix api-contracts git@github.com:nwebxyz/api-contracts.git main
```

Changes pushed from one repo will be available to all other repos that embed this subtree.

## HTTP URL Conventions

### Nested resources
Sub-resources must be nested under their parent in the URL path. Use the field name from the request message as the path parameter (e.g. `{workspace_id}`, `{project_id}`), not a generic `{id}`.

```
GET    /v1/workspaces/{id}/members
POST   /v1/workspaces/{workspace_id}/members
DELETE /v1/workspaces/{workspace_id}/members
```

### Custom methods
Use `:` (not `/`) to separate custom action names from the resource path, per Google AIP-136.

```
POST /retask/v1/projects/{id}:archive          # correct
POST /retask/v1/projects/{id}/archive          # wrong
```

### Flat resources
Resources that exist independently (not owned by a single parent) may use a flat path.

```
POST   /retask/v1/external-projects
DELETE /retask/v1/external-projects/{id}
```

### Path parameter naming
- Use `{id}` when the RPC takes `common.v1.Id` directly.
- Use the actual field name (e.g. `{workspace_id}`, `{project_id}`) when the RPC takes a custom request message.

## RPC Naming Conventions

### Get list of items: `Get<Items>`
- Request: `<Items>Request` (plural form, no `Get` prefix)
- Response: `<Items>Response` (plural form, no `Get` prefix)
- Example: `rpc GetCustomers (CustomersRequest) returns (CustomersResponse)`

### Get single item by ID: `Get<Item>`
- Request: `common.v1.Id`
- Response: `<Item>` (the entity message itself)
- Example: `rpc GetCustomer (common.v1.Id) returns (Customer)`

### Get single item by custom params: `Get<Item>`
- Request: `<Item>Request` (singular form, no `Get` prefix)
- Response: `<Item>` (the entity message itself)
- Example: `rpc GetFileDetails (FileDetailsRequest) returns (File)`

### Get single item with custom response: `Get<Item>`
- Request: `<Item>Request` (singular form, no `Get` prefix)
- Response: `<Item>Response` (singular form, no `Get` prefix)
- Example: `rpc GetFileSignedUrl (FileSignedUrlRequest) returns (FileSignedUrlResponse)`

### Lookup by alternate key: `Get<Item>By<Field>`
- Request: `common.v1.Id` or `common.v1.Nrn` (a common identifier type)
- Response: `<Item>` (the entity message itself)
- Example: `rpc GetCustomerByUserId (common.v1.Id) returns (Customer)`

### Key principles
- Never prefix message types with `Get` — the verb belongs on the RPC name, not the message type
- List RPCs use **plural** form for both request and response
- Single-item-by-ID RPCs use `common.v1.Id` and return the entity directly
- Alt-key lookups use a common identifier type and return the entity directly

## Filter Field Conventions

Filter fields should use `repeated` (arrays) wherever multi-select filtering is reasonable. This lets the FE send e.g. `statuses: [ACTIVE, IDLE]` in a single request instead of multiple calls.

**Exceptions** that remain singular:
- `workspace_id` — always a single required scope.
- Fields where multi-select makes no semantic sense.

```protobuf
message Filter {
  string workspace_id           = 1;   // singular — required scope
  repeated string sandbox_ids   = 2;   // array
  repeated Sandbox.Type types   = 3;   // array — multi-select
  repeated Sandbox.Status statuses = 4; // array — multi-select
}
```

## Audit Fields Convention

Any stored Protobuf message that is created or updated via user input (Set/Create/Update RPCs) **must** include these four audit fields:

```protobuf
  common.v1.Nrn created_by_nrn = N;
  common.v1.Nrn updated_by_nrn = N;
  google.protobuf.Timestamp created_at = N;
  google.protobuf.Timestamp updated_at = N;
```

- `created_by_nrn` and `created_at` are set once on insert.
- `updated_by_nrn` and `updated_at` are set on every insert and update.
- The NRN is extracted from the authenticated caller (via `auth.ExtractTokenUser`), not from the request message.

## Soft Delete Convention

All main service entities (workspace, project, task, customer, file, ...) default to **soft-delete**. When designing a new entity, assume soft-delete unless the user explicitly opts out.

### Required fields

Every soft-delete-aware message must include:

```protobuf
  bool is_deleted = N;                              // @inject_tag: bson:"is_deleted"
  common.v1.DeletionInfo deletion_info = N;         // @inject_tag: bson:"deletion_info,omitempty"
```

`DeletionInfo` (in `common/v1/soft_delete.proto`) records `source` (`USER` or `CASCADE`), the cascading `parent_nrn`, and an optional note.

### RPC contract

- `DeleteX` RPCs must set `is_deleted = true` AND populate `deletion_info`:
  - Direct user delete: `source = USER`, `parent_nrn` empty.
  - Cascade from parent deletion: `source = CASCADE`, `parent_nrn = <parent resource NRN>`.
- `Get<Item>`, `Get<Items>`, and `Set<Item>` paths must filter out soft-deleted documents server-side. Soft-deleted items are invisible to the API — there is no "show me the deleted ones" mode.
- `is_deleted` MUST NOT appear as a field on any `<Items>Request.Filter`.
- Parent-resource `Delete` events cascade to children in owning services via pubsub listeners; child entities do not check parent `is_deleted` directly — they rely on `Get<Parent>` returning NotFound.

## Nested Type Naming

When a message or enum is nested inside another message, do **not** repeat the parent's name in the nested type's name. The parent already provides scope, so prefixing the nested type with the parent's name is redundant.

```protobuf
message Sandbox {
  message Config {           // correct — referenced as Sandbox.Config
    repeated EnvVar env_vars = 1;
  }
  Config config = 5;
}

message Sandbox {
  message SandboxConfig {    // wrong — repeats parent name
    repeated EnvVar env_vars = 1;
  }
  SandboxConfig config = 5;
}
```

Applies to nested messages and nested enums. **Exception:** enum *values* must still be prefixed with the enum type name (see Enum Conventions below) — proto3 enum values are scoped at the package level, not the enum, so unprefixed values can collide.

### Nesting depth

Nested types must be defined inside the message that **directly uses them**, not at a higher parent level. If `EnvVar` is only used inside `Config`, it belongs inside `Config`, not its grandparent.

```protobuf
message Sandbox {
  message Config {
    message EnvVar { ... }         // correct — EnvVar is used by Config
    repeated EnvVar env_vars = 1;
  }
}

message Sandbox {
  message EnvVar { ... }           // wrong — EnvVar is not used directly by Sandbox
  message Config {
    repeated Sandbox.EnvVar env_vars = 1;
  }
}
```

## Enum Conventions

Enum values must be prefixed with the enum type name in `UPPER_SNAKE_CASE`, per the [Protobuf style guide](https://protobuf.dev/programming-guides/style/#enums). This prevents naming collisions since proto3 enums are scoped at the package level.

```protobuf
enum ThemePreference {
  THEME_PREFERENCE_LIGHT = 0;   // correct — prefixed with enum name
  THEME_PREFERENCE_DARK = 1;
  THEME_PREFERENCE_SYSTEM = 2;
}

enum ThemePreference {
  LIGHT = 0;                    // wrong — no prefix
  DARK = 1;
  SYSTEM = 2;
}
```
