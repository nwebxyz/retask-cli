// internal/cmd/file/upload.go
package file

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
)

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
