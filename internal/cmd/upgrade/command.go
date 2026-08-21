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
	"time"

	update "github.com/inconshreveable/go-update"
	"github.com/spf13/cobra"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/version"
)

const githubAPI = "https://api.github.com/repos/nwebxyz/retask-cli/releases/latest"

// releaseCheckClient bounds the release-metadata lookup only. Run is called
// as the first step of every "retask sandbox connect" invocation, before any
// signal handling is installed, so an unreachable or slow GitHub must not
// hang the whole command indefinitely. The (large, genuinely slow-varying)
// binary download further down is left unbounded, as before.
var releaseCheckClient = &http.Client{Timeout: 10 * time.Second}

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
	w     io.Writer
	isTTY bool
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.isTTY {
		if p.total > 0 {
			pct := p.read * 100 / p.total
			fmt.Fprintf(p.w, "\rDownloading %s... %s / %s (%d%%)",
				p.name, formatBytes(p.read), formatBytes(p.total), pct)
		} else {
			fmt.Fprintf(p.w, "\rDownloading %s... %s", p.name, formatBytes(p.read))
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

// isTerminal reports whether w is a real terminal, so the progress bar only
// renders its carriage-return animation when something will actually show
// it — not into a log file or a discarded writer.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
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
			upgraded, _, err := Run(os.Stdout)
			if err != nil {
				return err
			}
			if !upgraded {
				fmt.Printf("retask v%s is already up to date\n", version.Version)
			}
			return nil
		},
	}
}

// LatestVersion returns the latest published release version, without the
// "v" prefix. It makes no changes on disk — callers that only need to know
// whether an upgrade is available (without necessarily applying it) use this
// instead of Run.
func LatestVersion() (latest string, err error) {
	rel, err := fetchLatestRelease()
	if err != nil {
		return "", fmt.Errorf("upgrade: failed to fetch latest release: %w", err)
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

// Run fetches the latest release from GitHub and, if newer than the running
// binary, downloads, verifies, and applies it in place. upgraded reports
// whether a new binary was applied; newVersion is only set when upgraded is
// true. Run never prints an "already up to date" message — callers that care
// about that case check the returned upgraded value themselves.
//
// w receives the same human-readable progress and status text this function
// has always printed. Pass io.Discard when a caller renders its own UI (such
// as sandbox connect's TUI) and raw terminal writes would corrupt it.
func Run(w io.Writer) (upgraded bool, newVersion string, err error) {
	if version.Version == "dev" {
		return false, "", fmt.Errorf("upgrade: cannot upgrade a dev build")
	}

	rel, err := fetchLatestRelease()
	if err != nil {
		return false, "", fmt.Errorf("upgrade: failed to fetch latest release: %w", err)
	}

	latest := strings.TrimPrefix(rel.TagName, "v")

	if latest == version.Version {
		return false, "", nil
	}

	fmt.Fprintf(w, "retask v%s → v%s\n", version.Version, latest)

	asset := assetName(latest, runtime.GOOS, runtime.GOARCH)
	assetURL, checksumURL := findAssetURLs(rel.Assets, asset)
	if assetURL == "" {
		return false, "", fmt.Errorf("upgrade: no release asset found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	checksumData, err := downloadRaw(checksumURL)
	if err != nil {
		return false, "", fmt.Errorf("upgrade: failed to download checksums: %w", err)
	}

	expectedChecksum, err := parseChecksum(checksumData, asset)
	if err != nil {
		return false, "", fmt.Errorf("upgrade: %w", err)
	}

	data, err := downloadWithProgress(w, assetURL, asset)
	if err != nil {
		return false, "", fmt.Errorf("upgrade: failed to download: %w", err)
	}

	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], expectedChecksum) {
		return false, "", fmt.Errorf("upgrade: checksum verification failed")
	}

	var binary []byte
	if strings.HasSuffix(asset, ".zip") {
		binary, err = extractFromZip(data)
	} else {
		binary, err = extractFromTarGz(data)
	}
	if err != nil {
		return false, "", fmt.Errorf("upgrade: failed to extract binary: %w", err)
	}

	if err := update.Apply(bytes.NewReader(binary), update.Options{}); err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			return false, "", fmt.Errorf("upgrade: %s (try running with sudo)", err)
		}
		return false, "", fmt.Errorf("upgrade: %w", err)
	}

	fmt.Fprintf(w, "Upgraded to v%s\n", latest)
	return true, latest, nil
}

func fetchLatestRelease() (*githubRelease, error) {
	req, err := http.NewRequest("GET", githubAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "retask-cli/"+version.Version)

	resp, err := releaseCheckClient.Do(req)
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

func downloadWithProgress(w io.Writer, url, name string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	tty := isTerminal(w)
	pr := &progressReader{
		r:     resp.Body,
		total: resp.ContentLength,
		name:  name,
		w:     w,
		isTTY: tty,
	}
	data, err := io.ReadAll(pr)
	if tty {
		fmt.Fprintln(w)
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
