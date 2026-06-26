// internal/cmd/skillcmd/command.go
package skillcmd

import (
	"fmt"

	retaskcli "github.com/nwebxyz/retask-cli"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/spf13/cobra"
)

func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print the retask Claude Code skill (Markdown onboarding guide)",
		Long: `Print the retask Claude Code skill file as Markdown.

The skill is a concise onboarding guide — auth, output format, and discovery.
It is embedded in the binary, so this works without the repo. An agent running
in a sandbox can read it to learn how to drive the CLI, then use retask help-llm
for the full machine-readable command manifest.

Usage example:
  retask skill
  retask skill > retask-cli.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), retaskcli.SkillMarkdown)
			return err
		},
	}
	return cmd
}
