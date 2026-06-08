# Auth v1 Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify auth v1 by removing `IssueOneTimeToken`, unifying session token issuance under `IssueSessionPat`, collapsing `Scope` to `SCOPE_ALL = 0`, and extending access token TTL to 1 hour.

**Architecture:** All changes are confined to `proto/auth/v1/auth.proto` and `docs/architecture/auth.md`. No code generation command exists in this repo — downstream service repos run generation after pulling the subtree. Changes are purely proto + documentation.

**Tech Stack:** Protocol Buffers (proto3), Markdown

---

### Task 1: Simplify Scope enum

**Files:**
- Modify: `proto/auth/v1/auth.proto`

- [ ] **Step 1: Replace the Scope enum**

In `proto/auth/v1/auth.proto`, replace everything from the `// ============================================================================` comment above `enum Scope {` through the closing `}` (lines 63–107) with:

```protobuf
// ============================================================================
// Scope (top-level enum — values prefixed per proto style guide)
// ============================================================================

// v1: single value — all permissions granted implicitly.
// Granular per-resource scopes are reserved for v2;
// see docs/architecture/auth.md for the reserved value list.
enum Scope {
  SCOPE_ALL = 0;
}
```

- [ ] **Step 2: Verify the diff**

```bash
git diff proto/auth/v1/auth.proto
```

Expected: The 30-line scope block is gone; only `SCOPE_ALL = 0` remains inside the enum.

- [ ] **Step 3: Commit**

```bash
git add proto/auth/v1/auth.proto
git commit -m "feat(auth): collapse Scope enum to SCOPE_ALL for v1"
```

---

### Task 2: Remove IssueOneTimeToken; extend IssueSessionPatRequest

**Files:**
- Modify: `proto/auth/v1/auth.proto`

- [ ] **Step 1: Remove the IssueOneTimeToken RPC from the service block**

Delete these lines from the `AuthService` service definition:

```protobuf
  // === One-time token (BE-BE, service auth — sandbox service only) ===
  rpc IssueOneTimeToken(OneTimeTokenRequest) returns (OneTimeTokenResponse) {
    option (google.api.http) = {
      post: "/v1/auth/one-time-tokens"
      body: "*"
    };
  }

```

The blank line before `// === Session PAT` stays for spacing.

- [ ] **Step 2: Remove the OneTimeToken messages**

Delete the entire block:

```protobuf
// ============================================================================
// Request / Response — One-time token (BE-BE)
// ============================================================================

message OneTimeTokenRequest {
  .common.v1.Nrn user_nrn = 1;
  string session_id = 2;
  // E.g. "sandbox-proxy".
  string audience = 3;
  // Capped server-side at 120.
  uint32 ttl_seconds = 4;
}

message OneTimeTokenResponse {
  string jwt = 1;
  google.protobuf.Timestamp expires_at = 2;
}

```

- [ ] **Step 3: Add expires_after_seconds to IssueSessionPatRequest**

Replace:

```protobuf
message IssueSessionPatRequest {
  // Session owner — becomes the sub claim in JWTs minted from this PAT.
  .common.v1.Nrn user_nrn     = 1;
  string         session_id   = 2;
  string         workspace_id = 3;
}
```

With:

```protobuf
message IssueSessionPatRequest {
  // Session owner — becomes the sub claim in JWTs minted from this PAT.
  .common.v1.Nrn user_nrn              = 1;
  string         session_id            = 2;
  string         workspace_id          = 3;
  // 0 = server-defined default. Auth service may enforce a maximum.
  uint32         expires_after_seconds = 4;
}
```

- [ ] **Step 4: Verify the diff**

```bash
git diff proto/auth/v1/auth.proto
```

Expected: `IssueOneTimeToken` RPC gone from service block, `OneTimeTokenRequest` / `OneTimeTokenResponse` messages deleted, `expires_after_seconds` field present in `IssueSessionPatRequest`.

- [ ] **Step 5: Commit**

```bash
git add proto/auth/v1/auth.proto
git commit -m "feat(auth): remove IssueOneTimeToken; add expires_after_seconds to IssueSessionPatRequest"
```

---

### Task 3: Update auth.md — Scope section

**Files:**
- Modify: `docs/architecture/auth.md`

- [ ] **Step 1: Replace the Scope enum block**

In `docs/architecture/auth.md`, replace the entire `### Scope (shared enum)` section — from `### Scope (shared enum)` through the closing ` ``` ` of the protobuf block (lines 96–134):

```markdown
### Scope (shared enum)

v1 uses a single `SCOPE_ALL` value — no scope enforcement is done at runtime. Granular scopes are reserved for v2.

```protobuf
enum Scope {
  SCOPE_ALL = 0;
}
```

See [Future Scopes (v2)](#future-scopes-v2--not-implemented-in-v1) for the reserved value list.
```

- [ ] **Step 2: Add Future Scopes section at the bottom of the file**

Append to the end of `docs/architecture/auth.md`:

```markdown

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
```

- [ ] **Step 3: Verify**

```bash
git diff docs/architecture/auth.md
```

Expected: the old 30-value scope enum replaced by 4 lines; Future Scopes section appended at bottom.

- [ ] **Step 4: Commit**

```bash
git add docs/architecture/auth.md
git commit -m "docs(auth): collapse Scope to SCOPE_ALL; add v2 reserved scope list"
```

---

### Task 4: Update auth.md — Token Formats section

**Files:**
- Modify: `docs/architecture/auth.md`

- [ ] **Step 1: Update access token TTL from 15 min to 1 hour**

In the `### Access token (from ExchangePat)` block, replace:

```
  "exp": <now + 15min>,
```

With:

```
  "exp": <now + 1h>,
```

- [ ] **Step 2: Remove the one-time token section**

Delete the entire `### One-time token (from IssueOneTimeToken)` section:

```markdown
### One-time token (from `IssueOneTimeToken`)

```
{
  "iss": "https://auth.nweb.xyz",
  "sub": "<user_nrn>",
  "aud": "sandbox-proxy",
  "exp": <now + 60s>,
  "iat": <now>,
  "jti": "<uuid>",
  "nweb:session_id": "<session_uuid>",
  "nweb:token_type": "websocket"
}
```
```

- [ ] **Step 3: Verify**

```bash
git diff docs/architecture/auth.md
```

Expected: `15min` → `1h` in access token claims; one-time token JWT block gone.

- [ ] **Step 4: Commit**

```bash
git add docs/architecture/auth.md
git commit -m "docs(auth): update access token TTL to 1h; remove one-time token JWT format"
```

---

### Task 5: Update auth.md — RPC Surface section

**Files:**
- Modify: `docs/architecture/auth.md`

- [ ] **Step 1: Update the service definition heading and block**

Replace:

```markdown
### Service Definition (7 RPCs)

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

  // === One-time token (BE-BE, service auth — sandbox service only) ===
  rpc IssueOneTimeToken(OneTimeTokenRequest) returns (OneTimeTokenResponse)

  // === Session PAT (BE-BE only — sandbox service only) ===
  rpc IssueSessionPat(IssueSessionPatRequest) returns (IssueSessionPatResponse)
}
```
```

With:

```markdown
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
```

- [ ] **Step 2: Remove the OneTimeToken messages from the Request/Response section**

Delete the entire `--- One-time token (BE-BE) ---` block:

```markdown
// --- One-time token (BE-BE) ---
message OneTimeTokenRequest {
  .common.v1.Nrn user_nrn    = 1;
  string         session_id  = 2;
  string         audience    = 3;    // e.g. "sandbox-proxy"
  uint32         ttl_seconds = 4;    // capped server-side at 120
}
message OneTimeTokenResponse {
  string                    jwt = 1;
  google.protobuf.Timestamp expires_at = 2;
}
```

- [ ] **Step 3: Update IssueSessionPatRequest in the messages section**

Replace:

```protobuf
// --- Session PAT (BE-BE only) ---
message IssueSessionPatRequest {
  .common.v1.Nrn user_nrn     = 1;  // session owner — sub in minted JWTs
  string         session_id   = 2;
  string         workspace_id = 3;
}
```

With:

```protobuf
// --- Session PAT (BE-BE only) ---
message IssueSessionPatRequest {
  .common.v1.Nrn user_nrn              = 1;  // session owner — sub in minted JWTs
  string         session_id            = 2;
  string         workspace_id          = 3;
  uint32         expires_after_seconds = 4;  // 0 = server-defined default
}
```

- [ ] **Step 4: Verify**

```bash
git diff docs/architecture/auth.md
```

Expected: RPC count 7→6, `IssueOneTimeToken` gone from service block, `OneTimeToken*` messages deleted, `expires_after_seconds` in `IssueSessionPatRequest`.

- [ ] **Step 5: Commit**

```bash
git add docs/architecture/auth.md
git commit -m "docs(auth): remove IssueOneTimeToken from RPC surface; update IssueSessionPatRequest"
```

---

### Task 6: Update auth.md — Flows and Access Control

**Files:**
- Modify: `docs/architecture/auth.md`

- [ ] **Step 1: Update Flow B — CLI refresh timing**

Replace:

```
7. At ~12-min mark: CLI proactively refreshes before the 15-min expiry
```

With:

```
7. At ~50-min mark: CLI proactively refreshes before the 1-hour expiry
```

- [ ] **Step 2: Update Flow C — TTL references**

Replace:

```
   ← AccessToken{ jwt, expires_at = now + 15min }
4. Caller caches, uses jwt as Authorization: Bearer for all subsequent calls
```

With:

```
   ← AccessToken{ jwt, expires_at = now + 1h }
4. Caller caches, uses jwt as Authorization: Bearer for all subsequent calls
```

- [ ] **Step 3: Remove Flow D (one-time token)**

Delete the entire `### Flow D — One-time token (sandbox → auth, BE-BE)` section:

```markdown
### Flow D — One-time token (sandbox → auth, BE-BE)

```
1. FE → sandbox.ConnectSession(session_id)     (Firebase JWT)
2. Sandbox svc → auth.IssueOneTimeToken{
     user_nrn, session_id, audience="sandbox-proxy", ttl_seconds=60 }
                                                (GCP service-account JWT, BE-BE)
3. Auth svc: sign WS JWT (aud="sandbox-proxy", 60s exp, jti)
   ← OneTimeTokenResponse{ jwt, expires_at }
4. Sandbox svc returns SessionConnectionResponse{
     url: "wss://proxy.nweb.xyz/ws?token=<jwt>", expires_at }
5. FE: new WebSocket(response.url)
6. Proxy on connect:
   - validate JWT via JWKS
   - assert aud=="sandbox-proxy"
   - assert jti not in replay cache; SET NX with TTL = exp-now
   - assert nweb:session_id matches
   - accept connection
```
```

- [ ] **Step 4: Replace Flow E with the updated two-call pattern**

Replace the entire `### Flow E — Sandbox session PAT (NWEB_API_KEY injection)` section:

```markdown
### Flow E — Sandbox session PAT (NWEB_API_KEY injection)

```
1. FE → sandbox.ConnectSession(session_id)       (Firebase JWT)
2. Sandbox svc → auth.IssueSessionPat{
     user_nrn, session_id, workspace_id, expires_after_seconds=120 }
                                                  (GCP service-account JWT, BE-BE)
3. Auth svc: creates SOURCE_SYSTEM Pat{..., expires_at=now+2min}
   ← IssueSessionPatResponse{ raw_token, expires_at }
4. Sandbox svc returns SessionConnectionResponse{
     url: "wss://proxy.nweb.xyz/ws?token=<raw_token>", expires_at }
5. FE: new WebSocket(response.url)
6. Proxy on connect:
   - HMAC-SHA256(token, server_pepper) → look up by token_hash
   - Reject if not found, is_deleted, or expires_at passed
   - Assert source = SOURCE_SYSTEM and session_id matches
   - Accept connection

NWEB_API_KEY injection (called when the session runtime is needed):
7. FE → sandbox.GetSessionRuntime(session_id)    (Firebase JWT)
8. Sandbox svc → auth.IssueSessionPat{
     user_nrn, session_id, workspace_id, expires_after_seconds=<session lifetime> }
9. Auth svc: creates SOURCE_SYSTEM Pat{..., expires_at=now+<session lifetime>}
   ← IssueSessionPatResponse{ raw_token, expires_at }
10. Sandbox svc includes raw_token as NWEB_API_KEY in Sandbox.Config.EnvVar
11. CLI inside container: ExchangePat(NWEB_API_KEY, workspace_id) → 1-hour JWT → API calls
12. SessionStatusChanged(new_status=STOPPED) event → auth service:
    - soft-deletes all Pat where session_id matches and source=SOURCE_SYSTEM
13. Next ExchangePat call with either session PAT returns UNAUTHENTICATED
```
```

- [ ] **Step 5: Update the Access Control table — remove IssueOneTimeToken row**

Replace:

```markdown
| `ExchangePat`            | Anyone with a raw PAT    | Auth service — hash verify + workspace membership + `is_deleted`/`expires_at` |
| `IssueOneTimeToken`      | Service account          | Auth service — allowlist of service-account emails (sandbox only in v1)    |
| `IssueSessionPat`        | Service account          | Auth service — allowlist of service-account emails (sandbox only in v1). **NOT callable by end users, FE, or CLI.** |
```

With:

```markdown
| `ExchangePat`            | Anyone with a raw PAT    | Auth service — hash verify + workspace membership + `is_deleted`/`expires_at` |
| `IssueSessionPat`        | Service account          | Auth service — allowlist of service-account emails (sandbox only in v1). **NOT callable by end users, FE, or CLI.** |
```

- [ ] **Step 6: Update the Architecture section — remove IssueOneTimeToken from the diagram**

Replace:

```
│  • PAT mgmt        │
│  • ExchangePat     │
│  • IssueOneTimeToken│
│  • IssueSessionPat │
```

With:

```
│  • PAT mgmt        │
│  • ExchangePat     │
│  • IssueSessionPat │
```

- [ ] **Step 7: Update Communication Rules table — remove IssueOneTimeToken**

Replace:

```markdown
| Sandbox Service | Auth Service        | GCP service-account JWT | `IssueOneTimeToken`, `IssueSessionPat`                        |
```

With:

```markdown
| Sandbox Service | Auth Service        | GCP service-account JWT | `IssueSessionPat`                                             |
```

- [ ] **Step 8: Verify the full diff**

```bash
git diff docs/architecture/auth.md
```

Expected: Flow D section gone, Flow E rewritten with two `IssueSessionPat` calls, `IssueOneTimeToken` removed from access control table and communication rules table, architecture diagram updated, TTLs updated.

- [ ] **Step 9: Commit**

```bash
git add docs/architecture/auth.md
git commit -m "docs(auth): update flows and access control for IssueOneTimeToken removal"
```
