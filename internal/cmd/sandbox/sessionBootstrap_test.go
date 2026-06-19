package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// --- deriveTargetDir ---

func TestDeriveTargetDir(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:nwebxyz/api-contracts.git", "api-contracts"},
		{"https://github.com/foo/bar.git", "bar"},
		{"https://github.com/foo/bar", "bar"},
		{"https://github.com/foo/bar/", "bar"},
		{"https://example.com/deep/path/repo.git", "repo"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, deriveTargetDir(tc.url), "url=%q", tc.url)
	}
}

// --- injectGithubToken ---

func TestInjectGithubToken(t *testing.T) {
	token := "ghp_TOKEN"
	tests := []struct {
		url  string
		want string
	}{
		{
			"https://github.com/foo/bar.git",
			"https://oauth2:ghp_TOKEN@github.com/foo/bar.git",
		},
		{
			"git@github.com:foo/bar.git",
			"https://oauth2:ghp_TOKEN@github.com/foo/bar.git",
		},
		{
			"https://gitlab.com/foo/bar.git",
			"https://gitlab.com/foo/bar.git", // non-github: unchanged
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, injectGithubToken(tc.url, token), "url=%q", tc.url)
	}
}

func TestInjectGithubToken_EmptyToken(t *testing.T) {
	url := "https://github.com/foo/bar.git"
	assert.Equal(t, url, injectGithubToken(url, ""))
}

// --- buildEnv ---

func TestBuildEnv_HostLayer(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}
	cfg := &sandboxv1.Sandbox_Config{}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "/usr/bin", m["PATH"])
	assert.Equal(t, "/root", m["HOME"])
}

func TestBuildEnv_ConfigOverridesHost(t *testing.T) {
	base := []string{"MY_VAR=host_value"}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "MY_VAR", Plain: "config_value"},
		},
	}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "config_value", m["MY_VAR"])
}

func TestBuildEnv_SecretOverridesPlain(t *testing.T) {
	base := []string{}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{
				Key:   "API_KEY",
				Plain: "not-this",
				Secret: &sandboxv1.Sandbox_Config_EnvVar_SecretValue{
					Value: "real-secret",
				},
			},
		},
	}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "real-secret", m["API_KEY"])
}

func TestBuildEnv_InjectedWinsAll(t *testing.T) {
	base := []string{"SESSION_ID=old"}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "SESSION_ID", Plain: "also-old"},
		},
	}
	injected := map[string]string{"SESSION_ID": "injected"}
	env := buildEnv(base, cfg, injected)
	m := envToMap(env)
	assert.Equal(t, "injected", m["SESSION_ID"])
}

func TestBuildEnv_SkipsEmptyKey(t *testing.T) {
	base := []string{}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "", Plain: "should-be-skipped"},
		},
	}
	env := buildEnv(base, cfg, nil)
	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "="), "empty key leaked: %q", e)
	}
}

// --- parseChoiceFrom ---

func TestParseChoiceFrom(t *testing.T) {
	tests := []struct {
		input []byte
		want  int
	}{
		{[]byte("1"), 1},
		{[]byte("2"), 2},
		{[]byte("3"), 3},
		{[]byte("4"), 0},
		{[]byte{'\r'}, 0},
		{[]byte{'\n'}, 0},
		{[]byte("12"), 1}, // first valid char wins
		{[]byte("abc2"), 2},
		{[]byte{}, 0},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, parseChoiceFrom(tc.input), "input=%q", tc.input)
	}
}

// helpers

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}
