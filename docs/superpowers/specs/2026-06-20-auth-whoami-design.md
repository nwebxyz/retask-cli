# Design: `retask auth whoami` enrichment

**Date:** 2026-06-20
**Status:** Approved

## Summary

Enrich `retask auth whoami` to return identity and workspace membership information beyond what is stored locally in the profile. The command will parse the JWT claims and call `GetWorkspaceMembers` to produce a complete picture of who the caller is within the active workspace.

## Motivation

The current `whoami` output (`workspace_id`, `jwt_expires`, `endpoint`) is insufficient for agents and operators who need to know their user NRN, their role, and their membership status before acting on workspace resources.

## Output

```json
{
  "user_nrn": "nweb:auth:user:x079wLj5wKh9THofFK7gtEZCcps2",
  "workspace_id": "80541a07-90ce-469c-9d9c-d9d7d4508a38",
  "jwt_expires": "2026-06-20T...",
  "endpoint": "api.nweb.xyz:443",
  "workspace_member": {
    "nrn": "nweb:workspace:member:<workspace_member_id>",
    "role": "OWNER",
    "membership_status": "ACTIVE",
    "display_name": "Tan",
    "name": "Tan Hoai Nguyen",
    "email": "tan@nweb.co",
    "joined_at": "2026-01-15T..."
  }
}
```

`workspace_member` is omitted (`omitempty`) when the member record is not found (e.g. service-account token with no human member row).

### WorkspaceMember fields included

| Field | Source proto field | Why |
|---|---|---|
| `nrn` | `"nweb:workspace:member:" + workspace_member_id` | Identity for APIs requiring a member NRN |
| `role` | `role` enum → string | Permission level |
| `membership_status` | `membership_status` enum → string | ACTIVE vs PENDING |
| `display_name` | `display_name` | Workspace-admin-set name |
| `name` | `member_profile.name` | Real name cached from Customer service |
| `email` | `member_profile.email` | Email for identity confirmation |
| `joined_at` | `joined_at` | When the member became ACTIVE |

Fields omitted: `invited_by_nrn`, `updated_by_nrn`, `workspace_id` (redundant), `photo`, `created_at`, `updated_at`.

## Components

### 1. `internal/auth/claims.go` (new file)

```go
type Claims struct {
    Sub         string `json:"sub"`                // full user NRN
    WorkspaceID string `json:"nweb:workspace_id"`
    SourcePatID string `json:"nweb:source_pat_id"`
}

func ParseClaims(jwt string) (Claims, error)
```

Implementation: split on `.`, base64url-decode index 1 (the payload segment), unmarshal into `Claims`. Uses stdlib `encoding/base64` (RawURLEncoding) and `encoding/json` — no new dependencies. Does not verify the signature (the token was already accepted by the server).

`claims.Sub` is the full user NRN (`nweb:auth:user:<id>`), used directly as `user_nrn`.

### 2. `internal/cmd/auth/command.go` — `newWhoamiCommand`

Flow:

1. Load profile (`loadProfile`)
2. `resolver.Token(ctx)` → `jwt` (same resolver pattern as every other command; handles env, cache, and PAT exchange)
3. `auth.ParseClaims(jwt)` → `claims`
4. Resolve `workspaceID`: `profile.WorkspaceID` → `claims.WorkspaceID` → error
5. Connect to workspace service (inline connect pattern — same as every other command package)
6. Call `GetWorkspaceMembers(workspace_id, user_nrns=[claims.Sub])`
7. If response contains a member, map to `memberSnapshot`; otherwise leave nil
8. Print via `output.Print`

The workspace connect follows the standard connect pattern already used in every other command package. No shared helper is extracted — consistent with the codebase convention.

### 3. Output types (in `command.go`)

```go
type whoamiOutput struct {
    UserNrn         string          `json:"user_nrn"`
    WorkspaceID     string          `json:"workspace_id"`
    JWTExpires      string          `json:"jwt_expires"`
    Endpoint        string          `json:"endpoint"`
    WorkspaceMember *memberSnapshot `json:"workspace_member,omitempty"`
}

type memberSnapshot struct {
    Nrn              string `json:"nrn"`
    Role             string `json:"role"`
    MembershipStatus string `json:"membership_status"`
    DisplayName      string `json:"display_name,omitempty"`
    Name             string `json:"name,omitempty"`
    Email            string `json:"email,omitempty"`
    JoinedAt         string `json:"joined_at,omitempty"`
}
```

## Error handling

| Scenario | Behaviour |
|---|---|
| JWT missing (no profile, no env) | Error: "not logged in. Run: retask auth login" |
| JWT malformed (parse fails) | Error returned |
| `workspaceID` unresolvable | Error returned |
| `GetWorkspaceMembers` network error | Error returned |
| `GetWorkspaceMembers` returns 0 members | `workspace_member` omitted, no error |

## Out of scope

- `--fetch` / `--no-fetch` flag (not needed; network call is always appropriate for `whoami`)
- Surfacing `nweb:source_pat_id` or `nweb:token_type` from JWT claims (too internal)
- Caching the member lookup result
