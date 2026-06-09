package flags_test

import (
	"testing"

	"nweb.xyz/retask-cli/internal/config"
	"nweb.xyz/retask-cli/internal/flags"
)

func TestResolveWorkspaceID_FlagWins(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "env-ws")
	profile := config.Profile{WorkspaceID: "profile-ws"}
	got := flags.ResolveWorkspaceID("flag-ws", profile)
	if got != "flag-ws" {
		t.Fatalf("expected flag-ws, got %q", got)
	}
}

func TestResolveWorkspaceID_EnvWinsOverProfile(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "env-ws")
	profile := config.Profile{WorkspaceID: "profile-ws"}
	got := flags.ResolveWorkspaceID("", profile)
	if got != "env-ws" {
		t.Fatalf("expected env-ws, got %q", got)
	}
}

func TestResolveWorkspaceID_ProfileFallback(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "")
	profile := config.Profile{WorkspaceID: "profile-ws"}
	got := flags.ResolveWorkspaceID("", profile)
	if got != "profile-ws" {
		t.Fatalf("expected profile-ws, got %q", got)
	}
}

func TestResolveWorkspaceID_AllEmpty(t *testing.T) {
	t.Setenv("NWEB_WORKSPACE_ID", "")
	profile := config.Profile{}
	got := flags.ResolveWorkspaceID("", profile)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
