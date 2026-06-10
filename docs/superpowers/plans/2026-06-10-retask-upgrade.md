# retask upgrade: Self-Update Command

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `retask upgrade` subcommand that fetches the latest GitHub release, verifies its SHA256 checksum, and atomically replaces the running binary in place.

**Architecture:** A new `internal/cmd/upgrade` package holds all logic. Pure helpers (`assetName`, `parseChecksum`) are unit-tested via TDD. Network and binary-swap operations use `go-update` and are covered by a manual smoke test. The command is registered in `main.go` and documented in `helpcmd`.

**Tech Stack:** Go stdlib (`net/http`, `archive/tar`, `archive/zip`, `compress/gzip`, `crypto/sha256`), `github.com/inconshreveable/go-update` for atomic binary replacement, `github.com/spf13/cobra` (existing).

---

## File Map

| Action | Path | Purpose |
|--------|------|---------|
| Create | `internal/cmd/upgrade/command.go` | All upgrade logic: command, HTTP fetch, progress display, extraction, apply |
| Create | `internal/cmd/upgrade/command_test.go` | Unit tests for `assetName` and `parseChecksum` |
| Modify | `cmd/retask/main.go` | Add `upgradecmd` import and register `upgradecmd.NewCommand(gf)` |
| Modify | `internal/cmd/helpcmd/command.go` | Add `retask upgrade` entry to the JSON manifest |

---

### Task 1: Add go-update dependency

**Files:**
- Modify: `go.mod`, `go.sum` (via go get)

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/inconshreveable/go-update
```

Expected: `go.mod` gains a `require github.com/inconshreveable/go-update` line; `go.sum` is updated.

- [ ] **Step 2: Tidy**

```bash
go mod tidy
```

Expected: no errors, no unexpected removals.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add go-update dependency for self-upgrade"
```

---

### Task 2: Pure helper functions (TDD)

**Files:**
- Create: `internal/cmd/upgrade/command_test.go`
- Create: `internal/cmd/upgrade/command.go` (stub, then filled)

- [ ] **Step 1: Write the failing tests**

Create `internal/cmd/upgrade/command_test.go`:

```go
package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"darwin", "amd64", "retask_0.2.0_darwin_amd64.tar.gz"},
		{"darwin", "arm64", "retask_0.2.0_darwin_arm64.tar.gz"},
		{"linux", "amd64", "retask_0.2.0_linux_amd64.tar.gz"},
		{"linux", "arm64", "retask_0.2.0_linux_arm64.tar.gz"},
		{"windows", "amd64", "retask_0.2.0_windows_amd64.zip"},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			assert.Equal(t, tt.want, assetName("0.2.0", tt.goos, tt.goarch))
		})
	}
}

func TestParseChecksum(t *testing.T) {
	data := []byte(
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  retask_0.2.0_darwin_arm64.tar.gz\n" +
			"b94d27b9934d3e08a52e52d7da7dabfac484efe04294e576b4b9f8c4a3b9f8c4  retask_0.2.0_linux_amd64.tar.gz\n",
	)

	t.Run("found", func(t *testing.T) {
		got, err := parseChecksum(data, "retask_0.2.0_darwin_arm64.tar.gz")
		require.NoError(t, err)
		assert.Len(t, got, 32) // SHA256 = 32 bytes
	})

	t.Run("not found", func(t *testing.T) {
		_, err := parseChecksum(data, "retask_0.2.0_windows_amd64.zip")
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("malformed hex", func(t *testing.T) {
		bad := []byte("notvalidhex!!  retask_0.2.0_darwin_arm64.tar.gz\n")
		_, err := parseChecksum(bad, "retask_0.2.0_darwin_arm64.tar.gz")
		assert.Error(t, err)
	})
}
```

- [ ] **Step 2: Create a minimal stub so the package compiles**

Create `internal/cmd/upgrade/command.go`:

```go
package upgrade
```

- [ ] **Step 3: Run tests to confirm they fail**

```bash
go test ./internal/cmd/upgrade/...
```

Expected: FAIL — `assetName undefined`, `parseChecksum undefined`.

- [ ] **Step 4: Implement the pure functions**

Replace `internal/cmd/upgrade/command.go` with:

```go
package upgrade

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func assetName(ver, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("retask_%s_%s_%s%s", ver, goos, goarch, ext)
}

func parseChecksum(data []byte, filename string) ([]byte, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return hex.DecodeString(fields[0])
		}
	}
	return nil, fmt.Errorf("checksum not found for %s", filename)
}
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/cmd/upgrade/...
```

Expected: PASS — 2 test functions, 8 sub-tests total.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/upgrade/
git commit -m "feat: add upgrade package with assetName and parseChecksum helpers"
```

---

### Task 3: Full upgrade command implementation

**Files:**
- Modify: `internal/cmd/upgrade/command.go` (replace stub with full implementation)

- [ ] **Step 1: Replace command.go with the full implementation**

```go
package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	update "github.com/inconshreveable/go-update"
	"github.com/spf13/cobra"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/version"
)

const githubAPI = "https://api.github.com/repos/nwebxyz/retask-cli/releases/latest"

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	name  string
	isTTY bool
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.isTTY {
		if p.total > 0 {
			pct := p.read * 100 / p.total
			fmt.Fprintf(os.Stderr, "\rDownloading %s... %s / %s (%d%%)",
				p.name, formatBytes(p.read), formatBytes(p.total), pct)
		} else {
			fmt.Fprintf(os.Stderr, "\rDownloading %s... %s", p.name, formatBytes(p.read))
		}
	}
	return n, err
}

func formatBytes(b int64) string {
	const mb = 1024 * 1024
	const kb = 1024
	switch {
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.0f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// NewCommand returns the upgrade cobra command. gf is accepted for API
// consistency with other commands but is not used.
func NewCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade retask to the latest version",
		Long: `Fetch the latest release from GitHub and replace the running binary.

Usage example:
  retask upgrade

Requires write permission to the directory containing the retask binary.
If permission is denied, retry with sudo.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run()
		},
	}
}

func run() error {
	if version.Version == "dev" {
		return fmt.Errorf("upgrade: cannot upgrade a dev build")
	}

	rel, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("upgrade: failed to fetch latest release: %w", err)
	}

	latest := strings.TrimPrefix(rel.TagName, "v")

	if latest == version.Version {
		fmt.Printf("retask v%s is already up to date\n", version.Version)
		return nil
	}

	fmt.Printf("retask v%s → v%s\n", version.Version, latest)

	asset := assetName(latest, runtime.GOOS, runtime.GOARCH)
	assetURL, checksumURL := findAssetURLs(rel.Assets, asset)
	if assetURL == "" {
		return fmt.Errorf("upgrade: no release asset found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	checksumData, err := downloadRaw(checksumURL)
	if err != nil {
		return fmt.Errorf("upgrade: failed to download checksums: %w", err)
	}

	expectedChecksum, err := parseChecksum(checksumData, asset)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}

	data, err := downloadWithProgress(assetURL, asset)
	if err != nil {
		return fmt.Errorf("upgrade: failed to download: %w", err)
	}

	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], expectedChecksum) {
		return fmt.Errorf("upgrade: checksum verification failed")
	}

	var binary []byte
	if strings.HasSuffix(asset, ".zip") {
		binary, err = extractFromZip(data)
	} else {
		binary, err = extractFromTarGz(data)
	}
	if err != nil {
		return fmt.Errorf("upgrade: failed to extract binary: %w", err)
	}

	if err := update.Apply(bytes.NewReader(binary), update.Options{}); err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("upgrade: %s (try running with sudo)", err)
		}
		return fmt.Errorf("upgrade: %w", err)
	}

	fmt.Printf("Upgraded to v%s\n", latest)
	return nil
}

func fetchLatestRelease() (*githubRelease, error) {
	req, err := http.NewRequest("GET", githubAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "retask-cli/"+version.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func findAssetURLs(assets []githubAsset, name string) (assetURL, checksumURL string) {
	for _, a := range assets {
		switch a.Name {
		case name:
			assetURL = a.BrowserDownloadURL
		case "sha256sums.txt":
			checksumURL = a.BrowserDownloadURL
		}
	}
	return
}

func downloadRaw(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func downloadWithProgress(url, name string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	tty := stderrIsTTY()
	pr := &progressReader{
		r:     resp.Body,
		total: resp.ContentLength,
		name:  name,
		isTTY: tty,
	}
	data, err := io.ReadAll(pr)
	if tty {
		fmt.Fprintln(os.Stderr)
	}
	return data, err
}

func extractFromTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("retask binary not found in archive")
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == "retask" {
			return io.ReadAll(tr)
		}
	}
}

func extractFromZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) == "retask.exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("retask.exe not found in archive")
}

func assetName(ver, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("retask_%s_%s_%s%s", ver, goos, goarch, ext)
}

func parseChecksum(data []byte, filename string) ([]byte, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return hex.DecodeString(fields[0])
		}
	}
	return nil, fmt.Errorf("checksum not found for %s", filename)
}
```

- [ ] **Step 2: Run tests to confirm they still pass**

```bash
go test ./internal/cmd/upgrade/...
```

Expected: PASS — same 8 sub-tests as before.

- [ ] **Step 3: Verify the package compiles cleanly**

```bash
go build ./internal/cmd/upgrade/...
```

Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/upgrade/command.go
git commit -m "feat: implement retask upgrade command"
```

---

### Task 4: Register command and update helpcmd

**Files:**
- Modify: `cmd/retask/main.go`
- Modify: `internal/cmd/helpcmd/command.go`

- [ ] **Step 1: Add import to main.go**

In `cmd/retask/main.go`, add the following import between `taskcmd` and `workspacecmd` (line 18–19):

```go
	upgradecmd "github.com/nwebxyz/retask-cli/internal/cmd/upgrade"
```

The import block should look like:

```go
	taskcmd "github.com/nwebxyz/retask-cli/internal/cmd/task"
	upgradecmd "github.com/nwebxyz/retask-cli/internal/cmd/upgrade"
	workspacecmd "github.com/nwebxyz/retask-cli/internal/cmd/workspace"
```

- [ ] **Step 2: Register the command in main.go**

After `root.AddCommand(taskcmd.NewCommand(gf))` (currently line ~87), add:

```go
	root.AddCommand(upgradecmd.NewCommand(gf))
```

The block should look like:

```go
	root.AddCommand(taskcmd.NewCommand(gf))
	root.AddCommand(upgradecmd.NewCommand(gf))
	root.AddCommand(workspacecmd.NewCommand(gf))
```

- [ ] **Step 3: Add entry to helpcmd manifest**

In `internal/cmd/helpcmd/command.go`, add this line at the end of the `Commands` slice, after the `retask agent delete` entry (currently the last entry before the closing `}`):

```go
			{Command: "retask upgrade", Description: "Upgrade retask to the latest version", Example: "retask upgrade"},
```

- [ ] **Step 4: Build the full binary**

```bash
go build -ldflags "-X github.com/nwebxyz/retask-cli/internal/version.Version=0.1.0" -o retask ./cmd/retask/
```

Expected: no errors, `retask` binary produced.

- [ ] **Step 5: Smoke test — help output**

```bash
./retask upgrade --help
```

Expected to contain:

```
Fetch the latest release from GitHub and replace the running binary.

Usage example:
  retask upgrade
```

- [ ] **Step 6: Smoke test — dev guard**

Build without version ldflags so `Version` stays `"dev"`:

```bash
go build -o retask-dev ./cmd/retask/ && ./retask-dev upgrade
```

Expected:

```
Error: upgrade: cannot upgrade a dev build
```

- [ ] **Step 7: Smoke test — already up to date**

```bash
./retask upgrade
```

Expected (if v0.1.0 is the current latest release):

```
retask v0.1.0 is already up to date
```

If a newer release exists on GitHub it will begin downloading — that is also correct behaviour.

- [ ] **Step 8: Run all tests**

```bash
go test ./...
```

Expected: PASS — no regressions.

- [ ] **Step 9: Clean up dev binary and commit**

```bash
rm retask-dev
git add cmd/retask/main.go internal/cmd/helpcmd/command.go
git commit -m "feat: register retask upgrade command and add to help manifest"
```
