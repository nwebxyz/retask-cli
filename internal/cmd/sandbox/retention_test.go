package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"0d", 0},
		{"12h", 12 * time.Hour},
		{"90m", 90 * time.Minute},
		{"0", 0},
		{" 7d ", 7 * 24 * time.Hour},
	}
	for _, tc := range tests {
		got, err := parseDuration(tc.in)
		require.NoError(t, err, "in=%q", tc.in)
		assert.Equal(t, tc.want, got, "in=%q", tc.in)
	}
}

func TestParseDurationRejects(t *testing.T) {
	for _, in := range []string{"", "off", "30days", "-1d", "-5h", "abc", "d"} {
		_, err := parseDuration(in)
		assert.Error(t, err, "in=%q should be rejected", in)
	}
}

func TestParseRetention(t *testing.T) {
	d, enabled, err := parseRetention("30d")
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, 30*24*time.Hour, d)

	for _, in := range []string{"off", "OFF", " off "} {
		_, enabled, err := parseRetention(in)
		require.NoError(t, err, "in=%q", in)
		assert.False(t, enabled, "in=%q should disable retention", in)
	}
}

func TestParseRetentionRejectsZero(t *testing.T) {
	// 0 means "delete everything" for --older-than; allowing it here would turn
	// an hourly sweep into an hourly wipe. Disabling is spelled "off".
	_, _, err := parseRetention("0")
	assert.Error(t, err)
	_, _, err = parseRetention("0d")
	assert.Error(t, err)
}
