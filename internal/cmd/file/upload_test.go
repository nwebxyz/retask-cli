package file

import (
	"strings"
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
