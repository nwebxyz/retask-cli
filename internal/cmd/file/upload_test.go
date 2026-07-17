package file

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
