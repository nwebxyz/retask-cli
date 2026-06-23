package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEnvVar(t *testing.T) {
	t.Run("key and value", func(t *testing.T) {
		ev, err := parseEnvVar("FOO=bar")
		require.NoError(t, err)
		assert.Equal(t, "FOO", ev.Key)
		assert.Equal(t, "bar", ev.Plain)
	})
	t.Run("value contains equals (split on first only)", func(t *testing.T) {
		ev, err := parseEnvVar("FOO=a=b=c")
		require.NoError(t, err)
		assert.Equal(t, "FOO", ev.Key)
		assert.Equal(t, "a=b=c", ev.Plain)
	})
	t.Run("empty value is allowed", func(t *testing.T) {
		ev, err := parseEnvVar("FOO=")
		require.NoError(t, err)
		assert.Equal(t, "FOO", ev.Key)
		assert.Equal(t, "", ev.Plain)
	})
	t.Run("missing equals errors", func(t *testing.T) {
		_, err := parseEnvVar("FOO")
		require.Error(t, err)
	})
	t.Run("empty key errors", func(t *testing.T) {
		_, err := parseEnvVar("=bar")
		require.Error(t, err)
	})
}
