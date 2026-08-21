package upgrade

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nwebxyz/retask-cli/internal/version"
)

// withFakeGithubAPI points githubAPI at a test server that always returns
// rel, restoring the original value on cleanup.
func withFakeGithubAPI(t *testing.T, rel githubRelease) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rel)
	}))
	t.Cleanup(srv.Close)

	orig := githubAPI
	githubAPI = srv.URL
	t.Cleanup(func() { githubAPI = orig })
}

// withVersion sets version.Version for the duration of the test, restoring
// the original value on cleanup.
func withVersion(t *testing.T, v string) {
	t.Helper()
	orig := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = orig })
}

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

func TestCheckLatestVersion(t *testing.T) {
	withFakeGithubAPI(t, githubRelease{TagName: "v1.2.3"})

	t.Run("up to date", func(t *testing.T) {
		withVersion(t, "1.2.3")
		latest, upToDate, err := CheckLatestVersion()
		require.NoError(t, err)
		assert.Equal(t, "1.2.3", latest)
		assert.True(t, upToDate)
	})

	t.Run("newer available", func(t *testing.T) {
		withVersion(t, "1.0.0")
		latest, upToDate, err := CheckLatestVersion()
		require.NoError(t, err)
		assert.Equal(t, "1.2.3", latest)
		assert.False(t, upToDate)
	})
}

func TestCheckLatestVersionNetworkError(t *testing.T) {
	orig := githubAPI
	githubAPI = "http://127.0.0.1:0" // nothing listens here
	t.Cleanup(func() { githubAPI = orig })

	_, _, err := CheckLatestVersion()
	assert.Error(t, err)
}

func TestRunAlreadyUpToDateSkipsDownload(t *testing.T) {
	withFakeGithubAPI(t, githubRelease{TagName: "v1.2.3"})
	withVersion(t, "1.2.3")

	didUpgrade, err := Run()
	require.NoError(t, err)
	assert.False(t, didUpgrade, "already up to date: nothing should be applied")
}

func TestRunRejectsDevBuild(t *testing.T) {
	withVersion(t, "dev")

	didUpgrade, err := Run()
	assert.ErrorContains(t, err, "dev build")
	assert.False(t, didUpgrade)
}
