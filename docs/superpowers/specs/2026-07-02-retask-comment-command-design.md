# `retask comment` command — design

**Date:** 2026-07-02
**Status:** Approved (pending spec review)
**Backend:** [nwebxyz/go#67](https://github.com/nwebxyz/go/pull/67) — `comment.v1.CommentService` (RETA-87). Contracts already merged/generated; `proto/comment/v1/comment.proto` and `proto-gen/comment/v1/` are present in this repo.

## Overview

Add a top-level `retask comment` command that wraps `comment.v1.CommentService`. v1 comments target **retask tasks only**. The command surface and attachment flow mirror the existing `retask task` command so operators and agents get a consistent experience.

## Backend surface (reference)

`comment.v1.CommentService` RPCs:

| RPC | Request → Response |
|---|---|
| `GetComments` | `CommentsRequest` → `CommentsResponse` (list) |
| `GetComment` | `common.v1.Id` → `Comment` |
| `SetComment` | `Comment` → `common.v1.Id` (create when `comment_id` empty; edit when set) |
| `DeleteComment` | `common.v1.Id` → `common.v1.Empty` |
| `AddCommentAttachment` | `AddCommentAttachmentRequest{comment_id, file_id}` → `Comment` |
| `DeleteCommentAttachment` | `DeleteCommentAttachmentRequest{comment_id, file_id}` → `Comment` |

Generated client: `commentv1connect.NewCommentServiceClient(httpClient, baseURL, opts...)`.

Key `Comment` fields: `comment_id`, `workspace_id`, `target_nrn` (`common.v1.Nrn`), `parent_comment_id`, `body` (HTML), `mentioned_member_nrns` (server-derived, read-only), `attachments` (`file.v1.FileInfo`, mutated only via attachment RPCs), `is_edited`, audit fields, `user_access` (computed on read).

`CommentsRequest.Filter`: `workspace_id` (**required scope**), `target_nrn` (**required scope**), `comment_ids[]`, `parent_comment_id`, `created_by_nrns[]`. Sort enum: `SORT_DEFAULT` (created_at DESC), `SORT_CREATED_AT_ASC`, `SORT_CREATED_AT_DESC`.

## Command surface

```
retask comment
  list                            GetComments   — comments on a task
  get      <comment-id>           GetComment
  create                          SetComment    — new comment (no id)
  update   <comment-id>           SetComment    — edit body of existing comment
  delete   <comment-id>           DeleteComment
  attachment
    add    <comment-id> <file-id> AddCommentAttachment
    remove <comment-id> <file-id> DeleteCommentAttachment
```

### Flags

| Command | Flags / args |
|---|---|
| `list` | `--task <task-id>` (**required**); `--parent-comment-id <comment-id>` (list replies under it; unset → top-level only); `--sort default\|created-asc\|created-desc`; `--created-by <member-nrn>` (repeatable). Requires workspace scope. |
| `create` | `--task <task-id>` (**required**); `--body <html>` (**required**); `--parent-comment-id <comment-id>` (reply to a top-level comment). Requires workspace scope. |
| `update` | positional `<comment-id>`; `--body <html>` (**required**). |
| `get` | positional `<comment-id>`. |
| `delete` | positional `<comment-id>`. |
| `attachment add` / `attachment remove` | positional `<comment-id> <file-id>`. |

## Behaviors and decisions

### Target NRN mapping
`--task <id>` maps to `common.v1.Nrn{Domain:"nweb", Service:"retask-task", ResourceType:"task", ResourceId:<id>}`. The domain is the constant `"nweb"` for this deployment; define it as a package-level const (`const domain = "nweb"`) with a small `taskNrn(id string) *commonv1.Nrn` helper so it is easy to change and unit-testable. No extra RPC is needed to build the NRN.

### Body input — raw passthrough
`--body` is stored **verbatim** as the `Comment.body` HTML. The CLI does no escaping or wrapping; the caller supplies valid HTML (TipTap `getHTML()` format). This is agent-first by design. Help text documents the format and the mention span syntax (see Documentation).

### Threading semantics
The server's `parent_comment_id` filter is a proto3 `string` with no presence, so "omit" and `""` are indistinguishable and both mean **top-level only**. The CLI therefore exposes exactly what the server supports:
- `comment list --task t1` → top-level comments only.
- `comment list --task t1 --parent-comment-id <cid>` → replies under `<cid>`.

There is no single "all comments (top-level + replies)" listing. This limitation is documented rather than hidden. On `create`, `--parent-comment-id` sets `Comment.parent_comment_id` to reply to a top-level comment (replies are one level deep — enforced server-side).

### `update` — body-only edit (no round-trip)
Confirmed against `comment/handler/handler.go`: when `SetComment` receives a `Comment` with a non-empty `comment_id`, the edit path loads the existing comment by ID, checks authorship, and applies **only `in.GetBody()`** (`UpdateComment(ctx, existing, in.GetBody(), user)`). `target_nrn`, `parent_comment_id`, and attachments are taken from the stored record and are not affected by the request.

Therefore `update` sends `SetComment(&Comment{CommentId: <id>, Body: <body>})` directly — no `GetComment`, no `workspace_id`, no `target_nrn`. The server flips `is_edited`.

### Workspace scope
`list` and `create` require the workspace ID (`Comment.workspace_id` / filter `workspace_id`). Resolve from `gf.WorkspaceID` (global `--workspace-id` / `NWEB_WORKSPACE_ID`). If unset, return a clear error, matching `task get-by-key`:
`--workspace-id is required (or set NWEB_WORKSPACE_ID)`.

### Output
| Command | Output |
|---|---|
| `create` | the returned `common.v1.Id` (`comment_id`) |
| `get` / `update` | the `Comment` |
| `attachment add` / `remove` | the returned `Comment` |
| `list` | the `[]Comment` array |
| `delete` | `{"status":"deleted","comment_id":"<id>"}` |

All output goes through `output.Print(gf.Pretty, …)`.

## Documentation (explicit requirement)

Each command's `Long` help, the `help-llm` manifest (`internal/cmd/helpcmd/command.go`), and the skill file (`skills/retask-cli.md`) must all clearly cover:

- **Body is HTML** stored verbatim (TipTap `getHTML()` format).
- **Mention syntax**: `<span data-type="mention" data-id="nweb:workspace:member:<uuid>">@Name</span>` embedded in `--body`; the server parses mentions from the body and populates `mentioned_member_nrns` (read-only; unknown mentions dropped).
- **Threading**: one level; `--parent-comment-id` replies to a top-level comment; list defaults to top-level only.
- **Attachments**: by file ID via `attachment add/remove`; file IDs come from `retask file` (there is no upload command, same as tasks).
- **Workspace scope** requirement for `list`/`create`.
- **Output fields** per command (follow the existing "Output fields: …" convention).

Each `Long` follows the repo's help template (one-line summary, usage examples, flags with values).

## Files touched

**New**
- `internal/cmd/comment/command.go` — package `comment`; `NewCommand(gf)`, `connect(gf)` via `commentv1connect`, all subcommands, `taskNrn` helper + `domain` const.
- `internal/cmd/comment/command_test.go` — unit tests for `taskNrn` mapping, sort-flag parsing, and workspace-required errors.

**Modified**
- `cmd/retask/main.go` — `root.AddCommand(commentcmd.NewCommand(gf))`.
- `internal/cmd/helpcmd/command.go` — manifest entries for `comment …`.
- `.bin/sync_proto.sh` (APPROVED) and `.bin/build_proto.sh` (APPROVED_SERVICES) — add `comment/v1` (proto already synced/generated; this keeps regeneration reproducible).
- `CLAUDE.md` — add `comment` row to the approved proto services table.
- `skills/retask-cli.md` — document `retask comment`.

## Testing

- `go build ./...` and `go test ./...` pass.
- Unit tests: `taskNrn` produces `nweb:retask-task:task:<id>`; `--sort` string → enum mapping (invalid value errors); missing-workspace error on `list`/`create`.
- Manual smoke against a live workspace (deferred to reviewer / follow-up): create → list (top-level) → reply via `--parent-comment-id` → list replies → update body (`is_edited` flips) → attachment add/remove → delete.

## Out of scope (v1)

- Dedicated `--mention` flag (documented raw-HTML embedding covers it).
- File upload (not present for tasks either).
- Commenting on non-task resources (backend is task-only in v1).
- Pagination flags on `list` (add later if needed; `CommentsRequest.pagination` exists).
