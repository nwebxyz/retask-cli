# Comment Service — Architecture Design

> Proto package: `comment.v1`
> Proto file: `proto/comment/v1/comment.proto`
> Global service. NRN: `nweb:comment:comment:<uuid>`.

## Overview

The comment service lets users attach threaded, rich-text comments to any target resource
identified by an NRN. v1 supports one target type — a retask task
(`nweb:retask-task:task:<uuid>`) — but the design is generic: a resource becomes commentable by
implementing one access-check RPC on its owning service.

Every read is scoped by **`workspace_id` + `target_nrn`** (both required). The comment service owns
no target ACLs; it verifies access by asking the target's owning service.

## Entities

### Comment

Rich-text body stored as an **HTML string** (TipTap `getHTML()` output, same format as
`task.description`). `@mentions` are inline `<span data-type="mention" data-id="<member nrn>"
data-label="<name>">@<name></span>` nodes; the server parses them into `mentioned_member_nrns`.
Attachments are `file.v1.FileInfo` snapshots, mutated only via the attachment RPCs. Standard audit
+ soft-delete fields apply. `user_access` (`auth.v1.UserAccess`) is computed on read: `can_edit`
when the caller is `created_by`, `can_delete` when the caller is `created_by` or has admin on the
target.

Threading is one level: `parent_comment_id` points at a top-level comment; a reply cannot be
replied to.

## RPCs

| RPC | HTTP | Notes |
|---|---|---|
| `GetComments` | `GET /v1/comments` | Filter requires `workspace_id` + `target_nrn`. |
| `GetComment` | `GET /v1/comments/{id}` | |
| `SetComment` | `POST /v1/comments` | Create (empty id, needs `can_view`) or edit (existing id, `created_by` only; sets `is_edited`). |
| `DeleteComment` | `DELETE /v1/comments/{id}` | `created_by` OR target admin; cascade-deletes replies. |
| `AddCommentAttachment` | `POST /v1/comments/{comment_id}/attachments` | `created_by` only. |
| `DeleteCommentAttachment` | `DELETE /v1/comments/{comment_id}/attachments/{file_id}` | `created_by` only. |

## Access control

On each access-sensitive call the service routes by `target_nrn.service` to the owning service and
calls its `Check<X>Permissions` RPC. For tasks: `retask.task.v1.CheckTaskPermissions` returns the
effective project `project.v1.MemberRole` per task; only accessible tasks are returned (absence ⇒
no view access). The service maps `effective_role` present ⇒ `can_view`, `MEMBER_ROLE_ADMIN` ⇒
`can_admin`.

**Service-account callers bypass the access check entirely** (full access), determined from the
auth context.

| Action | Requirement |
|---|---|
| read, create | `can_view` on target |
| edit, add/delete attachment | caller is `created_by` |
| delete | `created_by` OR `can_admin` on target |

## @mention format contract

The comment `body` is the single source of truth. A mention is serialized by the FE editor
(`@tiptap/extension-mention`) as exactly:

```html
<span data-type="mention" data-id="nweb:workspace:member:<uuid>" data-label="<name>">@<name></span>
```

`data-id` is the full workspace member NRN (consistent with `task.assignee_nrns`). On every write
the server parses all `data-type="mention"` spans, validates each `data-id` against workspace
membership, and stores the result in `mentioned_member_nrns`.

## Events

- **Publishes:** none in v1.
- **Consumes:** `retask.task.v1.event.TaskDeleted` → soft-deletes all comments with the matching
  task `target_nrn` (`source = CASCADE`, `parent_nrn = task NRN`).

## Out of scope for v1

Published comment events / mention-notification delivery; reactions; resolve/unresolve threads;
multi-level reply nesting; plaintext `body_text` projection; commentable targets other than tasks.
