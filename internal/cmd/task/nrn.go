// internal/cmd/task/nrn.go
package task

import (
	"fmt"
	"strings"

	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
)

// parseNrn parses an NRN string of the form
// "domain:service:resource_type:resource_id" into a commonv1.Nrn. It mirrors
// the server-side parser: exactly four colon-separated parts are required.
func parseNrn(s string) (*commonv1.Nrn, error) {
	parts := strings.SplitN(s, ":", 4)
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid NRN %q: expected domain:service:resource_type:resource_id", s)
	}
	return &commonv1.Nrn{
		Domain:       parts[0],
		Service:      parts[1],
		ResourceType: parts[2],
		ResourceId:   parts[3],
	}, nil
}
