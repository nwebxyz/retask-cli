package sandbox

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hoaitan/agentfleet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
)

// Spawns a real PTY process through the production config path. buildEnv's
// output was always correct; the leak was agentfleet appending it to
// os.Environ(), which put back the operator's terminal identity (making Claude
// Code poll DECXCPR forever) and their PAT. Only the spawned process shows it.
func TestSessionPtyConfig_ChildSeesOnlyBuiltEnv(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("LC_TERMINAL", "iTerm2")
	t.Setenv("NWEB_API_KEY", "pat-must-not-leak")

	cfg := &sandboxv1.Sandbox_Config{
		EnvVars: []*sandboxv1.Sandbox_Config_EnvVar{
			{Key: "NWEB_API_TOKEN", Plain: "session-token"},
		},
	}
	env := buildEnv(os.Environ(), cfg, map[string]string{"SESSION_ID": "s1"})

	agCfg := sessionPtyConfig(agentfleet.AgentConfig{}, env, 200, 10)
	script := `echo "TP=[${TERM_PROGRAM-unset}] LT=[${LC_TERMINAL-unset}] KEY=[${NWEB_API_KEY-unset}] SID=[${SESSION_ID-unset}] TERM=[${TERM-unset}] TOK=[${NWEB_API_TOKEN-unset}]"`
	ag := agentfleet.NewPtyAgent([]string{"sh", "-c", script}, agCfg)
	r := agentfleet.NewRunner(&agentfleet.BasicTask{TaskID: "env", TaskName: "env", Cmd: "sh"}, ag, agentfleet.FleetConfig{}, agCfg)
	r.Start()
	t.Cleanup(func() { r.Stop() }) //nolint:errcheck

	select {
	case <-r.Done():
	case <-time.After(10 * time.Second):
		require.Fail(t, "session process never exited")
	}

	screen := strings.Join(r.Lines(), "\n")
	assert.Contains(t, screen, "TP=[unset]")
	assert.Contains(t, screen, "LT=[unset]")
	assert.Contains(t, screen, "KEY=[unset]")
	assert.NotContains(t, screen, "pat-must-not-leak")
	assert.Contains(t, screen, "SID=[s1]")
	assert.Contains(t, screen, "TERM=[xterm-256color]")
	assert.Contains(t, screen, "TOK=[session-token]")
}
