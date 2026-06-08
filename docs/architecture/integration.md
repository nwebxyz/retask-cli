# Integration Service — Architecture Design

> Proto package: `integration.v1`
> Proto file: `proto/integration/v1/integration.proto`

## Overview

The Integration service manages connections from NWEB workspaces to third-party providers (GitHub, Anthropic, OpenAI, …). It stores integration metadata and delegates credential storage to the existing `secret.v1` service.

Two trust tiers:

- **User-callable RPCs** (`GetProviders`, `GetIntegrations`, `SetIntegration`, `DeleteIntegration`) — authenticated end-user, workspace permission checks apply.
- **BE-BE only** (`AccessIntegration`) — service-account auth only; user tokens rejected. The only path that returns plaintext credentials.

## Architecture

```
        ┌──────────────────┐
   FE ──▶│  IntegrationSvc  │── BE-BE ──▶ ┌──────────────────┐
        │  (integration/v1)│             │  Secret Manager  │
        │                  │             │  (secret/v1)     │
        │  GetProviders    │             └──────────────────┘
        │  GetIntegrations │
        │  SetIntegration  │
        │  DeleteIntegration│
        └──────────────────┘
                 ▲
                 │ BE-BE only
        ┌────────┴─────────┐
        │   Sandbox /      │
        │   AI Agent       │ ── AccessIntegration ──▶
        │   Service        │
        └──────────────────┘
```

## Entities

### Entity: Provider

A read-only catalog row. Engineers seed and manage these via migrations until admin write RPCs are added.

```protobuf
message Provider {
  // @inject_tag: bson:"_id,omitempty"
  // String key, e.g. "github", "anthropic", "openai".
  string provider_id = 1;
  // @inject_tag: bson:"name"
  string name = 2;
  // @inject_tag: bson:"logo,omitempty"
  string logo = 3;

  // Auth method gating. Both default false; at least one must be enabled
  // for the provider to be usable.
  // @inject_tag: bson:"disable_oauth_flow"
  bool disable_oauth_flow = 4;
  // @inject_tag: bson:"disable_access_token"
  bool disable_access_token = 5;

  // OAuth authorize URL template. Empty for MVP / providers that don't
  // support OAuth.
  // @inject_tag: bson:"oauth_authorize_url,omitempty"
  string oauth_authorize_url = 6;

  // @inject_tag: bson:"created_at"
  google.protobuf.Timestamp created_at = 7;
  // @inject_tag: bson:"updated_at"
  google.protobuf.Timestamp updated_at = 8;
}
```

No `created_by_nrn` / `updated_by_nrn` — no public write path; provenance is the migration commit.

### Entity: Integration

```protobuf
message Integration {
  enum Level {
    LEVEL_UNKNOWN   = 0;
    LEVEL_WORKSPACE = 1;   // admin-managed, shared across workspace
    LEVEL_MEMBER    = 2;   // private to one workspace member
  }

  enum AccessLevel {
    ACCESS_LEVEL_UNKNOWN = 0;
    ACCESS_LEVEL_READ    = 1;
    ACCESS_LEVEL_WRITE   = 2;
    ACCESS_LEVEL_ADMIN   = 3;   // provider-dependent
  }

  message Credentials {
    // Transient: populated on SetIntegration, stripped before DB storage.
    // Plaintext stored in secret-manager; re-injected on AccessIntegration.
    // @inject_tag: bson:"-"
    string access_token = 1;
    // @inject_tag: bson:"access_token_secret_nrn"
    common.v1.Nrn access_token_secret_nrn = 2;
    // @inject_tag: bson:"access_token_masked"
    string access_token_masked = 3;

    // Refresh token. Empty for access-token integrations and OAuth tokens
    // that don't expire.
    // @inject_tag: bson:"-"
    string refresh_token = 4;
    // @inject_tag: bson:"refresh_token_secret_nrn,omitempty"
    common.v1.Nrn refresh_token_secret_nrn = 5;
    // @inject_tag: bson:"refresh_token_masked,omitempty"
    string refresh_token_masked = 6;
  }

  message ExternalAccount {
    // Provider's user/org id (e.g. GitHub user id, OpenAI org id).
    // @inject_tag: bson:"id"
    string id = 1;
    // @inject_tag: bson:"name"
    string name = 2;
    // @inject_tag: bson:"avatar,omitempty"
    string avatar = 3;
  }

  // @inject_tag: bson:"_id,omitempty"
  string integration_id = 1;
  // @inject_tag: bson:"workspace_id"
  string workspace_id = 2;
  // @inject_tag: bson:"provider_id"
  string provider_id = 3;
  // @inject_tag: bson:"level"
  Level level = 4;
  // For LEVEL_MEMBER: the workspace_member_id of the owning member.
  // Empty for LEVEL_WORKSPACE. Always overwritten server-side — never
  // trusted from the request body.
  // @inject_tag: bson:"owner_member_id,omitempty"
  string owner_member_id = 5;

  // @inject_tag: bson:"access_level"
  AccessLevel access_level = 6;

  // Granted scopes from OAuth token response. Empty for access-token
  // integrations (scopes are not verified in MVP).
  // @inject_tag: bson:"granted_scopes"
  repeated string granted_scopes = 7;

  // Identity of the connected external account, fetched once at connect
  // time (e.g. GET /user on GitHub). Used for display and dedupe.
  // @inject_tag: bson:"external_account,omitempty"
  ExternalAccount external_account = 8;

  // Token expiry. Empty for access-token integrations and OAuth tokens
  // that don't expire. AccessIntegration returns FAILED_PRECONDITION if
  // now() > expires_at.
  // @inject_tag: bson:"expires_at,omitempty"
  google.protobuf.Timestamp expires_at = 9;

  // @inject_tag: bson:"credentials"
  Credentials credentials = 10;

  // Audit (hard-delete only — no soft-delete fields).
  // @inject_tag: bson:"created_by_nrn"
  common.v1.Nrn created_by_nrn = 11;
  // @inject_tag: bson:"created_at"
  google.protobuf.Timestamp created_at = 12;
  // @inject_tag: bson:"updated_by_nrn"
  common.v1.Nrn updated_by_nrn = 13;
  // @inject_tag: bson:"updated_at"
  google.protobuf.Timestamp updated_at = 14;
  // Updated on each successful AccessIntegration. Used for "last used X
  // ago" display and future inactivity cleanup.
  // @inject_tag: bson:"last_used_at,omitempty"
  google.protobuf.Timestamp last_used_at = 15;
}
```

#### Key notes

- **`owner_member_id`** is `WorkspaceMember.workspace_member_id` (not the user NRN). Tying to membership means removing a member from the workspace cascades their integrations cleanly.
- **Unique index**: `{workspace_id, provider_id, level, owner_member_id}`. For `LEVEL_WORKSPACE`, `owner_member_id` is `""`, so the (workspace, provider, WORKSPACE) tuple is enforced as unique by the same index.
- **Hard delete only** — no `is_deleted` / `deletion_info` fields. When the row is gone, `IntegrationDeleted` drives secret-manager to clean up the underlying secret rows.

## RPC Surface

```protobuf
service IntegrationService {
  // === Provider catalog (read-only) ===

  rpc GetProviders(ProvidersRequest) returns (ProvidersResponse) {
    option (google.api.http) = {get: "/v1/integration-providers"};
  }
  rpc GetProvider(common.v1.Id) returns (Provider) {
    option (google.api.http) = {get: "/v1/integration-providers/{id}"};
  }

  // === Integration CRUD (user-callable) ===

  rpc GetIntegrations(IntegrationsRequest) returns (IntegrationsResponse) {
    option (google.api.http) = {get: "/v1/integrations"};
  }
  rpc GetIntegration(common.v1.Id) returns (Integration) {
    option (google.api.http) = {get: "/v1/integrations/{id}"};
  }
  // Create or update. Uniqueness on (workspace_id, provider_id, level,
  // owner_member_id) means a second call for the same tuple replaces the
  // existing row and its underlying secrets.
  rpc SetIntegration(Integration) returns (common.v1.Id) {
    option (google.api.http) = {
      post: "/v1/integrations"
      body: "*"
    };
  }
  // Hard delete. Removes the Integration row and emits IntegrationDeleted;
  // secret-manager listens and cascades the underlying token secrets.
  rpc DeleteIntegration(common.v1.Id) returns (common.v1.Empty) {
    option (google.api.http) = {delete: "/v1/integrations/{id}"};
  }

  // === Runtime access (BE-BE only) ===

  // Resolves the right Integration for (workspace, provider, caller) and
  // returns plaintext credentials. User tokens are rejected.
  // Resolution: MEMBER first (via caller's user_nrn in request metadata
  // → workspace_member_id), then WORKSPACE.
  // Errors: NOT_FOUND if neither exists; FAILED_PRECONDITION if
  // expires_at is in the past.
  rpc AccessIntegration(AccessIntegrationRequest) returns (AccessIntegrationResponse) {
    option (google.api.http) = {
      post: "/v1/integrations:access"
      body: "*"
    };
  }
}
```

### Request / Response Messages

```protobuf
message ProvidersRequest {
  message Filter {
    repeated string provider_ids = 1;   // optional — fetch a subset
  }
  Filter filter = 1;
}
message ProvidersResponse {
  repeated Provider providers = 1;
}

message IntegrationsRequest {
  message Filter {
    // Required.
    string          workspace_id     = 1;
    // Optional.
    repeated string integration_ids  = 2;
    // Optional.
    repeated string provider_ids     = 3;
  }
  Filter filter = 1;
}
message IntegrationsResponse {
  repeated Integration integrations = 1;
}

message AccessIntegrationRequest {
  string workspace_id = 1;
  string provider_id  = 2;
}
message AccessIntegrationResponse {
  // The resolved Integration with credentials.access_token and
  // credentials.refresh_token populated with plaintext. The only path
  // that returns plaintext credentials.
  Integration integration = 1;
}
```

## Access Control

### Permission matrix

| Action | Required role |
|---|---|
| `GetProviders` / `GetProvider` | Any authenticated user |
| `GetIntegrations` / `GetIntegration` | Active workspace member; MEMBER-level rows restricted to owner |
| `SetIntegration` (level=WORKSPACE) | Workspace `ADMIN` or `OWNER` |
| `SetIntegration` (level=MEMBER) | Any active workspace member |
| `DeleteIntegration` (level=WORKSPACE) | Workspace `ADMIN` or `OWNER` |
| `DeleteIntegration` (level=MEMBER) | Owning member only |
| `AccessIntegration` | **Service-account only** — user tokens rejected |

### Server-side enforcement

- **`owner_member_id`** is never trusted from the request body. For `LEVEL_MEMBER`, the handler resolves it from the caller's `user_nrn` + `workspace_id` and overwrites the field. Prevents a member from creating an integration on behalf of another member.
- For `LEVEL_WORKSPACE`, `owner_member_id` is forced to `""` server-side.

### `GetIntegrations` visibility

Server filters silently — no `PERMISSION_DENIED`, just filtered rows:

- `LEVEL_WORKSPACE` rows: visible to all active workspace members.
- `LEVEL_MEMBER` rows: visible only to the owning member. `ADMIN`/`OWNER` cannot see other members' personal integrations.

### `AccessIntegration` trust boundary

The only RPC returning plaintext. Two hard constraints:

1. Auth layer rejects user tokens — service-account only.
2. Calling backend passes the end-user's `user_nrn` in request metadata (not body). Integration service resolves it to `workspace_member_id` for MEMBER lookup.

## Data Flows

### Flow 1: Connect via access token (PAT/API key)

```
1. FE calls GetProviders → renders catalog gated by
   provider.disable_access_token / disable_oauth_flow.

2. User clicks "Connect GitHub" (workspace level), pastes their PAT.

3. FE calls SetIntegration(Integration{
     workspace_id: W,
     provider_id: "github",
     level: LEVEL_WORKSPACE,
     access_level: ACCESS_LEVEL_READ,
     credentials: { access_token: "ghp_xxx" },
   })

4. Handler:
   a. Auth check: WORKSPACE → caller must be ADMIN/OWNER.
   b. Optional identity fetch (GET api.github.com/user) → external_account.
      If the call fails, integration is saved with empty external_account.
   c. Calls secret-manager.SetSecret(Secret{
        workspace_id: W,
        related_to_nrn: nweb:integration:integration:<new_uuid>,
        name: "<provider>:access_token",
        value: "ghp_xxx",
      }) → returns secret_nrn + masked_value.
   d. Builds credentials: access_token = "" (bson:"-"),
      access_token_secret_nrn = <nrn>,
      access_token_masked = "ghp_•••••aB12".
   e. Inserts (or upserts on uniqueness conflict) the Integration document.
   f. Publishes IntegrationCreated (or IntegrationUpdated on upsert).
   g. Returns Id.
```

### Flow 2: Listing for FE

```
1. FE calls GetProviders → catalog (one-shot, no workspace filter).
2. FE calls GetIntegrations(filter: {workspace_id: W}) → connected rows.
3. FE joins client-side: for each provider, render workspace-level and
   member-level status.
```

### Flow 3: Runtime access (BE-BE)

```
AI agent or sandbox service needs the user's GitHub token.

1. Service calls AccessIntegration(workspace_id: W, provider_id: "github")
   with service-account auth + caller's user_nrn in request metadata.

2. Handler:
   a. Reject if not service-account.
   b. Resolve caller's workspace_member_id from user_nrn + workspace_id.
   c. Try MEMBER: (workspace_id, provider_id, level=MEMBER,
      owner_member_id=<caller>). If found → use it.
   d. Else WORKSPACE: (workspace_id, provider_id, level=WORKSPACE).
      If found → use it.
   e. Else NotFound.
   f. If expires_at set and now > expires_at → FAILED_PRECONDITION.
   g. Call secret-manager.AccessSecrets([access_token_secret_nrn,
      refresh_token_secret_nrn]) with workspace_id.
   h. Mirror plaintext into credentials.access_token / .refresh_token.
   i. Async: bump last_used_at on the row.
   j. Return AccessIntegrationResponse{ integration }.
```

### Flow 4: Update (rotate access token)

```
1. User pastes a new PAT in the Connect form.
2. FE calls SetIntegration with the same (workspace, provider, level,
   owner_member_id) tuple → upsert path.
3. Handler:
   a. Loads existing row.
   b. Calls secret-manager.SetSecret with existing access_token_secret_nrn
      → updates encrypted value + recomputes masked_value in place.
   c. Updates credentials.access_token_masked and clears expires_at if
      the new credential doesn't expire.
   d. Publishes IntegrationUpdated.
```

### Flow 5: Delete

```
1. User clicks Disconnect.
2. FE calls DeleteIntegration(id).
3. Handler:
   a. Auth check.
   b. Hard-deletes the Integration document.
   c. Publishes IntegrationDeleted.
4. Secret-manager listens to IntegrationDeleted → hard-deletes all
   Secret rows where related_to_nrn = nweb:integration:integration:<id>.
```

### Flow 6: Cascade deletion

```
WorkspaceDeleted (from workspace service)
  → Integration service handler:
    1. Find all Integrations where workspace_id = <deleted>.
    2. Hard-delete each + publish IntegrationDeleted (drives
       secret-manager cleanup automatically).

WorkspaceMemberRemoved (from workspace service)
  → Integration service handler:
    1. Find all Integrations where workspace_id = W AND
       level = LEVEL_MEMBER AND owner_member_id = <removed>.
    2. Hard-delete each + publish IntegrationDeleted for each.
```

## Events

Published by integration service:

| Event | Published when |
|---|---|
| `IntegrationCreated` | New integration row inserted |
| `IntegrationUpdated` | Existing integration row updated (token rotation, etc.) |
| `IntegrationDeleted` | Integration hard-deleted |

**Consumers:**

- **Secret-manager**: listens to `IntegrationDeleted` → hard-deletes all `Secret` rows where `related_to_nrn = nweb:integration:integration:<id>`. Same pattern as `SandboxDeleted` / `SandboxTemplateDeleted`.
- **Sandbox / AI agent service**: may listen to `IntegrationDeleted` or `IntegrationUpdated` to invalidate caches.

## NRN Format

- Integration: `nweb:integration:integration:<uuid>`
- Provider: `nweb:integration:provider:<id>`

## Deferred: OAuth Flow

The entity shape is already OAuth-ready (`granted_scopes`, `expires_at`, `credentials.refresh_token_*`). When OAuth ships, two new RPCs are added:

```protobuf
// Step 1: generate the provider authorize URL + state token.
// Returns the URL for FE to redirect to.
rpc StartOAuthIntegration(StartOAuthIntegrationRequest)
    returns (StartOAuthIntegrationResponse);

// Step 2: provider redirects back with code + state.
// BE exchanges code for tokens, fetches external_account,
// stores via secret-manager, upserts the Integration row.
rpc CompleteOAuthIntegration(CompleteOAuthIntegrationRequest)
    returns (common.v1.Id);
```

Token refresh: when `AccessIntegration` detects `now > expires_at` and `refresh_token_secret_nrn` is set, handler attempts a refresh (POST to provider token endpoint), updates both secret rows and `expires_at` in place, and returns the fresh credentials. If `refresh_token_secret_nrn` is absent or the refresh call fails, returns `UNAUTHENTICATED` with a reconnect signal.

`Provider.oauth_authorize_url` is the base template for `StartOAuthIntegration` to build the full authorize URL (appending `client_id`, `redirect_uri`, `scope`, `state`).
