# Auth Service — Architecture Design

> Proto package: `auth.v1`
> Proto file: `proto/auth/v1/auth.proto`
> Public issuer / domain: `https://auth.nweb.xyz`

## Overview

The `auth` service is a **global service** that issues and manages the platform's own access credentials. It wraps
Firebase (which remains the primary FE identity provider) and adds two capabilities Firebase can't provide well:

1. **Personal Access Tokens (PATs)** — user-level, revocable, optionally workspace-restricted credentials for the
   `retask` CLI and other programmatic clients.
2. **Short-lived, audience-scoped JWTs** — the one-time WebSocket token that [Sandbox Service](./retask-sandbox.md)
   needs for the `ConnectSession` flow, and 1-hour access JWTs minted from PATs.
3. **Session PATs** — system-issued PATs created per session by the sandbox service (BE-BE), injected as `NWEB_API_KEY`
   into the container environment, and auto-revoked when the session stops.

Firebase remains in charge of **who is this user?** Auth service is in charge of **what can this bearer token do, where,
and for how long?**

## Architecture

```
┌───────────────────────────────────────────────────────────────┐
│                     GCP Cloud Endpoints                       │
│  issuer #1: securetoken.google.com (Firebase — FE)            │
│  issuer #2: auth.nweb.xyz          (own JWTs — CLI, WebSocket)│
└──────┬─────────────────────────────────────────┬──────────────┘
       │ verified JWT claims forwarded           │
       ▼                                         ▼
┌──────────────┐                       ┌────────────────────┐
│  BE services │                       │   Auth Service     │
│  (retask,    │─── Firebase ID ──────▶│   (auth/v1)        │
│   secret,    │     exchange (n/a)    │                    │
│   etc.)      │                       │  • PAT mgmt        │
│              │                       │  • ExchangePat     │
│              │                       │  • IssueSessionPat │
└──────────────┘                       └────────┬───────────┘
                                                │
                                                ▼
                                       ┌────────────────────┐
                                       │  MongoDB           │
                                       │  (Pat — user +     │
                                       │   system source)   │
                                       └────────────────────┘

                                       ┌────────────────────┐
                                       │  /.well-known/     │
                                       │    jwks.json       │  ← fetched by Cloud
                                       │  (HTTP, public)    │    Endpoints on boot
                                       └────────────────────┘  + every ~5 min
```

### Two-issuer chain at Cloud Endpoints

The API gateway is configured with **two JWT issuers**. Every request's `Authorization: Bearer <jwt>` is validated
against one of them locally — no call-back to auth service on the hot path.

| # | Issuer                             | Audience(s)      | Used by                                        | Purpose                                  |
|---|------------------------------------|------------------|------------------------------------------------|------------------------------------------|
| 1 | `securetoken.google.com/<project>` | Firebase project | Web FE                                         | Existing Firebase ID token flow          |
| 2 | `https://auth.nweb.xyz`            | `nweb-api`       | `retask` CLI, sandbox programs                 | Short-lived access JWTs minted from PATs |
| 2 | `https://auth.nweb.xyz`            | `sandbox-proxy`  | Sandbox Proxy only                             | One-time WebSocket tokens                |

Issuer #2 is one logical issuer (one JWKS, one signing keypair) that emits tokens with different `aud` claims depending
on who the token is for.

Both Firebase and auth-issued JWTs carry `sub = <user_nrn>`. BE services extract the user NRN from verified claims and
do not care which issuer signed.

### Communication Rules

| From            | To                  | Auth                    | Purpose                                                       |
|-----------------|---------------------|-------------------------|---------------------------------------------------------------|
| FE              | Auth Service        | Firebase ID             | `GetPats`, `CreatePat`, `RevokePat`                           |
| CLI             | Auth Service        | none (public endpoint)  | `ExchangePat`                                                 |
| Sandbox Service | Auth Service        | GCP service-account JWT | `IssueSessionPat`                                             |
| Cloud Endpoints | Auth Service (HTTP) | none                    | Fetch `/.well-known/jwks.json`                                |

### Zero Pubsub Out of Auth Service (v1)

Auth service publishes no events in v1. Revocation of a PAT is bounded by the 1-hour JWT TTL.

Auth service **does** subscribe to:

- `WorkspaceDeleted` — cascades to all PATs whose `workspace_id` matches that workspace.
- `SessionStatusChanged` (where `new_status = STATUS_STOPPED`) — auto-revokes the session PAT (`source = SOURCE_SYSTEM`)
  bound to that session.
- `UserDeleted` (future) — cascades to all PATs owned by that user.

## Entities

### Scope (shared enum)

v1 uses a single `SCOPE_ALL` value — no scope enforcement is done at runtime. Granular scopes are reserved for v2.

```protobuf
enum Scope {
  SCOPE_ALL = 0;
}
```

See [Future Scopes (v2)](#future-scopes-v2--not-implemented-in-v1) for the reserved value list.

### Pat (Personal Access Token)

User-level. Optionally bound to a single workspace — `workspace_id` controls which workspace the token can be used for
(empty = any workspace the owner belongs to).

`GetPats` always excludes `SOURCE_SYSTEM` PATs — they are never visible in the FE UI.

```protobuf
message Pat {
  enum Source {
    SOURCE_USER   = 0;  // created by user via CreatePat; visible in FE UI
    SOURCE_SYSTEM = 1;  // created by platform via IssueSessionPat; excluded from GetPats
  }

  string                    pat_id = 1;
  // Empty = no restriction. Non-empty = only exchangeable for this workspace.
  string                    workspace_id = 2;
  .common.v1.Nrn            owner_nrn = 3;
  string                    name = 4;
  string                    description = 5;
  string                    token_hash = 6;   // HMAC-SHA256(raw_token, server_pepper)
  string                    masked_value = 7;
  repeated Scope            scopes = 8;
  google.protobuf.Timestamp expires_at = 9;   // optional
  google.protobuf.Timestamp last_used_at = 10;
  .common.v1.Nrn            created_by_nrn = 11;
  google.protobuf.Timestamp created_at = 12;
  .common.v1.Nrn            updated_by_nrn = 13;
  google.protobuf.Timestamp updated_at = 14;
  bool                      is_deleted = 15;
  .common.v1.DeletionInfo   deletion_info = 16;
  Source                    source = 17;
  // Only set for SOURCE_SYSTEM. Used to find and revoke the PAT on SessionStopped.
  string                    session_id = 18;
}
```

#### Field notes

- **Raw token format.** `nweb_pat_<32-byte random, base62-encoded>`.
- **`token_hash`.** `HMAC-SHA256(raw_token, server_pepper)`, hex-encoded. Deterministic, uniquely indexed.
- **`workspace_id`.** Empty means no restriction — valid for any workspace the `owner_nrn` belongs to. `ExchangePat`
  validates membership at exchange time.
- **`source`.** `SOURCE_USER` for user-created PATs; `SOURCE_SYSTEM` for session PATs created by `IssueSessionPat`.
- **`session_id`.** Set only on `SOURCE_SYSTEM` PATs. The auth service listens to `SessionStatusChanged` (stopped) and
  soft-deletes matching PATs.
- **Soft delete.** `RevokePat` sets `is_deleted = true`. `ExchangePat` rejects deleted PATs immediately.

## Token Formats & Claims

### Access token (from `ExchangePat`)

```
{
  "iss": "https://auth.nweb.xyz",
  "sub": "<user_nrn>",
  "aud": "nweb-api",
  "exp": <now + 1h>,
  "iat": <now>,
  "jti": "<uuid>",
  "nweb:workspace_id": "<workspace_id from request>",
  "nweb:scopes": ["all"],
  "nweb:token_type": "access",
  "nweb:source_pat_id": "<pat_uuid>"
}
```

**Scope serialization rule.** Strip `SCOPE_` prefix, lowercase: `SCOPE_ALL` → `"all"`.

### Key rotation

Auth service generates a new RSA signing keypair every 90 days. The JWKS endpoint publishes both the current and the
previous public keys for a 48-hour overlap window. Cloud Endpoints refetches JWKS every ~5 minutes.

## RPC Surface

### Service Definition (6 RPCs)

```protobuf
service AuthService {
  // === PAT management (FE, Firebase-authed) ===
  rpc GetPats(PatsRequest) returns (PatsResponse)
  rpc CreatePat(CreatePatRequest) returns (CreatePatResponse)
  rpc RevokePat(.common.v1.Id) returns (.common.v1.Empty)

  // === Token exchange (CLI and sandbox programs — unauth endpoint) ===
  rpc ExchangePat(PatExchangeRequest) returns (AccessToken)

  // === JWKS (public, no auth) ===
  rpc GetJwks(.common.v1.Empty) returns (google.api.HttpBody)

  // === Session PAT (BE-BE only — sandbox service only) ===
  rpc IssueSessionPat(IssueSessionPatRequest) returns (IssueSessionPatResponse)
}
```

Plus one **HTTP-only** endpoint (not a gRPC RPC): `GET /.well-known/jwks.json`.

### Request / Response Messages

```protobuf
// --- Pat listing ---
message PatsRequest {
  message Filter {
    string          workspace_id = 1;   // filter to PATs valid for this workspace
    repeated string pat_ids = 2;
    repeated Scope  has_scopes = 3;
  }
  enum Sort { DEFAULT = 0; CREATED_AT_ASC = 1; CREATED_AT_DESC = 2;
              LAST_USED_AT_ASC = 3; LAST_USED_AT_DESC = 4; }
  Filter                filter = 1;
  Sort                  sort = 2;
  .common.v1.Pagination pagination = 3;
}
message PatsResponse {
  repeated Pat pats = 1;   // SOURCE_SYSTEM tokens always excluded; token_hash always stripped
}

// --- Pat create ---
message CreatePatRequest {
  string                    workspace_id = 1;    // optional; empty = all workspaces
  string                    name = 2;
  string                    description = 3;
  repeated Scope            scopes = 4;
  google.protobuf.Timestamp expires_at = 5;       // optional
}
message CreatePatResponse {
  Pat    pat = 1;         // token_hash stripped
  string raw_token = 2;  // "nweb_pat_..." — returned once, never again
}

// --- Pat exchange (CLI / sandbox program → AccessToken) ---
message PatExchangeRequest {
  string token        = 1;  // raw "nweb_pat_..."
  string workspace_id = 2;  // workspace to scope the minted JWT for
}
message AccessToken {
  string                    jwt = 1;
  google.protobuf.Timestamp expires_at = 2;
}

// --- Session PAT (BE-BE only) ---
message IssueSessionPatRequest {
  .common.v1.Nrn user_nrn     = 1;  // session owner — sub in minted JWTs
  string         session_id   = 2;
  string         workspace_id = 3;
  string         name         = 4;
  string         description  = 5;
  repeated Scope scopes       = 6;
  google.protobuf.Timestamp expires_at = 7;  // optional; absent = no expiry
}
message IssueSessionPatResponse {
  string                    raw_token  = 1;  // "nweb_pat_..." — inject as NWEB_API_KEY; never log
  google.protobuf.Timestamp expires_at = 2;
}
```

### ExchangePat validation sequence

`ExchangePat` is an **unauthenticated endpoint** — caller identity comes from the PAT row, not the request:

1. `HMAC-SHA256(token, server_pepper)` → look up by `token_hash`
2. Reject if not found, `is_deleted`, or `expires_at` passed
3. If `Pat.workspace_id` is non-empty: assert `workspace_id == Pat.workspace_id`
4. Assert `Pat.owner_nrn` is a member of `workspace_id`
5. Update `last_used_at`
6. Mint and return `AccessToken` JWT with `nweb:workspace_id = workspace_id`

## Flows

### Flow A — FE creates a PAT

```
1. User: Settings → Personal Access Tokens → "New Token"
2. User picks: name, description, scopes, optional workspace restriction, optional expiry
3. FE → auth.CreatePat                         (Firebase JWT)
4. Auth svc:
   - gen "nweb_pat_" + 32B random base62
   - HMAC-SHA256(raw, server_pepper) → token_hash
   - compute masked_value
   - store Pat{workspace_id, owner_nrn, name, scopes, source=SOURCE_USER, ...}
   - return Pat (hash stripped) + raw_token
5. FE: show raw_token in modal — unrecoverable after close
```

### Flow B — CLI auth (v1: PAT via FE)

```
1. User: Settings → Personal Access Tokens → "New Token"
2. User picks: name, description, scopes, optional workspace restriction, optional expiry
3. FE → auth.CreatePat (Firebase JWT)
4. Auth service generates nweb_pat_... raw token, returns it once
5. User copies raw token to ~/.config/retask/credentials.toml

CLI hot path:
6. On each command: ExchangePat(token, workspace_id) → 1-hour JWT → API call
7. At ~50-min mark: CLI proactively refreshes before the 1-hour expiry
```

### Flow C — PAT exchange (hot path on CLI / sandbox program refresh)

```
1. If cached access_token not expired, skip. Else:
2. → auth.ExchangePat(raw_pat, workspace_id)    // unauth endpoint
3. Auth svc: validate → update last_used_at → sign AccessToken JWT
   ← AccessToken{ jwt, expires_at = now + 1h }
4. Caller caches, uses jwt as Authorization: Bearer for all subsequent calls
```

### Flow D — Sandbox session tokens (ConnectSession + GetSessionRuntime)

```
WebSocket URL token (2-min):
1. FE → sandbox.ConnectSession(session_id)         (Firebase JWT)
2. Sandbox svc → auth.IssueSessionPat{
     user_nrn, session_id, workspace_id,
     expires_after_seconds=120 }                   (GCP service-account JWT, BE-BE)
3. Auth svc: creates SOURCE_SYSTEM Pat{..., expires_at=now+2min}
   ← IssueSessionPatResponse{ raw_token, expires_at }
4. Sandbox svc returns SessionConnectionResponse{
     url: "wss://proxy.nweb.xyz/ws?token=<raw_token>", expires_at }
5. FE: new WebSocket(response.url)
6. Proxy on connect:
   - HMAC-SHA256(token, server_pepper) → look up by token_hash
   - Reject if not found, is_deleted, or expires_at passed
   - Assert source=SOURCE_SYSTEM and session_id matches
   - Accept connection

NWEB_API_KEY injection:
7. FE → sandbox.GetSessionRuntime(session_id)      (Firebase JWT)
8. Sandbox svc → auth.IssueSessionPat{
     user_nrn, session_id, workspace_id,
     expires_after_seconds=<session lifetime> }    (GCP service-account JWT, BE-BE)
9. Auth svc: creates SOURCE_SYSTEM Pat{..., expires_at=now+<session lifetime>}
   ← IssueSessionPatResponse{ raw_token, expires_at }
10. Sandbox svc includes raw_token as NWEB_API_KEY in Sandbox.Config.EnvVar
11. CLI inside container: ExchangePat(NWEB_API_KEY, workspace_id) → 1-hour JWT → API calls
12. SessionStatusChanged(new_status=STOPPED) event → auth service:
    - soft-deletes all Pat where session_id matches and source=SOURCE_SYSTEM
13. Next ExchangePat call with either session PAT returns UNAUTHENTICATED
```

## Access Control

### Who can call what

| RPC                      | Caller identity          | Enforced by                                                                |
|--------------------------|--------------------------|----------------------------------------------------------------------------|
| `GetPats`, `CreatePat`, `RevokePat` | End user via Firebase ID | Auth service — filters `owner_nrn == caller`; SOURCE_SYSTEM excluded |
| `ExchangePat`            | Anyone with a raw PAT    | Auth service — hash verify + workspace membership + `is_deleted`/`expires_at` |
| `IssueSessionPat`        | Service account          | Auth service — allowlist of service-account emails (sandbox only in v1). **NOT callable by end users, FE, or CLI.** |

### PAT visibility and ownership

- A PAT is visible via `GetPats` only to its `owner_nrn`. Even workspace OWNER/ADMIN cannot see another user's PATs.
- `RevokePat` is callable only by `owner_nrn`.
- `SOURCE_SYSTEM` PATs are never returned by `GetPats`.

### Scope enforcement at the gateway

The gateway extracts `nweb:scopes` from the verified access JWT and checks simple set membership:
`required_scope ∈ token.nweb:scopes`. There is no runtime hierarchy or implicit expansion.

## Events & Cascade

### Events published (v1)

None.

### Events consumed

**`WorkspaceDeleted` → Auth Service:**

- Soft-delete all `Pat` where `workspace_id` matches the deleted workspace ID.
- `deletion_info`: `source = CASCADE`, `parent_nrn = workspace NRN`.
- `ExchangePat` for any affected PAT immediately begins returning `UNAUTHENTICATED`.

**`SessionStatusChanged` (where `new_status = STATUS_STOPPED`) → Auth Service:**

- Find `Pat` where `session_id` matches and `source = SOURCE_SYSTEM`.
- Soft-delete: `is_deleted = true`, `deletion_info { source: CASCADE, parent_nrn: session NRN }`.
- Next `ExchangePat` call with the session PAT returns `UNAUTHENTICATED`.

**`UserDeleted` → Auth Service (future, not v1):**

- Soft-delete all `Pat` where `owner_nrn` matches.

## Implementation Notes

- **Server pepper for `token_hash`.** 32-byte random secret in GCP Secret Manager, loaded at boot. Never stored in DB.
- **JWT signing key.** Private key in GCP Secret Manager. Loaded at boot, reloaded on rotation.
- **JWKS caching.** ESP v2 fetches at boot, refreshes every ~5 min. Must be low-latency and highly available.
- **Rate limiting.** `ExchangePat` is public; must be rate-limited at the gateway (per-IP + per-token).
- **`RevokePat` uses custom action syntax** (`POST /v1/auth/pats/{id}:revoke`).
- **Session PAT scopes.** `IssueSessionPat` always grants the full scope set server-side — callers cannot specify scopes.

## Out of Scope for v1

- Device auth flow (OAuth device flow for CLI login) — v2.
- Workspace API keys (org-level credentials, not user-bound) — v2.
- User entity management.
- Replacing Firebase as the FE identity provider.
- Service-account token issuance (auth service consumes GCP service-account JWTs; it does not mint its own).
- Sub-1-hour PAT revocation via pubsub denylist.
- Per-RPC scope-enforcement proto option.
- Granular per-resource scopes (see below).

## Future Scopes (v2 — not implemented in v1)

The following scope values are reserved. They will be re-added to the proto when granular scope enforcement is implemented.

```
// --- workspace (100–119) ---
SCOPE_WORKSPACE_READ = 100
SCOPE_WORKSPACE_EDIT = 101
SCOPE_WORKSPACE_MEMBER_READ = 102
SCOPE_WORKSPACE_MEMBER_EDIT = 103
SCOPE_WORKSPACE_BILLING_READ = 104
SCOPE_WORKSPACE_BILLING_EDIT = 105

// --- retask: project (200–209) ---
SCOPE_PROJECT_READ = 200
SCOPE_PROJECT_EDIT = 201
SCOPE_PROJECT_MEMBER_READ = 202
SCOPE_PROJECT_MEMBER_EDIT = 203

// --- retask: task (210–219) ---
SCOPE_TASK_READ = 210
SCOPE_TASK_EDIT = 211

// --- retask: sandbox (220–239) ---
SCOPE_SANDBOX_READ = 220
SCOPE_SANDBOX_EDIT = 221
SCOPE_SANDBOX_TEMPLATE_READ = 222
SCOPE_SANDBOX_TEMPLATE_EDIT = 223
SCOPE_SESSION_READ = 224
SCOPE_SESSION_EDIT = 225

// --- retask: agent (240–249) ---
SCOPE_AGENT_READ = 240
SCOPE_AGENT_EDIT = 241
```
