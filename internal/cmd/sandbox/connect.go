package sandbox

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/nwebxyz/retask-cli/internal/flags"
)

func newConnectCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "connect <id>",
		Short: "Connect this machine as a Private VM sandbox",
		Long: `Connect this machine as the execution backend for a Private VM sandbox.

This is a long-running command that maintains a persistent WebSocket connection
to sandbox-proxy and manages sessions as local PTY processes.

Usage example:
  retask sandbox connect sandbox_abc123

Environment:
  SANDBOX_PROXY_ENDPOINT  Proxy base URL (default: https://sandbox-proxy.prd.nweb.app/)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Full implementation added in Task 8.
			return nil
		},
	}
}

// proxyWSBase returns the WebSocket base URL for sandbox-proxy,
// converting https→wss and http→ws.
func proxyWSBase() string {
	ep := os.Getenv("SANDBOX_PROXY_ENDPOINT")
	if ep == "" {
		ep = "https://sandbox-proxy.prd.nweb.app/"
	}
	ep = strings.TrimRight(ep, "/")
	ep = strings.Replace(ep, "https://", "wss://", 1)
	ep = strings.Replace(ep, "http://", "ws://", 1)
	return ep
}
