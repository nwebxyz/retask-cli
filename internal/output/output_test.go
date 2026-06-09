// internal/output/output_test.go
package output_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/nwebxyz/retask-cli/internal/output"
)

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	err := output.Fprint(&buf, false, map[string]string{"key": "value"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), `"key": "value"`)
}

func TestPrintPretty(t *testing.T) {
	var buf bytes.Buffer
	err := output.Fprint(&buf, true, []map[string]any{
		{"id": "abc", "name": "Test"},
	})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "abc")
	assert.Contains(t, buf.String(), "Test")
}
