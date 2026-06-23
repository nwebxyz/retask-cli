package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	helpcmd "github.com/nwebxyz/retask-cli/internal/cmd/helpcmd"
)

// globalFlagNames are the persistent flags declared on the root command plus
// cobra's auto-added help flag. They apply to every command and are not listed
// per-command in the help-llm manifest, so the drift check ignores them on both
// sides. "workspace-id" is global but a few commands also re-declare it locally
// as an override; that ambiguity is sidestepped by ignoring it everywhere.
var globalFlagNames = map[string]bool{
	"profile":      true,
	"workspace-id": true,
	"pretty":       true,
	"insecure":     true,
	"no-save":      true,
	"config":       true,
	"verbose":      true,
	"help":         true,
}

// commandFlags returns the command-specific (non-global) flag names a leaf
// command declares.
func commandFlags(c *cobra.Command) []string {
	var names []string
	c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !globalFlagNames[f.Name] {
			names = append(names, f.Name)
		}
	})
	sort.Strings(names)
	return names
}

// documentedFlags strips the leading "--" and drops global flags from a
// manifest entry's flag list.
func documentedFlags(flags []string) []string {
	var names []string
	for _, f := range flags {
		name := strings.TrimPrefix(f, "--")
		if !globalFlagNames[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// leafCommands walks the command tree and returns every runnable leaf command.
func leafCommands(root *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		subs := c.Commands()
		if len(subs) == 0 {
			if c.Runnable() {
				leaves = append(leaves, c)
			}
			return
		}
		for _, sub := range subs {
			walk(sub)
		}
	}
	walk(root)
	return leaves
}

// TestHelpManifestMatchesCommandTree guards against the hand-maintained
// help-llm manifest drifting from the actual cobra command tree: every runnable
// command must be documented, and its documented flags must match the flags it
// declares.
func TestHelpManifestMatchesCommandTree(t *testing.T) {
	root := newRootCommand()
	documented := helpcmd.FlagsByCommand()

	seen := make(map[string]bool)
	for _, c := range leafCommands(root) {
		path := c.CommandPath()
		seen[path] = true

		entry, ok := documented[path]
		if !ok {
			t.Errorf("command %q is not documented in the help-llm manifest", path)
			continue
		}

		want := commandFlags(c)
		got := documentedFlags(entry)
		if !equalStrings(want, got) {
			t.Errorf("command %q flags drift:\n  declared:   %v\n  documented: %v", path, want, got)
		}
	}

	for path := range documented {
		if !seen[path] {
			t.Errorf("manifest documents %q but no such runnable command exists", path)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
