package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	connectrpc "connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	agentfleet "github.com/hoaitan/agentfleet"
	"github.com/hoaitan/agentfleet/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
	sandboxv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1/sandboxv1connect"
)

func newConnectCommand(gf *flags.Global) *cobra.Command {
	var mode string
	var autoOpen bool
	cmd := &cobra.Command{
		Use:   "connect <id>",
		Short: "Connect this machine as a Private VM sandbox",
		Long: `Connect this machine as the execution backend for a Private VM sandbox.

This is a long-running command that maintains a persistent WebSocket connection
to sandbox-proxy and manages sessions as local PTY processes.

Usage example:
  retask sandbox connect sandbox_abc123
  retask sandbox connect sandbox_abc123 --mode headless
  retask sandbox connect sandbox_abc123 --auto-open

Flags:
  --mode string  Running mode: auto, tui, headless (default: auto)
  --auto-open    Auto-open a terminal tab for each new session (default: false)

Environment:
  SANDBOX_PROXY_ENDPOINT   Proxy base URL (default: https://sandbox-proxy.prd.nweb.app/)
  RETASK_SANDBOX_AUTO_OPEN_SESSION=1  Enable auto-open without the flag`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if mode != "auto" && mode != "tui" && mode != "headless" {
				return fmt.Errorf("invalid --mode %q: must be auto, tui, or headless", mode)
			}
			sandboxID := args[0]

			// Resolve credentials.
			path := gf.ConfigPath
			if path == "" {
				path = config.DefaultConfigPath()
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			profile := cfg.ActiveProfileData(gf.Profile)
			resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			jwt, err := resolver.Token(ctx)
			if err != nil {
				return err
			}

			// Build sandbox service client.
			httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
			baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
			svc := sandboxv1connect.NewSandboxServiceClient(httpClient, baseURL, client.Options(gf.Transport)...)

			// Validate sandbox type.
			sbResp, err := svc.GetSandbox(ctx, connectrpc.NewRequest(&commonv1.Id{Id: sandboxID}))
			if err != nil {
				return err
			}
			if sbResp.Msg.Type != sandboxv1.Sandbox_TYPE_PRIVATE {
				return fmt.Errorf("sandbox %q must be type PRIVATE (got %s)", sandboxID, sbResp.Msg.Type)
			}

			wsBase := proxyWSBase()

			// Connection state: 0=connecting, 1=connected, 2=error.
			var rawConnState int32
			atomic.StoreInt32(&rawConnState, connStateConnecting)

			useTUI := mode == "tui" || (mode == "auto" && term.IsTerminal(int(os.Stdout.Fd())))

			// LogBuffer captures all events; in TUI mode it feeds the log panel,
			// in headless mode it drains to stderr so output is identical.
			logBuf := agentfleet.NewLogBuffer(500)
			var logOut io.Writer = os.Stderr
			if useTUI {
				logOut = logBuf
			}
			logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))

			// agentfleet config.
			fleetCfg := agentfleet.DefaultConfig()
			fleetCfg.TUI.Title = makeTitleFunc(sbResp.Msg.Name, sbResp.Msg.SandboxId)
			fleetCfg.TUI.TitleRight = makeConnStatusFunc(&rawConnState)
			fleetCfg.TUI.AutoOpen = autoOpen || os.Getenv("RETASK_SANDBOX_AUTO_OPEN_SESSION") == "1"
			fleetCfg.TUI.FilterLines = filterLines
			if useTUI {
				fleetCfg.TUI.Log = logBuf
			}
			fleet := agentfleet.NewFleet(fleetCfg.Fleet)

			sm := newSessionManager(sandboxID, wsBase, fleet, fleetCfg.Fleet, fleetCfg.Agent, logger)
			dl := newDataLane(sandboxID, wsBase, jwt, sm, &rawConnState, logger)

			go dl.Run(ctx)

			if useTUI {
				execPath, _ := os.Executable()
				onAttach := func(taskID string) {
					tui.OpenInTerminal(execPath, "sandbox", "attach", taskID)
				}
				fleetCfg.TUI.OnClose = func(taskID string) {
					sm.Stop(taskID) // terminate local PTY immediately
					dl.Send(dataLaneMsg{Type: "terminate_session", SessionID: taskID})
				}
				if err := tui.Run(ctx, fleet, fleetCfg.TUI, onAttach); err != nil {
					return err
				}
				stop() // disconnect data lane when TUI exits
			} else {
				<-ctx.Done()
			}

			sm.StopAll()
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "auto", "Running mode: auto, tui, headless")
	cmd.Flags().BoolVar(&autoOpen, "auto-open", false, "Auto-open a terminal tab for each new session")
	return cmd
}

// proxyWSBase returns the WebSocket base URL for sandbox-proxy.
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

// makeTitleFunc returns the static left-side header: logo + sandbox name + dim full ID.
func makeTitleFunc(name, id string) func() string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#c084fc")).Render("◈ retask")
	dimID := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(id)
	label := name + "  " + dimID
	return func() string { return logo + "  " + label }
}

// makeConnStatusFunc returns the right-side connection status indicator.
func makeConnStatusFunc(connState *int32) func() string {
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))
	gray := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	return func() string {
		switch atomic.LoadInt32(connState) {
		case connStateConnected:
			return green.Render("● connected")
		case connStateError:
			return red.Render("● error")
		default:
			return gray.Render("○ connecting")
		}
	}
}
