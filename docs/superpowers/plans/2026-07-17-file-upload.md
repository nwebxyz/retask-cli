# `retask file upload` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `retask file upload <path>` — uploading a local file to the file service, optionally attaching it to a task or comment in one invocation.

**Architecture:** `FileService` has no upload RPC, so bytes go over a REST multipart POST to a separate host (`rest_api_endpoint`), while follow-up calls (attach, read-back) use the existing gRPC/Connect clients. The upload function takes its HTTP client and endpoint as plain parameters so it is testable against `httptest` without touching credential resolution.

**Tech Stack:** Go 1.26.4, cobra, connectrpc, `mime/multipart`, stdlib `testing` (+ testify in `internal/config` tests only).

**Spec:** `docs/superpowers/specs/2026-07-17-file-upload-design.md`
**Task:** RETA-98

## Global Constraints

- Module path: `github.com/nwebxyz/retask-cli`.
- Functions with multiple return values MUST use named return parameters (repo convention).
- Every command's `Long` help follows the template: one-line summary, blank line, `Usage example:`, blank line, `Flags:`, and an `Output fields:` line last.
- The help-llm manifest is build-enforced: `TestHelpManifestMatchesCommandTree` (`cmd/retask/main_test.go:80`) fails if a runnable leaf is undocumented or its documented flags differ from its declared flags. Global flags (`profile`, `workspace-id`, `pretty`, `insecure`, `no-save`, `config`, `verbose`, `help`) are ignored on both sides; both sides are sorted, so order does not matter.
- REST upload wire contract, exact:
  `POST {rest_api_endpoint}/v1/upload-file/?workspace_id=<ws>&require_ocr=false&target_nrn=<nrn>` — **trailing slash on the path is required**; `Authorization: Bearer <jwt>`; body `multipart/form-data` with exactly one part named `file`; success `201 {"id":"<file-id>"}`; errors `{"error":"..."}`.
- `target_nrn` is REQUIRED whenever `workspace_id` is non-empty (server returns 400 otherwise). Omitting `workspace_id` selects user-file scope, where the server forces the target to the caller's own NRN.
- Only `retask-task` is an accepted target service. A comment NRN is not a legal file target.
- Upload size limit: 100 MB (`UploadFileSizeLimitInMB`).
- NRN string form: `domain:service:resource_type:resource_id`, e.g. `nweb:retask-task:task:<id>`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/profile.go` (modify) | Add `RestAPIEndpoint` to `Profile` + default + env override |
| `internal/config/profile_test.go` (modify) | Cover the new default and env override |
| `internal/cmd/file/upload.go` (create) | The `upload` subcommand: REST transport, MIME selection, mode dispatch |
| `internal/cmd/file/upload_test.go` (create) | httptest-based transport tests + flag validation |
| `internal/cmd/file/command.go` (modify) | Register `newUploadCommand(gf)` |
| `internal/cmd/helpcmd/command.go` (modify) | Manifest entry for `retask file upload` |

Upload lives in its own file: REST transport is a distinct concern from `command.go`'s gRPC subcommands. Precedent: `internal/cmd/task/nrn.go`.

---

### Task 1: Config — `rest_api_endpoint`

**Files:**
- Modify: `internal/config/profile.go`
- Test: `internal/config/profile_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.DefaultRestAPIEndpoint` (const, `"https://rest-api.nweb.app"`); `config.Profile.RestAPIEndpoint` (string field, yaml `rest_api_endpoint`). `Config.ActiveProfileData(name string) Profile` keeps its signature and now populates `RestAPIEndpoint`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/profile_test.go`:

```go
func TestRestAPIEndpointDefault(t *testing.T) {
	cfg, err := config.Load("/tmp/retask-test-nonexistent-abc123.yaml")
	require.NoError(t, err)
	p := cfg.ActiveProfileData("")
	assert.Equal(t, "https://rest-api.nweb.app", p.RestAPIEndpoint)
}

func TestRestAPIEndpointFromProfile(t *testing.T) {
	cfg := &config.Config{
		ActiveProfile: "default",
		Profiles: map[string]config.Profile{
			"default": {Endpoint: "api.nweb.app:443", RestAPIEndpoint: "https://rest-api.dev.nweb.app"},
		},
	}
	p := cfg.ActiveProfileData("")
	assert.Equal(t, "https://rest-api.dev.nweb.app", p.RestAPIEndpoint)
}

func TestRestAPIEndpointEnvOverride(t *testing.T) {
	t.Setenv("NWEB_REST_API_ENDPOINT", "http://localhost:8080")
	cfg, err := config.Load("/tmp/retask-test-nonexistent-abc123.yaml")
	require.NoError(t, err)
	p := cfg.ActiveProfileData("")
	assert.Equal(t, "http://localhost:8080", p.RestAPIEndpoint)
}

func TestRestAPIEndpointUnknownProfileFallsBackToDefault(t *testing.T) {
	cfg := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
	p := cfg.ActiveProfileData("nope")
	assert.Equal(t, "https://rest-api.nweb.app", p.RestAPIEndpoint)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestRestAPIEndpoint -v`
Expected: FAIL — compile error, `p.RestAPIEndpoint` undefined.

- [ ] **Step 3: Implement**

In `internal/config/profile.go`, add the const next to `DefaultEndpoint`:

```go
const DefaultEndpoint = "api.nweb.app:443"

// DefaultRestAPIEndpoint is the REST host used for file uploads. Unlike
// Endpoint (a gRPC host:port), this is a full URL and is used verbatim.
const DefaultRestAPIEndpoint = "https://rest-api.nweb.app"
```

Add the field to `Profile`:

```go
type Profile struct {
	Endpoint        string    `yaml:"endpoint"`
	RestAPIEndpoint string    `yaml:"rest_api_endpoint,omitempty"`
	WorkspaceID     string    `yaml:"workspace_id,omitempty"`
	CachedJWT       string    `yaml:"cached_jwt,omitempty"`
	JWTExpiresAt    time.Time `yaml:"jwt_expires_at,omitempty"`
}
```

In `Load()`, update the not-exist branch literal:

```go
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{
			ActiveProfile: "default",
			Profiles: map[string]Profile{
				"default": {Endpoint: DefaultEndpoint, RestAPIEndpoint: DefaultRestAPIEndpoint},
			},
		}, nil
	}
```

In `ActiveProfileData()`, update the `!ok` fallback and add defaulting + env override:

```go
	p, ok := c.Profiles[name]
	if !ok {
		p = Profile{Endpoint: DefaultEndpoint, RestAPIEndpoint: DefaultRestAPIEndpoint}
	}
	if p.Endpoint == "" {
		p.Endpoint = DefaultEndpoint
	}
	if p.RestAPIEndpoint == "" {
		p.RestAPIEndpoint = DefaultRestAPIEndpoint
	}
	if v := os.Getenv("NWEB_API_ENDPOINT"); v != "" {
		p.Endpoint = v
	}
	if v := os.Getenv("NWEB_REST_API_ENDPOINT"); v != "" {
		p.RestAPIEndpoint = v
	}
	return p
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, including the pre-existing `TestLoadMissing` and `TestSaveAndLoad`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/profile.go internal/config/profile_test.go
git commit -m "feat(config): add rest_api_endpoint to profile

The file service has no upload RPC; uploads go to a separate REST host.
Defaults to the prod https://rest-api.nweb.app, matching the existing prod
default for Endpoint. Overridable via NWEB_REST_API_ENDPOINT."
```

---

### Task 2: MIME selection helper

**Files:**
- Create: `internal/cmd/file/upload.go`
- Test: `internal/cmd/file/upload_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `partMimeType(path string) (mimeType string)` — returns `""` when the extension is unknown, signalling "omit the header and let the server sniff".

Why this exists: the server prefers the client-declared part `Content-Type` and only sniffs as a fallback (`go/file/model/model_http_upload_file.go:50-52`). `multipart.CreateFormFile` hardcodes `application/octet-stream`, which would defeat that and mislabel every upload — so the part is built by hand and this helper decides the value.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/file/upload_test.go`:

```go
package file

import (
	"testing"
)

func TestPartMimeType(t *testing.T) {
	cases := map[string]string{
		"/tmp/report.pdf": "application/pdf",
		"/tmp/a.png":      "image/png",
		"/tmp/notes.txt":  "text/plain",
		"/tmp/blob.wat":   "",
		"/tmp/noext":      "",
	}
	for path, want := range cases {
		got := partMimeType(path)
		// mime.TypeByExtension may append "; charset=utf-8"; compare the bare type.
		if want == "" {
			if got != "" {
				t.Errorf("partMimeType(%q) = %q, want empty", path, got)
			}
			continue
		}
		if !strings.HasPrefix(got, want) {
			t.Errorf("partMimeType(%q) = %q, want prefix %q", path, got, want)
		}
	}
}
```

Add `"strings"` to the import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/file/ -run TestPartMimeType -v`
Expected: FAIL — `undefined: partMimeType`.

- [ ] **Step 3: Implement**

Create `internal/cmd/file/upload.go`:

```go
// internal/cmd/file/upload.go
package file

import (
	"mime"
	"path/filepath"
	"strings"
)

// partMimeType returns the MIME type to declare on the multipart part, derived
// from the file extension. An empty return means "unknown": the caller omits the
// Content-Type header entirely so the server falls back to sniffing the bytes.
//
// This mirrors the browser, which sets File.type from the extension. It matters
// because the server prefers the declared type over sniffing, and sniffing
// mislabels ZIP-based containers (.docx, .3mf) as application/zip.
func partMimeType(path string) (mimeType string) {
	ext := filepath.Ext(path)
	if ext == "" {
		return ""
	}
	return strings.TrimSpace(mime.TypeByExtension(ext))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cmd/file/ -run TestPartMimeType -v`
Expected: PASS.

Note: if `.wat` resolves to a real type on the test machine, swap it for another extension that `mime.TypeByExtension` does not know. The assertion that matters is that *some* unknown extension yields `""`.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/file/upload.go internal/cmd/file/upload_test.go
git commit -m "feat(file): add partMimeType helper for multipart uploads"
```

---

### Task 3: The REST upload function

**Files:**
- Modify: `internal/cmd/file/upload.go`
- Test: `internal/cmd/file/upload_test.go`

**Interfaces:**
- Consumes: `partMimeType(path string) (mimeType string)` from Task 2.
- Produces:
  - `const maxUploadBytes = 100 << 20`
  - `uploadFile(ctx context.Context, httpClient *http.Client, restEndpoint, path, workspaceID, targetNrn string) (fileID string, err error)` — POSTs the file and returns the new file ID. `workspaceID == ""` means user-file scope: neither `workspace_id` nor `target_nrn` is sent.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cmd/file/upload_test.go`:

```go
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUploadFileWorkspaceScope(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotPartName, gotFilename, gotPartType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		gotPartName, gotFilename = part.FormName(), part.FileName()
		gotPartType = part.Header.Get("Content-Type")
		b, _ := io.ReadAll(part)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"file_abc123"}`))
	}))
	defer srv.Close()

	path := writeTempFile(t, "report.pdf", "hello-bytes")
	httpClient := client.New("my-token", false, false)

	fileID, err := uploadFile(context.Background(), httpClient, srv.URL, path,
		"ws_1", "nweb:retask-task:task:task_9")
	if err != nil {
		t.Fatal(err)
	}

	if fileID != "file_abc123" {
		t.Errorf("fileID = %q, want file_abc123", fileID)
	}
	// Trailing slash is required: the server registers a prefix pattern.
	if gotPath != "/v1/upload-file/" {
		t.Errorf("path = %q, want /v1/upload-file/", gotPath)
	}
	q, _ := url.ParseQuery(gotQuery)
	if q.Get("workspace_id") != "ws_1" {
		t.Errorf("workspace_id = %q, want ws_1", q.Get("workspace_id"))
	}
	if q.Get("target_nrn") != "nweb:retask-task:task:task_9" {
		t.Errorf("target_nrn = %q", q.Get("target_nrn"))
	}
	if q.Get("require_ocr") != "false" {
		t.Errorf("require_ocr = %q, want false", q.Get("require_ocr"))
	}
	if gotAuth != "Bearer my-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotPartName != "file" {
		t.Errorf("part name = %q, want file", gotPartName)
	}
	if gotFilename != "report.pdf" {
		t.Errorf("filename = %q, want report.pdf", gotFilename)
	}
	if !strings.HasPrefix(gotPartType, "application/pdf") {
		t.Errorf("part Content-Type = %q, want application/pdf", gotPartType)
	}
	if gotBody != "hello-bytes" {
		t.Errorf("body = %q, want hello-bytes", gotBody)
	}
}

func TestUploadFileUserScopeOmitsWorkspaceAndTarget(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"file_personal"}`))
	}))
	defer srv.Close()

	path := writeTempFile(t, "note.txt", "x")
	fileID, err := uploadFile(context.Background(), client.New("t", false, false), srv.URL, path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if fileID != "file_personal" {
		t.Errorf("fileID = %q", fileID)
	}
	q, _ := url.ParseQuery(gotQuery)
	// A personal upload must send neither; workspace_id would make the server
	// demand a target_nrn and 400.
	if _, ok := q["workspace_id"]; ok {
		t.Errorf("workspace_id must be absent for user scope, got %q", gotQuery)
	}
	if _, ok := q["target_nrn"]; ok {
		t.Errorf("target_nrn must be absent for user scope, got %q", gotQuery)
	}
}

func TestUploadFileUnknownExtensionOmitsPartContentType(t *testing.T) {
	var gotPartType string
	var present bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		_, present = part.Header["Content-Type"]
		gotPartType = part.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"i"}`))
	}))
	defer srv.Close()

	path := writeTempFile(t, "noext", "x")
	if _, err := uploadFile(context.Background(), client.New("t", false, false), srv.URL, path, "", ""); err != nil {
		t.Fatal(err)
	}
	if present {
		t.Errorf("Content-Type should be omitted so the server sniffs, got %q", gotPartType)
	}
}

func TestUploadFileSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Permission denied"}`))
	}))
	defer srv.Close()

	path := writeTempFile(t, "a.txt", "x")
	_, err := uploadFile(context.Background(), client.New("t", false, false), srv.URL, path, "ws", "nrn")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("error = %v, want it to contain the server message", err)
	}
}

func TestUploadFileNonJSONErrorFallsBackToStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>gateway</html>`))
	}))
	defer srv.Close()

	path := writeTempFile(t, "a.txt", "x")
	_, err := uploadFile(context.Background(), client.New("t", false, false), srv.URL, path, "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %v, want it to mention the status code", err)
	}
}

func TestUploadFileMissingFile(t *testing.T) {
	_, err := uploadFile(context.Background(), client.New("t", false, false),
		"http://example.invalid", "/nonexistent/nope.txt", "", "")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestUploadFileRejectsOversizeBeforeRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "big.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse file: reports >100MB without allocating it.
	if err := f.Truncate(maxUploadBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = uploadFile(context.Background(), client.New("t", false, false), srv.URL, path, "", "")
	if err == nil {
		t.Fatal("expected oversize error")
	}
	if called {
		t.Error("must fail before making a request")
	}
}
```

Set the import block of `upload_test.go` to:

```go
import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwebxyz/retask-cli/internal/client"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cmd/file/ -v`
Expected: FAIL — `undefined: uploadFile`, `undefined: maxUploadBytes`.

- [ ] **Step 3: Implement**

Add to `internal/cmd/file/upload.go` (extend the import block to include `bytes`, `context`, `encoding/json`, `fmt`, `io`, `mime/multipart`, `net/http`, `net/textproto`, `net/url`, `os`):

```go
// maxUploadBytes mirrors the file service's UploadFileSizeLimitInMB (100 MB).
// Checked client-side because the server surfaces an over-limit body as an
// opaque 500 "Failed to parse multipart form".
const maxUploadBytes = 100 << 20

// uploadResponse is the upload endpoint's success body.
type uploadResponse struct {
	ID string `json:"id"`
}

// errorResponse is the upload endpoint's error body.
type errorResponse struct {
	Error string `json:"error"`
}

// uploadFile POSTs path to the REST upload endpoint and returns the new file ID.
//
// An empty workspaceID selects user-file scope: neither workspace_id nor
// target_nrn is sent, and the server targets the file at the caller's own NRN.
// A non-empty workspaceID requires a targetNrn — the server rejects the pair
// otherwise.
func uploadFile(ctx context.Context, httpClient *http.Client, restEndpoint, path, workspaceID, targetNrn string) (fileID string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > maxUploadBytes {
		return "", fmt.Errorf("%s is %d bytes, over the %d byte upload limit", path, info.Size(), maxUploadBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	// Buffered rather than streamed via io.Pipe: it yields a known
	// Content-Length and avoids chunked transfer-encoding through the gateway.
	// Bounded by maxUploadBytes.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	name := filepath.Base(path)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	// Built by hand rather than via CreateFormFile, which would hardcode
	// application/octet-stream. Omitting the header lets the server sniff.
	if mt := partMimeType(path); mt != "" {
		h.Set("Content-Type", mt)
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("require_ocr", "false")
	if workspaceID != "" {
		q.Set("workspace_id", workspaceID)
		q.Set("target_nrn", targetNrn)
	}

	// The trailing slash is required: the server registers /v1/upload-file/ as a
	// prefix pattern.
	endpoint := strings.TrimSuffix(restEndpoint, "/") + "/v1/upload-file/?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	// Authorization is injected by the client's transport.
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", uploadError(resp.StatusCode, respBody)
	}

	var ur uploadResponse
	if err := json.Unmarshal(respBody, &ur); err != nil || ur.ID == "" {
		return "", fmt.Errorf("upload %s: unexpected response: %s", name, strings.TrimSpace(string(respBody)))
	}
	return ur.ID, nil
}

// uploadError turns a non-2xx upload response into an error, preferring the
// server's {"error": "..."} message. The response Content-Type is not consulted:
// the server writes its status before setting the header, so error bodies ship
// without the JSON content-type.
func uploadError(status int, body []byte) error {
	var er errorResponse
	if err := json.Unmarshal(body, &er); err == nil && er.Error != "" {
		return fmt.Errorf("upload failed (%d): %s", status, er.Error)
	}
	return fmt.Errorf("upload failed with status %d", status)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cmd/file/ -v`
Expected: PASS — all eight tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/file/upload.go internal/cmd/file/upload_test.go
git commit -m "feat(file): add REST multipart upload function

The file service has no upload RPC, so bytes go over REST. Builds the part
by hand to declare an accurate Content-Type — CreateFormFile would hardcode
application/octet-stream and defeat the server's type preference."
```

---

### Task 4: The `upload` subcommand

**Files:**
- Modify: `internal/cmd/file/upload.go`
- Modify: `internal/cmd/file/command.go` (register the subcommand)
- Test: `internal/cmd/file/upload_test.go`

**Interfaces:**
- Consumes: `uploadFile(...)` (Task 3); `config.DefaultRestAPIEndpoint`, `Profile.RestAPIEndpoint` (Task 1); the existing package-local `connect(gf)`.
- Produces: `newUploadCommand(gf *flags.Global) *cobra.Command`; `taskNrnString(taskID string) (nrn string)`; `nrnString(n *commonv1.Nrn) (s string)`; `resolveUpload(gf *flags.Global) (deps uploadDeps, err error)` with `uploadDeps{httpClient *http.Client; restEndpoint, baseURL, transport string}`.

Mode dispatch, per the spec:

| Flags | workspace_id | target_nrn | Link call |
|---|---|---|---|
| none | *(omitted)* | *(omitted)* | none |
| `--task <id>` | `gf.WorkspaceID` | `nweb:retask-task:task:<id>` | `AddTaskAttachments` |
| `--comment <id>` | comment's `workspace_id` | comment's `target_nrn` (the parent task) | `AddCommentAttachments` |

All three then `GetFile(file_id)` and print it, for one uniform output shape.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cmd/file/upload_test.go`:

```go
func TestTaskNrnString(t *testing.T) {
	got := taskNrnString("task_abc123")
	want := "nweb:retask-task:task:task_abc123"
	if got != want {
		t.Errorf("taskNrnString() = %q, want %q", got, want)
	}
}

func TestNrnString(t *testing.T) {
	got := nrnString(&commonv1.Nrn{
		Domain: "nweb", Service: "retask-task", ResourceType: "task", ResourceId: "t1",
	})
	if got != "nweb:retask-task:task:t1" {
		t.Errorf("nrnString() = %q", got)
	}
	if s := nrnString(nil); s != "" {
		t.Errorf("nrnString(nil) = %q, want empty", s)
	}
}

func TestUploadCommandRejectsTaskAndCommentTogether(t *testing.T) {
	gf := &flags.Global{WorkspaceID: "ws_1"}
	cmd := newUploadCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"/tmp/whatever.txt", "--task", "t1", "--comment", "c1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for mutually exclusive --task/--comment")
	}
}

func TestUploadCommandRequiresPathArg(t *testing.T) {
	gf := &flags.Global{WorkspaceID: "ws_1"}
	cmd := newUploadCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for missing path argument")
	}
}

func TestUploadCommandRequiresWorkspaceForTask(t *testing.T) {
	gf := &flags.Global{} // no workspace id
	cmd := newUploadCommand(gf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"/tmp/whatever.txt", "--task", "t1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for --task without a workspace id")
	}
}
```

Add to the test import block:

```go
	"github.com/nwebxyz/retask-cli/internal/flags"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cmd/file/ -run 'TestTaskNrnString|TestNrnString|TestUploadCommand' -v`
Expected: FAIL — `undefined: taskNrnString`, `undefined: nrnString`, `undefined: newUploadCommand`.

The workspace-guard test must fail *before* `connect()` is reached, so validate flags first in the implementation.

- [ ] **Step 3: Implement**

Add to `internal/cmd/file/upload.go`. The import block ends up as:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	connectrpc "connectrpc.com/connect"
	"github.com/spf13/cobra"

	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	commentv1 "github.com/nwebxyz/retask-cli/proto-gen/comment/v1"
	commentv1connect "github.com/nwebxyz/retask-cli/proto-gen/comment/v1/commentv1connect"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	filev1connect "github.com/nwebxyz/retask-cli/proto-gen/file/v1/filev1connect"
	taskv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/task/v1"
	taskv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/task/v1/taskv1connect"
)
```

Then the code:

```go
// uploadDeps carries what an upload needs: an authenticated HTTP client, the
// REST endpoint for the bytes, and the gRPC base URL for the follow-up calls.
type uploadDeps struct {
	httpClient   *http.Client
	restEndpoint string
	baseURL      string
	transport    string
}

// resolveUpload loads the profile, resolves the JWT, and returns the endpoints
// an upload needs. It mirrors connect() but exposes the raw HTTP client and the
// REST endpoint instead of a FileServiceClient.
func resolveUpload(gf *flags.Global) (deps uploadDeps, err error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return uploadDeps{}, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return uploadDeps{}, err
	}
	return uploadDeps{
		httpClient: client.New(jwt, gf.Insecure, gf.Verbose),
		// Used verbatim: it is already a full URL, unlike Endpoint. Passing it
		// through client.BaseURL would let --insecure rewrite https:// to http://.
		restEndpoint: profile.RestAPIEndpoint,
		baseURL:      client.BaseURL(profile.Endpoint, gf.Insecure),
		transport:    gf.Transport,
	}, nil
}

// taskNrnString builds a task's target NRN: nweb:retask-task:task:<id>.
func taskNrnString(taskID string) (nrn string) {
	return "nweb:retask-task:task:" + taskID
}

// nrnString renders an NRN in its canonical colon-separated string form.
func nrnString(n *commonv1.Nrn) (s string) {
	if n == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s:%s", n.GetDomain(), n.GetService(), n.GetResourceType(), n.GetResourceId())
}

func newUploadCommand(gf *flags.Global) *cobra.Command {
	var taskID, commentID string
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a file",
		Long: `Upload a local file. With no flags the file is personal: it belongs to you and is
attached to nothing. Pass --task or --comment to attach it in the same step.

Usage examples:
  retask file upload ./report.pdf
  retask file upload ./report.pdf --task task_abc123
  retask file upload ./screenshot.png --comment comment_abc123

Flags:
  --task string     Attach the file to this task
  --comment string  Attach the file to this comment

Output fields: file_id, workspace_id, type, target_nrn, file_name, mime_type, bytes, storage_path, preview_url, download_url, created_by_nrn, created_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			ctx := context.Background()

			// A workspace upload must carry a target; the server rejects the pair
			// otherwise. Validate before resolving credentials so the error is fast
			// and offline.
			if (taskID != "" || commentID != "") && gf.WorkspaceID == "" {
				return fmt.Errorf("--task and --comment require a workspace ID (--workspace-id or NWEB_WORKSPACE_ID)")
			}

			deps, err := resolveUpload(gf)
			if err != nil {
				return err
			}

			// Resolve the upload scope.
			var workspaceID, targetNrn string
			switch {
			case taskID != "":
				workspaceID, targetNrn = gf.WorkspaceID, taskNrnString(taskID)
			case commentID != "":
				// A comment NRN is not a legal file target, so the upload targets the
				// comment's parent task. GetComment supplies both that NRN and the
				// authoritative workspace.
				cc := commentv1connect.NewCommentServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
				resp, gerr := cc.GetComment(ctx, connectrpc.NewRequest(&commonv1.Id{Id: commentID}))
				if gerr != nil {
					return gerr
				}
				targetNrn = nrnString(resp.Msg.GetTargetNrn())
				if targetNrn == "" {
					return fmt.Errorf("comment %s has no target task", commentID)
				}
				workspaceID = resp.Msg.GetWorkspaceId()
			}
			// Personal upload: both stay empty, which selects user-file scope.

			fileID, err := uploadFile(ctx, deps.httpClient, deps.restEndpoint, path, workspaceID, targetNrn)
			if err != nil {
				return err
			}

			// Link. The bytes are already stored; a failure here leaves an orphan
			// file, which the message points at so it can be cleaned up.
			switch {
			case taskID != "":
				tc := taskv1connect.NewTaskServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
				if _, aerr := tc.AddTaskAttachments(ctx, connectrpc.NewRequest(&taskv1.AddTaskAttachmentsRequest{
					TaskId:  taskID,
					FileIds: []string{fileID},
				})); aerr != nil {
					return fmt.Errorf("uploaded file %s but failed to attach it to task %s: %w", fileID, taskID, aerr)
				}
			case commentID != "":
				cc := commentv1connect.NewCommentServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
				if _, aerr := cc.AddCommentAttachments(ctx, connectrpc.NewRequest(&commentv1.AddCommentAttachmentsRequest{
					CommentId: commentID,
					FileIds:   []string{fileID},
				})); aerr != nil {
					return fmt.Errorf("uploaded file %s but failed to attach it to comment %s: %w", fileID, commentID, aerr)
				}
			}

			// Read back so every mode prints the same shape, with server-computed
			// mime_type, storage_path and URLs.
			fc := filev1connect.NewFileServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
			resp, err := fc.GetFile(ctx, connectrpc.NewRequest(&commonv1.Id{Id: fileID}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "Attach the file to this task")
	cmd.Flags().StringVar(&commentID, "comment", "", "Attach the file to this comment")
	cmd.MarkFlagsMutuallyExclusive("task", "comment")
	return cmd
}
```

In `internal/cmd/file/command.go`, register it:

```go
	cmd.AddCommand(
		newUploadCommand(gf),
		newListCommand(gf),
		newGetCommand(gf),
		newDeleteCommand(gf),
		newSignedURLCommand(gf),
	)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/cmd/file/ -v`
Expected: build succeeds; all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/file/upload.go internal/cmd/file/upload_test.go internal/cmd/file/command.go
git commit -m "feat(file): add 'retask file upload' subcommand

Personal by default; --task and --comment attach in the same invocation.
A comment NRN is not a legal file target, so --comment uploads against the
comment's parent task and then links by comment id."
```

---

### Task 5: help-llm manifest + docs

**Files:**
- Modify: `internal/cmd/helpcmd/command.go`

**Interfaces:**
- Consumes: the `retask file upload` command from Task 4, which declares exactly the flags `task` and `comment`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Run the guard test to verify it fails**

Run: `go test ./cmd/retask/ -run TestHelpManifestMatchesCommandTree -v`
Expected: FAIL — `command "retask file upload" is not documented in the help-llm manifest`.

This test is the reason this task exists; it fails the build until the manifest is updated.

- [ ] **Step 2: Add the manifest entry**

In `internal/cmd/helpcmd/command.go`, above the existing `retask file list` entry:

```go
			{Command: "retask file upload", Description: "Upload a file — personal by default, or attached to a task or comment", Flags: []string{"--task", "--comment"}, Example: "retask file upload ./report.pdf --task <task-id>"},
```

The `Flags` list must be exactly `--task` and `--comment`: the guard test compares it against the command's declared flags (both sides sorted, global flags ignored).

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 4: Verify the command is wired end-to-end**

Run:
```bash
go build -o /tmp/retask ./cmd/retask/
/tmp/retask file upload --help
/tmp/retask help-llm | jq '.commands[] | select(.command == "retask file upload")'
```
Expected: help text renders with `--task`/`--comment`; the manifest entry prints.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/helpcmd/command.go
git commit -m "docs(help-llm): document 'retask file upload'"
```

---

### Task 6: Regenerate retask-docs

**Files:**
- Modify: `retask-docs/src/content/docs/cli/reference/file.md` (generated — do not hand-edit)

**Interfaces:**
- Consumes: the help-llm manifest from Task 5.
- Produces: nothing.

- [ ] **Step 1: Regenerate**

Run, from the `retask-docs` repo:
```bash
yarn gen:cli
```
Expected: `file.md` gains an `upload` section.

If `gen:cli` reads a `retask` binary from PATH, build and install the local one first so it picks up the new command rather than a stale release.

- [ ] **Step 2: Verify the diff**

Run: `git -C ../retask-docs diff --stat`
Expected: `file.md` changed. It should gain `upload` and also lose the stale `--project-id` flag on `list` (that flag no longer exists; the fix rides along).

- [ ] **Step 3: Commit**

```bash
git -C ../retask-docs add src/content/docs/cli/reference/file.md
git -C ../retask-docs commit -m "docs(cli): regenerate file reference for 'file upload'"
```

Note: `retask-docs` is a separate repo, so this is a separate commit and PR from the CLI change. If a cross-repo PR is not wanted, stop after Task 5 and raise the docs regen separately.

---

## Manual verification

The unit tests cover the wire format against `httptest`, not the real service. Before merging, exercise it once for real:

```bash
go build -o /tmp/retask ./cmd/retask/

# 1. Personal upload — no workspace_id, server targets your own NRN
echo hello > /tmp/hello.txt
/tmp/retask file upload /tmp/hello.txt

# 2. Task attachment — verify it lands in the task's attachment list
/tmp/retask file upload /tmp/hello.txt --task <task-id>
/tmp/retask task get <task-id> | jq '.attachments'

# 3. Error path — a task you cannot edit should surface "Permission denied"
```

Confirm `mime_type` on the result is `text/plain` and not `application/octet-stream`: that is the `CreatePart` behaviour the tests assert in isolation, and the one most likely to regress silently against the real server.
