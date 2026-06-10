// cmd/retask/main.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	agentcmd "github.com/nwebxyz/retask-cli/internal/cmd/agent"
	authcmd "github.com/nwebxyz/retask-cli/internal/cmd/auth"
	customercmd "github.com/nwebxyz/retask-cli/internal/cmd/customer"
	filecmd "github.com/nwebxyz/retask-cli/internal/cmd/file"
	helpcmd "github.com/nwebxyz/retask-cli/internal/cmd/helpcmd"
	integrationcmd "github.com/nwebxyz/retask-cli/internal/cmd/integration"
	projectcmd "github.com/nwebxyz/retask-cli/internal/cmd/project"
	projectconfigcmd "github.com/nwebxyz/retask-cli/internal/cmd/projectconfig"
	sandboxcmd "github.com/nwebxyz/retask-cli/internal/cmd/sandbox"
	taskcmd "github.com/nwebxyz/retask-cli/internal/cmd/task"
	workspacecmd "github.com/nwebxyz/retask-cli/internal/cmd/workspace"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/version"
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
	root.PersistentFlags().BoolVarP(&gf.Verbose, "verbose", "v", false, "Print request and response info to stderr")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if gf.Profile == "" {
			gf.Profile = os.Getenv("RETASK_PROFILE")
		}
		if os.Getenv("RETASK_NO_PERSIST") != "" {
			gf.NoSave = true
		}
		if gf.Transport == "" {
			gf.Transport = os.Getenv("NWEB_API_TRANSPORT")
		}

		// Resolve workspace ID: flag > env > profile
		configPath := gf.ConfigPath
		if configPath == "" {
			configPath = config.DefaultConfigPath()
		}
		if cfg, err := config.Load(configPath); err == nil {
			profile := cfg.ActiveProfileData(gf.Profile)
			gf.WorkspaceID = flags.ResolveWorkspaceID(gf.WorkspaceID, profile)
		} else {
			gf.WorkspaceID = flags.ResolveWorkspaceID(gf.WorkspaceID, config.Profile{})
		}

		return nil
	}

	root.SetVersionTemplate(fmt.Sprintf("retask version %s\n", version.Version))

	// Service commands registered here — add one line per new service
	root.AddCommand(agentcmd.NewCommand(gf))
	root.AddCommand(authcmd.NewCommand(gf))
	root.AddCommand(customercmd.NewCommand(gf))
	root.AddCommand(filecmd.NewCommand(gf))
	root.AddCommand(helpcmd.NewCommand(gf))
	root.AddCommand(integrationcmd.NewCommand(gf))
	root.AddCommand(projectcmd.NewCommand(gf))
	root.AddCommand(projectconfigcmd.NewCommand(gf))
	root.AddCommand(sandboxcmd.NewCommand(gf))
	root.AddCommand(taskcmd.NewCommand(gf))
	root.AddCommand(workspacecmd.NewCommand(gf))

	return root
}
