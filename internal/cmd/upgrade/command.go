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
