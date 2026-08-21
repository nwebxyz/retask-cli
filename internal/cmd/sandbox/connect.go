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
	"github.com/nwebxyz/retask-cli/internal/version"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	sandboxv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1"
	sandboxv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/sandbox/v1/sandboxv1connect"
)

// Session PTY defaults. Sessions start at a generously sized, conventional
// terminal so the agent's TUI (e.g. Claude Code's trust dialog) renders fully
// for the screen watcher to detect; a 90-row height keeps the whole startup
// render on screen. A later session_resize from an attached client corrects the
// size, which now also moves the emulator (agentfleet >= v0.6.27).
const (
	sessionPTYCols = 80
	sessionPTYRows = 90
)

// sessionAgentConfig returns the agentfleet AgentConfig used for session PTYs.
func sessionAgentConfig() agentfleet.AgentConfig {
	a := agentfleet.DefaultConfig().Agent
	a.PTYCols = sessionPTYCols
	a.PTYRows = sessionPTYRows
	return a
}

func newConnectCommand(gf *flags.Global) *cobra.Command {
	var mode string
	var autoOpen bool
	var noAutoRespond bool
	var retention string
	var sessionBuffer string
	var logFlags logFileFlags
	cmd := &cobra.Command{
		Use:   "connect <id>",
		Short: "Connect this machine as a Private VM sandbox",
		Long: `Connect this machine as the execution backend for a Private VM sandbox.

This is a long-running command that maintains a persistent WebSocket connection
to sandbox-proxy and manages sessions as local PTY processes.

Session folders are created in the current directory and recorded in
sandbox_<sandbox-id>.json. Stopping a session, the sandbox, or this command leaves them
on disk; --retention deletes the ones older than its window, checked hourly.

Usage example:
  retask sandbox connect sandbox_abc123
  retask sandbox connect sandbox_abc123 --mode headless
  retask sandbox connect sandbox_abc123 --auto-open
  retask sandbox connect sandbox_abc123 --retention 7d
  retask sandbox connect sandbox_abc123 --retention off

A dropped connection never tears down a running agent: the agent PTY is stopped
only by an explicit stop/delete. Both lanes self-heal — the data lane and the
session lane reconnect on their own with exponential backoff. While a session
lane is down, its output is buffered (drop-oldest) and flushed to the proxy on
reconnect, so the viewer keeps the most recent output across the gap.

Every log line goes to two places: the TUI log panel (stderr in headless mode)
and retask.log in the current folder. The log file rotates Unix-style — the live
file keeps its name and older generations shift down through retask.log.1,
retask.log.2, ... up to --log-backups. The TUI Logs divider shows the active path.

Flags:
  --mode string       Running mode: auto, tui, headless (default: auto)
  --auto-open         Auto-open a terminal tab for each new session (default: false)
  --no-auto-respond   Disable auto-accepting known agent startup prompts (default: false)
  --retention string  Delete session folders older than this, checked hourly. Values: 30d, 12h, off (default: 30d)
  --session-buffer    Per-session output retained across a session-lane drop, flushed on
                      reconnect (default: 10MB). 0 disables buffering. Accepts 512KB, 10MB, ...
  --log-file string   Log file written alongside the TUI/stderr output, relative to the
                      current folder (default: retask.log)
  --no-log-file       Do not write a log file; log to the TUI/stderr only (default: false)
  --log-max-size      Rotate the log file once it exceeds this size (default: 10MB).
                      0 disables rotation. Accepts 512KB, 10MB, ...
  --log-backups int   Rotated log generations kept, retask.log.1 ... retask.log.N
                      (default: 5). 0 keeps none
  --no-log-path       Hide the log file path from the TUI Logs divider (default: false)

Environment:
  SANDBOX_PROXY_ENDPOINT   Proxy base URL (default: https://sandbox-proxy.prd.nweb.app/)
  RETASK_SANDBOX_AUTO_OPEN_SESSION=1  Enable auto-open without the flag
  RETASK_SANDBOX_NO_AUTO_RESPOND=1    Disable prompt auto-response without the flag
  RETASK_SANDBOX_SESSION_BUFFER       Session output buffer size (overridden by --session-buffer)
  RETASK_SANDBOX_LOG_FILE             Log file path (overridden by --log-file)
  RETASK_SANDBOX_NO_LOG_FILE=1        Disable the log file without the flag
  RETASK_SANDBOX_LOG_MAX_SIZE         Log rotation threshold (overridden by --log-max-size)
  RETASK_SANDBOX_LOG_BACKUPS          Rotated generations kept (overridden by --log-backups)
  RETASK_SANDBOX_NO_LOG_PATH=1        Hide the log path from the TUI without the flag`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if mode != "auto" && mode != "tui" && mode != "headless" {
				return fmt.Errorf("invalid --mode %q: must be auto, tui, or headless", mode)
			}
			retentionWindow, retentionOn, err := parseRetention(retention)
			if err != nil {
				return err
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

			// agentfleet config.
			fleetCfg := agentfleet.DefaultConfig()
			fleetCfg.Agent = sessionAgentConfig()
			fleetCfg.TUI.Title = makeTitleFunc(sbResp.Msg.Name, sbResp.Msg.SandboxId)
			fleetCfg.TUI.TitleRight = makeConnStatusFunc(&rawConnState)
			fleetCfg.TUI.AutoOpen = autoOpen || os.Getenv("RETASK_SANDBOX_AUTO_OPEN_SESSION") == "1"
			fleetCfg.TUI.FilterLines = filterLines

			logCfg, showLogPath, err := logFlags.resolve(cmd)
			if err != nil {
				return err
			}
			// A disabled log file yields a nil *LogFile, which discards writes,
			// so the tee below needs no branch for it.
			logFile, err := agentfleet.OpenLogFile(logCfg)
			if err != nil {
				return err
			}
			defer logFile.Close() //nolint:errcheck

			// LogBuffer captures all events; in TUI mode it feeds the log panel,
			// in headless mode it drains to stderr so output is identical. Either
			// way the same lines are appended to the log file.
			logBuf := agentfleet.NewLogBuffer(500)
			var termOut io.Writer = os.Stderr
			if useTUI {
				termOut = logBuf
				fleetCfg.TUI.Log = logBuf
				fleetCfg.TUI.LogPath = logFile.Path()
				fleetCfg.TUI.ShowLogPath = showLogPath
			}
			logger := slog.New(slog.NewTextHandler(io.MultiWriter(termOut, logFile), &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))

			// Startup line for debugging: pins the CLI version and connect params.
			logger.Info("retask sandbox connect",
				"version", version.Version,
				"sandbox_id", sandboxID,
				"sandbox_name", sbResp.Msg.Name,
				"mode", mode,
				"tui", useTUI,
				"proxy", wsBase,
				"log_file", logFile.Path(),
			)

			fleet := agentfleet.NewFleet(fleetCfg.Fleet)

			baseDir, err := os.Getwd()
			if err != nil {
				return err
			}
			sessLog := newSessionLog(baseDir, sandboxID)
			autoRespond := !(noAutoRespond || os.Getenv("RETASK_SANDBOX_NO_AUTO_RESPOND") == "1")

			// Per-session output buffer: flag wins, else env, else default.
			if v := os.Getenv("RETASK_SANDBOX_SESSION_BUFFER"); v != "" && !cmd.Flags().Changed("session-buffer") {
				sessionBuffer = v
			}
			sessionBufBytes, err := parseByteSize(sessionBuffer)
			if err != nil {
				return fmt.Errorf("--session-buffer: %w", err)
			}

			sm := newSessionManager(
				sandboxID, wsBase,
				fleet, fleetCfg.Fleet, fleetCfg.Agent,
				logger,
				sbResp.Msg.WorkspaceId,
				sbResp.Msg.Name,
				baseDir,
				profile.Endpoint,
				sessLog,
				autoRespond,
				sessionBufBytes,
			)

			if retentionOn {
				sweeper := &retentionSweeper{
					log:      sessLog,
					baseDir:  baseDir,
					window:   retentionWindow,
					interval: retentionSweepInterval,
					isActive: sm.isActive,
					logger:   logger,
				}
				go sweeper.Run(ctx)
			}

			dl := newDataLane(sandboxID, wsBase, jwt, sm, &rawConnState, logger)

			// A deleted sandbox ends the data lane for good; there is nothing
			// left to attach to, so unwind the CLI down the same path a Ctrl-C
			// takes. stop() is idempotent, so returning for any other reason
			// (ctx already cancelled) is harmless.
			go func() {
				dl.Run(ctx)
				stop()
			}()

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
	cmd.Flags().BoolVar(&noAutoRespond, "no-auto-respond", false, "Disable auto-accepting known agent startup prompts (e.g. folder-trust)")
	cmd.Flags().StringVar(&retention, "retention", "30d", `Delete session folders older than this (e.g. 30d, 12h); "off" disables`)
	cmd.Flags().StringVar(&sessionBuffer, "session-buffer", "10MB", "Per-session output buffered across a session-lane drop (drop-oldest), flushed on reconnect. 0 disables. Accepts 512KB, 10MB, ...")
	logFlags.register(cmd)
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

// versionLabel renders the bracketed version tag for the header.
// "dev" (unset ldflags) stays as-is; real versions get a "v" prefix.
func versionLabel(v string) string {
	if v == "dev" || v == "" {
		return "[dev]"
	}
	return "[v" + v + "]"
}

// makeTitleFunc returns the static left-side header: logo + version + sandbox name + dim full ID.
func makeTitleFunc(name, id string) func() string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#c084fc")).Render("🔀 retask")
	ver := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(versionLabel(version.Version))
	dimID := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(id)
	label := name + "  " + dimID
	return func() string { return logo + " " + ver + "  " + label }
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
