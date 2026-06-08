// internal/cmd/task/time.go
package task

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// parseTimestamp parses an RFC3339 string into a protobuf Timestamp.
func parseTimestamp(s string) (*timestamppb.Timestamp, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	return timestamppb.New(t), nil
}
