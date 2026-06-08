// cmd/retask/main.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	authcmd "nweb.xyz/retask-cli/internal/cmd/auth"
	customercmd "nweb.xyz/retask-cli/internal/cmd/customer"
	filecmd "nweb.xyz/retask-cli/internal/cmd/file"
	integrationcmd "nweb.xyz/retask-cli/internal/cmd/integration"
	projectcmd "nweb.xyz/retask-cli/internal/cmd/project"
	workspacecmd "nweb.xyz/retask-cli/internal/cmd/workspace"
	"nweb.xyz/retask-cli/internal/flags"
	"nweb.xyz/retask-cli/internal/version"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	gf := &flags.Global{}

	root := &cobra.Command{
		Use:          "retask",
		Short:        "Retask CLI — interact with NWEB Retask APIs",
		SilenceUsage: true,
		Version:      version.Version,
	}

	root.PersistentFlags().StringVar(&gf.Profile, "profile", "", "Config profile name (env: RETASK_PROFILE)")
	root.PersistentFlags().StringVar(&gf.WorkspaceID, "workspace-id", "", "Workspace ID (env: NWEB_WORKSPACE_ID)")
	root.PersistentFlags().BoolVar(&gf.Pretty, "pretty", false, "Human-readable table output (default: JSON)")
	root.PersistentFlags().BoolVar(&gf.Insecure, "insecure", false, "Skip TLS verification (local dev only)")
	root.PersistentFlags().BoolVar(&gf.NoSave, "no-save", false, "Don't write credentials to config file")
	root.PersistentFlags().StringVar(&gf.ConfigPath, "config", "", "Config file path (default: ~/.config/retask/config.yaml)")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Apply env overrides for flags not explicitly set
		if gf.Profile == "" {
			gf.Profile = os.Getenv("RETASK_PROFILE")
		}
		if gf.WorkspaceID == "" {
			gf.WorkspaceID = os.Getenv("NWEB_WORKSPACE_ID")
		}
		if os.Getenv("RETASK_NO_PERSIST") != "" {
			gf.NoSave = true
		}
		return nil
	}

	root.SetVersionTemplate(fmt.Sprintf("retask version %s\n", version.Version))

	// Service commands registered here — add one line per new service
	root.AddCommand(authcmd.NewCommand(gf))
	root.AddCommand(customercmd.NewCommand(gf))
	root.AddCommand(filecmd.NewCommand(gf))
	root.AddCommand(integrationcmd.NewCommand(gf))
	root.AddCommand(projectcmd.NewCommand(gf))
	root.AddCommand(workspacecmd.NewCommand(gf))

	return root
}
