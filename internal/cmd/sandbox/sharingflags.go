// internal/cmd/sandbox/sharingflags.go
package sandbox

import (
	"fmt"
	"strings"

	workspacev1 "github.com/nwebxyz/retask-cli/proto-gen/workspace/v1"
)

// sharingNames lists the accepted --sharing values in help-text order, so the
// flag description and every error message stay in sync with sharingValues.
var sharingNames = []string{"WORKSPACE_EDIT", "WORKSPACE_VIEW", "PRIVATE"}

// sharingValues maps each accepted --sharing value to its Sharing message.
// Scope and permission are paired here rather than exposed as two flags: the
// combinations left out (a PRIVATE scope carrying a permission, RESTRICTED
// without the member list to go with it) are not states a sandbox can be in.
var sharingValues = map[string]*workspacev1.Sharing{
	"WORKSPACE_EDIT": {
		Scope:      workspacev1.Sharing_SCOPE_WORKSPACE,
		Permission: workspacev1.Sharing_PERMISSION_EDIT,
	},
	"WORKSPACE_VIEW": {
		Scope:      workspacev1.Sharing_SCOPE_WORKSPACE,
		Permission: workspacev1.Sharing_PERMISSION_VIEW,
	},
	// Permission is left unset: the server ignores it for a private sandbox.
	"PRIVATE": {Scope: workspacev1.Sharing_SCOPE_PRIVATE},
}

// parseSharing resolves a --sharing value to the Sharing message to send. It
// returns a fresh message each call so a caller can never mutate the table.
func parseSharing(s string) (*workspacev1.Sharing, error) {
	v, ok := sharingValues[strings.ToUpper(strings.TrimSpace(s))]
	if !ok {
		return nil, fmt.Errorf("invalid --sharing %q. Valid values: %s", s, strings.Join(sharingNames, ", "))
	}
	return &workspacev1.Sharing{Scope: v.GetScope(), Permission: v.GetPermission()}, nil
}
