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
