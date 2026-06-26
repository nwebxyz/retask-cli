package sandbox

import (
	"encoding/base64"
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

// --- normalizeGithubURL ---

func TestNormalizeGithubURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{
			"git@github.com:foo/bar.git",
			"https://github.com/foo/bar.git", // ssh form rewritten to https
		},
		{
			"https://github.com/foo/bar.git",
			"https://github.com/foo/bar.git", // already https: unchanged
		},
		{
			"https://gitlab.com/foo/bar.git",
			"https://gitlab.com/foo/bar.git", // non-github: unchanged
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, normalizeGithubURL(tc.url), "url=%q", tc.url)
	}
}

// --- gitTokenEnv ---

func TestGitTokenEnv_EmptyToken(t *testing.T) {
	assert.Nil(t, gitTokenEnv(""))
}

func TestGitTokenEnv_InjectsScopedAuthHeader(t *testing.T) {
	token := "ghp_TOKEN"
	m := envToMap(gitTokenEnv(token))

	assert.Equal(t, "1", m["GIT_CONFIG_COUNT"])
	assert.Equal(t, "http.https://github.com/.extraHeader", m["GIT_CONFIG_KEY_0"])
	assert.Equal(t, "0", m["GIT_TERMINAL_PROMPT"])

	// The header value decodes to x-access-token:<token>.
	const prefix = "Authorization: Basic "
	val := m["GIT_CONFIG_VALUE_0"]
	assert.True(t, strings.HasPrefix(val, prefix), "value=%q", val)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(val, prefix))
	assert.NoError(t, err)
	assert.Equal(t, "x-access-token:"+token, string(decoded))
}

func TestGitTokenEnv_NeverLeaksRawToken(t *testing.T) {
	token := "ghp_SECRET"
	for _, e := range gitTokenEnv(token) {
		assert.NotContains(t, e, token, "raw token leaked in env entry: %q", e)
	}
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

func TestBuildEnv_StripsHostAuthVars(t *testing.T) {
	// Host-provided auth/workspace vars must never leak into the session.
	base := []string{
		"PATH=/usr/bin",
		"NWEB_API_TOKEN=host-jwt",
		"NWEB_API_KEY=host-pat",
		"NWEB_WORKSPACE_ID=host-ws",
	}
	cfg := &sandboxv1.Sandbox_Config{}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)

	assert.Equal(t, "/usr/bin", m["PATH"], "unrelated host vars must survive")
	_, hasToken := m["NWEB_API_TOKEN"]
	_, hasKey := m["NWEB_API_KEY"]
	_, hasWS := m["NWEB_WORKSPACE_ID"]
	assert.False(t, hasToken, "NWEB_API_TOKEN must be stripped from host layer")
	assert.False(t, hasKey, "NWEB_API_KEY must be stripped from host layer")
	assert.False(t, hasWS, "NWEB_WORKSPACE_ID must be stripped from host layer")
}

func TestBuildEnv_InjectedSetsStrippedAuthVars(t *testing.T) {
	// Stripping the host layer must not block injected session values.
	base := []string{"NWEB_API_TOKEN=host-jwt", "NWEB_WORKSPACE_ID=host-ws"}
	cfg := &sandboxv1.Sandbox_Config{}
	injected := map[string]string{
		"NWEB_API_TOKEN":    "session-jwt",
		"NWEB_WORKSPACE_ID": "session-ws",
	}
	env := buildEnv(base, cfg, injected)
	m := envToMap(env)
	assert.Equal(t, "session-jwt", m["NWEB_API_TOKEN"])
	assert.Equal(t, "session-ws", m["NWEB_WORKSPACE_ID"])
}

func TestBuildEnv_ConfigCanSetStrippedAuthVars(t *testing.T) {
	// Stripping is host-only: a deliberate Sandbox Config value still applies.
	base := []string{"NWEB_WORKSPACE_ID=host-ws"}
	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "NWEB_WORKSPACE_ID", Plain: "config-ws"},
		},
	}
	env := buildEnv(base, cfg, nil)
	m := envToMap(env)
	assert.Equal(t, "config-ws", m["NWEB_WORKSPACE_ID"])
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
