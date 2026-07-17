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
