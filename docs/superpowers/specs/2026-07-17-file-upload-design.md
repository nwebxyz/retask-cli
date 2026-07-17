# `retask file upload` — design

Date: 2026-07-17
Status: approved, pending implementation

## Goal

Add an `upload` subcommand to the existing `retask file` command. It uploads a
local file to the file service's REST endpoint and, optionally, attaches it to
a task or a comment in the same invocation.

`retask file` already provides `list`, `get`, `delete`, and `signed-url`. Upload
is the missing verb. It cannot reuse the existing gRPC path: `FileService` has
no upload RPC, so bytes must go over REST.

## Background: the two upload scopes

The upload handler (`go/file/handler/handler_http.go`) branches on whether
`workspace_id` is present, and the two branches are the *only* legal shapes.

**Workspace scope** — `workspace_id` set. `target_nrn` is then **required**;
omitting it is a `400`:

```go
workspaceID := r.URL.Query().Get("workspace_id")
if workspaceID != "" {
    // Workspace upload: require a target and EDITOR permission on it.
    target = nrn.NewFromNrnString(r.URL.Query().Get("target_nrn"))
    if target == nil {
        model.SetJSONErrorResponse(w, "target_nrn is required when workspace_id is set", http.StatusBadRequest)
```

The caller needs EDITOR on the target. `resolveTargetAccess`
(`go/file/model/access.go:56`) rejects any target whose service is not
`retask-task`, so **a task NRN is the only legal workspace target today**.

**User scope** — `workspace_id` absent. The server forces the target to the
caller's own NRN (`go/file/model/model_http_upload_file.go:29-32`):

```go
// User file: no workspace => personal file targeted at the caller's own NRN.
if workspaceID == "" {
    target = identifier.GetIdentifier()
}
```

Storage keys differ (`users/<id>/...` vs `workspaces/...`), and access is
owner-only (`access.go:80-83`). Listing mirrors this: `GetFiles` with no
`workspace_id` force-filters `created_by_nrns` to the caller
(`handler/handler.go:99-105`).

There is **no third shape**. In particular there is no "workspace file attached
to nothing". The optional `targetNrn?` and its "omitted for pre-comment uploads"
comment in `nweb-app-frontend/workspaces/apis/file/apis/FileClient.ts` are
misleading: every real caller passes it, and omitting it while sending
`workspace_id` would 400.

Consequence: a bare `retask file upload <path>` has exactly one valid meaning
(personal upload). No flag is needed to select it and no ambiguity needs
guarding against.

## Upload is not presigned

The client POSTs bytes straight to the REST API, which streams them to storage
server-side. `GetFileSignedUrl` is a *download* entry point and is unrelated.

Attaching is a **separate gRPC call** after the upload, exactly as the frontend
does it (`usePendingAttachments.ts`: upload → collect ids → `attachAll`).

## Command shape

```
retask file upload <path> [--task <task-id> | --comment <comment-id>]
```

`--task` and `--comment` are mutually exclusive (`MarkFlagsMutuallyExclusive`).
Neither → personal upload.

| Invocation | Scope | Sequence |
|---|---|---|
| `file upload ./x.pdf` | user | POST (no `workspace_id`) → `GetFile` |
| `file upload ./x.pdf --task <id>` | workspace | POST `target_nrn=nweb:retask-task:task:<id>` → `AddTaskAttachments` → `GetFile` |
| `file upload ./x.pdf --comment <id>` | workspace | `GetComment` → POST with **parent task** NRN → `AddCommentAttachments` → `GetFile` |

### Why `--comment` needs an extra lookup

A comment NRN is not a legal file target — `resolveTargetAccess` accepts only
`retask-task`. So a comment attachment must be uploaded against its **parent
task**, then linked by `comment_id`. `Comment.target_nrn` holds that task NRN
(`api-contracts/proto/comment/v1/comment.proto:48-52`), so `GetComment` supplies
both the target and the authoritative `workspace_id`.

This matches the frontend, whose `CommentComposer` notes: *"Uploads target the
task; the comment doesn't exist until submit."* The file's `target_nrn` stays
pointed at the task even for comment attachments; comment linkage lives only in
`Comment.attachments`.

## Wire contract

```
POST {restApiEndpoint}/v1/upload-file/?workspace_id=<ws>&require_ocr=false&target_nrn=<nrn>
Authorization: Bearer <jwt>
Content-Type: multipart/form-data; boundary=...
  └─ one part: name="file", filename="<name>", Content-Type: <mime>
→ 201 {"id": "<file-id>"}
```

All linking metadata rides in the **query string**; the form has exactly one
part. Three details that fail silently if missed:

1. **Trailing slash** on `/v1/upload-file/`. The server registers a prefix
   pattern (`go/file/cmd/main.go:73`) and the frontend sends the slash. The
   gateway's OpenAPI spec declares it without — the spec is stale (it also
   declares `project_id`/`thread_id`/`path`, which the handler never reads, and
   omits `workspace_id`/`target_nrn`, which it does). Trust the handler.
2. **`multipart.CreatePart`, not `CreateFormFile`.** `CreateFormFile` hardcodes
   `application/octet-stream`, and the server prefers the client-declared part
   MIME over sniffing (`model_http_upload_file.go:50-52`), so every upload would
   be stored mislabeled. Set the part's `Content-Type` from
   `mime.TypeByExtension`; when it returns `""`, omit the header entirely so the
   server falls back to sniffing.
3. **100 MB limit** (`UploadFileSizeLimitInMB`, `go/file/cmd/config.yml`).
   Exceeding it surfaces as a confusing `500 Failed to parse multipart form`, so
   check size client-side and fail fast with a clear message. (The frontend's
   10 MB cap is browser-side only and does not bind the CLI.)

Body is assembled in a `bytes.Buffer` rather than streamed via `io.Pipe`: it
yields a known `Content-Length` and avoids chunked transfer-encoding through the
gateway. Bounded by the 100 MB cap, the memory cost is acceptable.

### Error handling

Errors return `{"error": "..."}`. Decode it and surface the message; fall back to
the HTTP status when the body will not parse. Do **not** rely on the response
`Content-Type` — `SetJSONErrorResponse` calls `WriteHeader` before setting the
header, so the JSON content-type is never actually sent.

Notable statuses: `401` unauthorized; `403` `Permission denied` (not EDITOR on
the target); `400` `target_nrn is required when workspace_id is set`; `400`
`Failed to retrieve file from form data`.

## Config

`internal/config/profile.go`, mirroring the existing `Endpoint` pattern:

- `const DefaultRestAPIEndpoint = "https://rest-api.nweb.app"`
- `RestAPIEndpoint string \`yaml:"rest_api_endpoint,omitempty"\`` on `Profile`
  (snake_case, matching existing tags)
- Env override `NWEB_REST_API_ENDPOINT`, alongside `NWEB_API_ENDPOINT`

Prod is the right default: the CLI already defaults `Endpoint` to the prod
`api.nweb.app:443`. (The frontend's `restApiEndpoint` default reads
`rest-api.dev.nweb.app` only because the web app defaults to dev.)

Touch points for defaulting, all four:
1. `DefaultRestAPIEndpoint` const
2. `Profile` struct field
3. `Load()`'s not-exist branch literal
4. `ActiveProfileData()` — the `!ok` fallback, the `== ""` default, and the env
   override

**Not passed through `client.BaseURL()`.** That helper is for gRPC-style
`host:port` values; `RestAPIEndpoint` is already a full URL, and routing it
through `BaseURL` would let `--insecure` silently rewrite `https://`→`http://`.
`--insecure` still controls TLS verification via `client.New`.

## Output

Uniform across all three modes: fetch the file with `GetFile` after upload (and
after any linking) and print it via `output.Print(gf.Pretty, ...)`.

The upload endpoint returns only `{"id": "..."}` — no name, size, or mime — so
this costs one extra RPC but buys authoritative, server-computed values
(`mime_type`, `storage_path`, `preview_url`, `download_url`) and the same shape
as `retask file get`. `GetFile` is authorized in both scopes: owner for user
files, task-role VIEWER+ for workspace files.

Output fields: `file_id`, `workspace_id`, `type`, `target_nrn`, `file_name`,
`mime_type`, `bytes`, `storage_path`, `preview_url`, `download_url`,
`created_by_nrn`, `created_at`.

## Scope boundaries (YAGNI)

- **No `--require-ocr`.** The server has `// TODO: Implement require_ocr` and
  ignores the param. A flag that does nothing is worse than no flag. The CLI
  still sends `require_ocr=false` to match the frontend's wire format.
- **Single file per invocation.** `Args: cobra.ExactArgs(1)`. Batch upload is a
  plausible follow-up (`Add*Attachments` already takes repeated `file_ids`) but
  is not requested.
- **No `--target <nrn>` escape hatch.** `--task`/`--comment` cover every legal
  workspace target, since task is the only accepted target service.

## File layout

New file `internal/cmd/file/upload.go`; `command.go` keeps the gRPC subcommands.
REST transport is a distinct concern, and the split keeps each file focused.
Precedent: `internal/cmd/task/nrn.go`.

The core function takes its collaborators as parameters so it is testable
without touching `connect()`:

```go
func uploadFile(ctx context.Context, httpClient *http.Client, restEndpoint, path, workspaceID, targetNrn string) (fileID string, err error)
```

Named return parameters per the repo convention for multi-return functions.

`connect()` currently returns a `FileServiceClient`. Upload also needs the raw
`*http.Client` and the resolved `RestAPIEndpoint`, so a sibling helper resolves
profile + JWT and returns those, without duplicating the auth-resolution logic.

## Testing

`internal/cmd/file/upload_test.go`, package `file` (internal), stdlib `testing` —
matching `internal/cmd/comment/command_test.go`.

- **Transport, via `httptest.NewServer`** (the pattern in
  `internal/client/connect_test.go`): assert method `POST`, path
  `/v1/upload-file/` including trailing slash, query params per mode
  (`workspace_id`/`target_nrn` present for `--task`, both absent for personal),
  `Authorization: Bearer <jwt>`, multipart part named `file` with the right
  filename and `Content-Type`. Return `201 {"id":"..."}`.
- **Error mapping**: a `403 {"error":"Permission denied"}` surfaces that message.
- **MIME selection**: `.pdf` → `application/pdf`; unknown extension → header
  omitted.
- **Flag validation**: `--task` with `--comment` errors; missing/nonexistent path
  errors; oversize file errors before any request is made.

## Follow-ups (out of scope)

- `retask file list` always sends `gf.WorkspaceID`, so it cannot list personal
  files, and a bare `retask file list` (its own documented example) fails with
  `filter.target_nrn is required when workspace_id is set`. Pre-existing; worth a
  separate fix now that personal uploads make such files reachable.
- `retask-docs`' `cli/reference/file.md` is stale (documents a `--project-id`
  flag that no longer exists). Regenerating via `yarn gen:cli` fixes it as a side
  effect of this work.

## Checklist

1. `internal/config/profile.go` — `DefaultRestAPIEndpoint`, `RestAPIEndpoint`
   field, four defaulting touch points, `NWEB_REST_API_ENDPOINT` override
2. `internal/cmd/file/upload.go` — `newUploadCommand` + `uploadFile` helper
3. `internal/cmd/file/command.go` — register `newUploadCommand(gf)`
4. `internal/cmd/helpcmd/command.go` — manifest entry with
   `Flags: []string{"--task", "--comment"}` exactly. **Mandatory**:
   `TestHelpManifestMatchesCommandTree` (`cmd/retask/main_test.go:81`) walks the
   cobra tree and fails the build if a leaf is undocumented or its documented
   flags do not match its declared flags.
5. `internal/cmd/file/upload_test.go` + `internal/config/profile_test.go` update
6. `go build ./...` and `go test ./...`
7. Regenerate `retask-docs` via `yarn gen:cli`
