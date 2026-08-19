package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	workspacev1 "github.com/nwebxyz/retask-cli/proto-gen/workspace/v1"
)

func TestParseSharing(t *testing.T) {
	t.Run("workspace edit", func(t *testing.T) {
		sh, err := parseSharing("WORKSPACE_EDIT")
		require.NoError(t, err)
		assert.Equal(t, workspacev1.Sharing_SCOPE_WORKSPACE, sh.GetScope())
		assert.Equal(t, workspacev1.Sharing_PERMISSION_EDIT, sh.GetPermission())
	})
	t.Run("workspace view", func(t *testing.T) {
		sh, err := parseSharing("WORKSPACE_VIEW")
		require.NoError(t, err)
		assert.Equal(t, workspacev1.Sharing_SCOPE_WORKSPACE, sh.GetScope())
		assert.Equal(t, workspacev1.Sharing_PERMISSION_VIEW, sh.GetPermission())
	})
	t.Run("private ignores permission", func(t *testing.T) {
		sh, err := parseSharing("PRIVATE")
		require.NoError(t, err)
		assert.Equal(t, workspacev1.Sharing_SCOPE_PRIVATE, sh.GetScope())
		assert.Equal(t, workspacev1.Sharing_PERMISSION_VIEW, sh.GetPermission())
	})
	t.Run("lowercase is accepted", func(t *testing.T) {
		sh, err := parseSharing("workspace_edit")
		require.NoError(t, err)
		assert.Equal(t, workspacev1.Sharing_SCOPE_WORKSPACE, sh.GetScope())
		assert.Equal(t, workspacev1.Sharing_PERMISSION_EDIT, sh.GetPermission())
	})
	t.Run("invalid value errors and lists the valid ones", func(t *testing.T) {
		_, err := parseSharing("RESTRICTED_EDIT")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "WORKSPACE_EDIT")
		assert.Contains(t, err.Error(), "WORKSPACE_VIEW")
		assert.Contains(t, err.Error(), "PRIVATE")
	})
	t.Run("raw proto scope name is not accepted", func(t *testing.T) {
		_, err := parseSharing("SCOPE_WORKSPACE")
		require.Error(t, err)
	})
}
