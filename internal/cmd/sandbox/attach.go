package sandbox

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func newAttachCommand(_ *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <session-id>",
		Short: "Attach terminal to a running local session",
		Long: `Attach your terminal to a locally running sandbox session.

Usage example:
  retask sandbox attach 20eb7125-14be-487f-9310-ca22b0a20670`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sockPath := filepath.Join("/tmp", "agentfleet-"+args[0]+".sock")
			conn, err := net.Dial("unix", sockPath)
			if err != nil {
				return fmt.Errorf("session not found or not running: %w", err)
			}
			defer conn.Close()

			fd := int(os.Stdin.Fd())
			oldState, err := term.MakeRaw(fd)
			if err != nil {
				return err
			}
			defer term.Restore(fd, oldState) //nolint:errcheck

			go io.Copy(conn, os.Stdin) //nolint:errcheck
			io.Copy(os.Stdout, conn)   //nolint:errcheck
			return nil
		},
	}
}
