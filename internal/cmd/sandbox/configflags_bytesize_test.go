package sandbox

import "testing"

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"0", 0, false},
		{"1024", 1024, false},
		{"512KB", 512 * 1024, false},
		{"10MB", 10 * 1024 * 1024, false},
		{"1GB", 1 << 30, false},
		{"2mb", 2 * 1024 * 1024, false}, // case-insensitive
		{" 4MB ", 4 * 1024 * 1024, false},
		{"100B", 100, false},
		{"", 0, true},
		{"-5", 0, true},
		{"-1MB", 0, true},
		{"abc", 0, true},
		{"10XB", 0, true},
	}
	for _, tc := range cases {
		got, err := parseByteSize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseByteSize(%q) = %d, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseByteSize(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseByteSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
