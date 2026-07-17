// internal/cmd/sandbox/retention.go
package sandbox

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseDuration parses a retention window. It accepts Go duration syntax
// ("12h", "90m", "0") plus a "d" day suffix, which time.ParseDuration rejects.
func parseDuration(s string) (d time.Duration, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration (want e.g. 30d, 12h, 0)")
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, convErr := strconv.ParseFloat(days, 64)
		if convErr != nil {
			return 0, fmt.Errorf("invalid duration %q (want e.g. 30d, 12h, 0)", s)
		}
		if n < 0 {
			return 0, fmt.Errorf("duration %q must not be negative", s)
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	d, err = time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (want e.g. 30d, 12h, 0)", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %q must not be negative", s)
	}
	return d, nil
}

// parseRetention parses the --retention flag, which additionally accepts "off".
// Zero is rejected: it means "delete everything" for --older-than, so accepting
// it here would turn an hourly sweep into an hourly wipe. Disabling is "off".
func parseRetention(s string) (d time.Duration, enabled bool, err error) {
	if strings.EqualFold(strings.TrimSpace(s), "off") {
		return 0, false, nil
	}
	d, err = parseDuration(s)
	if err != nil {
		return 0, false, err
	}
	if d == 0 {
		return 0, false, fmt.Errorf(`invalid --retention %q: use "off" to disable retention`, s)
	}
	return d, true, nil
}
