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

func TestParseGitRepo(t *testing.T) {
	t.Run("url only", func(t *testing.T) {
		r, err := parseGitRepo("url=https://github.com/org/repo")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo", r.Url)
		assert.Equal(t, "", r.Branch)
		assert.Equal(t, "", r.TargetDir)
	})
	t.Run("url branch dir", func(t *testing.T) {
		r, err := parseGitRepo("url=https://github.com/org/repo,branch=dev,dir=src")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo", r.Url)
		assert.Equal(t, "dev", r.Branch)
		assert.Equal(t, "src", r.TargetDir)
	})
	t.Run("ssh url with @ and colon", func(t *testing.T) {
		r, err := parseGitRepo("url=git@github.com:org/repo.git,branch=main")
		require.NoError(t, err)
		assert.Equal(t, "git@github.com:org/repo.git", r.Url)
		assert.Equal(t, "main", r.Branch)
	})
	t.Run("order independent", func(t *testing.T) {
		r, err := parseGitRepo("branch=dev,url=https://github.com/org/repo")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo", r.Url)
		assert.Equal(t, "dev", r.Branch)
	})
	t.Run("missing url errors", func(t *testing.T) {
		_, err := parseGitRepo("branch=dev")
		require.Error(t, err)
	})
	t.Run("unknown key errors", func(t *testing.T) {
		_, err := parseGitRepo("url=https://x,depth=1")
		require.Error(t, err)
	})
	t.Run("segment without equals errors", func(t *testing.T) {
		_, err := parseGitRepo("url=https://x,oops")
		require.Error(t, err)
	})
}

func TestParseShutdownPolicy(t *testing.T) {
	t.Run("valid values", func(t *testing.T) {
		cases := map[string]int32{
			"ON_IDLE_NO_USER_ACTIONS": 0,
			"ON_IDLE":                 1,
			"NEVER":                   2,
		}
		for in, want := range cases {
			p, err := parseShutdownPolicy(in)
			require.NoError(t, err, in)
			assert.Equal(t, want, int32(p), in)
		}
	})
	t.Run("invalid value errors", func(t *testing.T) {
		_, err := parseShutdownPolicy("SOMETIMES")
		require.Error(t, err)
	})
}
