# Auth v1 Simplification — Design Spec

**Date:** 2026-06-05
**Scope:** `proto/auth/v1/auth.proto` + `docs/architecture/auth.md`

## Overview

Three targeted simplifications to auth v1:

1. Remove `IssueOneTimeToken`; extend `IssueSessionPat` with a configurable TTL so sandbox service covers both the WebSocket URL token and the `NWEB_API_KEY` injection from a single RPC.
2. Collapse the `Scope` enum to a single `SCOPE_ALL = 0` value; reserve granular scopes for v2.
3. Extend access token TTL from 15 minutes to 1 hour.

---

## Change 1 — Remove `IssueOneTimeToken`, extend `IssueSessionPat`

### What's removed

- `rpc IssueOneTimeToken` and its HTTP annotation
- `OneTimeTokenRequest` message
- `OneTimeTokenResponse` message
- All references to `nweb:token_type = "websocket"`, `aud = "sandbox-proxy"`, and jti replay enforcement

### What changes in `IssueSessionPatRequest`

Add `expires_after_seconds uint32`. The auth service mints a raw PAT (`nweb_pat_...`) with `expires_at = now + expires_after_seconds`. Callers pass `0` to get a server-defined default (same as the current session PAT lifetime).

```protobuf
message IssueSessionPatRequest {
  common.v1.Nrn user_nrn            = 1;
  string         session_id         = 2;
  string         workspace_id       = 3;
  uint32         expires_after_seconds = 4;  // 0 = server default
}
```

`IssueSessionPatResponse` is unchanged — returns `raw_token` and `expires_at`.

### Sandbox calling pattern

Sandbox service calls `IssueSessionPat` twice per session:

| Call site | `expires_after_seconds` | Purpose |
|---|---|---|
| `sandbox.ConnectSession` | `120` | 2-min raw PAT embedded in WebSocket URL |
| `sandbox.GetSessionRuntime` | session lifetime (e.g. `3600`) | Long-lived raw PAT returned as `NWEB_API_KEY` in `Sandbox.Config.EnvVar` |

### WebSocket URL token flow (updated)

```
1. FE → sandbox.ConnectSession(session_id)
2. Sandbox → auth.IssueSessionPat{ user_nrn, session_id, workspace_id, expires_after_seconds=120 }
3. Auth returns raw_token (2-min PAT), expires_at
4. Sandbox returns SessionConnectionResponse{ url: "wss://proxy.nweb.xyz/ws?token=<raw_token>", expires_at }
5. FE: new WebSocket(response.url)
6. Proxy on connect:
   - HMAC-SHA256(token, server_pepper) → look up by token_hash
   - Reject if not found, is_deleted, or expires_at passed
   - Assert source = SOURCE_SYSTEM and session_id matches
   - Accept connection
```

No jti replay cache. The 2-min TTL plus `is_deleted` are the sole protection.

### NWEB_API_KEY injection flow (updated)

```
1. FE → sandbox.GetSessionRuntime(session_id)
2. Sandbox → auth.IssueSessionPat{ user_nrn, session_id, workspace_id, expires_after_seconds=<session lifetime> }
3. Auth returns raw_token (long-lived PAT), expires_at
4. Sandbox returns runtime response; Sandbox.Config.EnvVar includes NWEB_API_KEY = raw_token
5. CLI inside container: ExchangePat(NWEB_API_KEY, workspace_id) → 1-hour JWT → API calls
```

---

## Change 2 — Collapse `Scope` enum to `SCOPE_ALL`

### Proto change

```protobuf
enum Scope {
  // v1: single value — all permissions granted implicitly.
  // Granular per-resource scopes are reserved for v2; see auth.md.
  SCOPE_ALL = 0;
}
```

All other values (`SCOPE_WORKSPACE_READ`, `SCOPE_PROJECT_READ`, etc.) are removed from the proto file and documented in a "Future Scopes (v2)" section in `auth.md`.

### Enforcement rule

v1 does no scope enforcement. Any valid access JWT is accepted at the gateway regardless of which scopes it nominally carries. Granular scope checking is deferred to v2 when the scope set is stable.

### Impact on PAT create / exchange

- `CreatePatRequest.scopes` can be omitted (empty = `SCOPE_ALL` implied).
- `ExchangePat` mints JWTs with `nweb:scopes: ["all"]` (strip `SCOPE_` prefix, lowercase).
- `IssueSessionPat` continues to grant the full scope set server-side.

---

## Change 3 — Access token TTL: 15 min → 1 hour

Access tokens minted by `ExchangePat` have `exp = now + 1h`, matching Firebase ID token TTL.

| Token | Old TTL | New TTL |
|---|---|---|
| `ExchangePat` access JWT | 15 min | 1 hour |
| Session PAT (2-min WS token) | n/a (new) | 2 min |
| Session PAT (NWEB_API_KEY) | no expiry (server default) | caller-specified |

The CLI's proactive refresh moves from the ~12-min mark to the ~50-min mark.

---

## What Is Not Changing

- `GetPats`, `CreatePat`, `RevokePat`, `ExchangePat`, `GetJwks` — unchanged RPCs
- The raw PAT format (`nweb_pat_<32-byte base62>`) and `token_hash` derivation
- `SOURCE_SYSTEM` PAT visibility rules (excluded from `GetPats`)
- `WorkspaceDeleted` and `SessionStatusChanged` cascade behaviour
- JWT signing, JWKS endpoint, two-issuer Cloud Endpoints setup

---

## Future Scopes (v2 — not implemented in v1)

The following scope values are reserved. They will be added back to the proto when granular scope enforcement is implemented.

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
