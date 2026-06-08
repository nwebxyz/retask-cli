# Public Service — Architecture Design

> Proto package: `public.v1`
> Proto file: `proto/public/v1/public.proto`

## Overview

The `public` service is the single home for **unauthenticated, email-link-driven** RPCs. Users who are not logged in need to perform narrow actions from email links — the most immediate use case is responding to a workspace invitation — and those actions must be reachable without a Firebase JWT.

Rather than relaxing auth on individual endpoints across workspace, customer, and other domain services (one misconfiguration per future feature), public service concentrates the entire unauthenticated surface in one place. Every other service keeps its Firebase-authed gateway posture untouched.

Public service itself holds **no state**. It is a thin façade that validates the request shape and proxies to the owning domain service via BE-BE service-account auth.

### Scope (v1)

Workspace invitation flow only:

1. Preview an invitation from a link (`GetWorkspaceInvitation`).
2. Decline an invitation from a link (`DeclineWorkspaceInvitation`).

Other public-link flows (notification unsubscribe, etc.) are explicitly out of scope for v1 and will be added as additional RPCs on this same service when needed.

### Link format

```
https://<domain>/onboarding/w/<workspaceId>/invitations/<workspaceMemberId>
```

The URL carries the identifying pair `(workspace_id, workspace_member_id)` as path segments. Both are UUIDs, unguessable. Possession of the URL is the shallow credential — no signed token for v1.

**Implication:** `workspace_member_id` must be treated as a low-value secret on this path. Access logs on public service must scrub it; the FE invite page must not load third-party resources that would leak the URL via the `Referer` header.

## Model

There is no `WorkspaceInvitation` entity. A `WorkspaceMember` with `membership_status == PENDING` **is** the invitation. The public service operates on this existing entity; no schema changes are required.

## RPC Surface

### `public.v1.PublicService`

```protobuf
service PublicService {
  rpc GetWorkspaceInvitation(WorkspaceInvitationRequest)
      returns (WorkspaceInvitation) {
    option (google.api.http) = {
      get: "/v1/public/workspaces/{workspace_id}/invitations/{invitation_id}"
    };
  }

  rpc DeclineWorkspaceInvitation(WorkspaceInvitationRequest)
      returns (common.v1.Empty) {
    option (google.api.http) = {
      post: "/v1/public/workspaces/{workspace_id}/invitations/{invitation_id}:decline"
    };
  }
}

message WorkspaceInvitationRequest {
  string workspace_id  = 1;
  string invitation_id = 2;   // = WorkspaceMember.workspace_member_id
}

message WorkspaceInvitation {
  message Workspace {
    string workspace_id = 1;
    string name         = 2;
    string color        = 3;
  }

  string invitation_id                 = 1;
  workspace.v1.MembershipStatus status = 2;
  string invited_email                 = 3;
  Workspace workspace                  = 4;
}
```

Public-facing terminology uses "invitation" (`invitation_id`, `WorkspaceInvitation`); internally on workspace service the row is still a `WorkspaceMember`. The ID value is identical.

### Error behavior

All failure modes on both RPCs collapse to `NOT_FOUND` without leaking which check failed:

- Pair `(workspace_id, invitation_id)` does not match a row.
- Row is soft-deleted.
- `membership_status != PENDING` (already accepted, already declined).

This makes decline idempotent: calling it twice returns `Empty` the first time and `NOT_FOUND` the second. The FE treats both as success on the decline path.
