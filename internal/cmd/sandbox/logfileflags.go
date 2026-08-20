// internal/cmd/sandbox/logfileflags.go
package sandbox

import (
	"fmt"
	"os"
	"strconv"

	agentfleet "github.com/hoaitan/agentfleet"
	"github.com/spf13/cobra"
)

// Defaults for the on-disk connect log. The file lives in the folder the
// command was started from — for a sandbox that is the session folder — so the
// log sits next to the work it describes.
const (
	defaultLogFileName = "retask.log"
	defaultLogMaxSize  = "10MB"
	defaultLogBackups  = 5
)

// logFileFlags holds the connect flags controlling the on-disk log file and
// whether the TUI shows its path.
type logFileFlags struct {
	path     string
	disabled bool
	maxSize  string
	backups  int
	hidePath bool
}

// register binds the log flags to cmd.
func (f *logFileFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.path, "log-file", defaultLogFileName,
		"Log file written alongside the TUI/stderr output, relative to the current folder")
	cmd.Flags().BoolVar(&f.disabled, "no-log-file", false,
		"Do not write a log file; log to the TUI/stderr only")
	cmd.Flags().StringVar(&f.maxSize, "log-max-size", defaultLogMaxSize,
		"Rotate the log file once it exceeds this size. 0 disables rotation. Accepts 512KB, 10MB, ...")
	cmd.Flags().IntVar(&f.backups, "log-backups", defaultLogBackups,
		"Rotated log generations kept (retask.log.1 ... retask.log.N). 0 keeps none")
	cmd.Flags().BoolVar(&f.hidePath, "no-log-path", false,
		"Hide the log file path from the TUI Logs divider")
}

// resolve applies the environment fallbacks and returns the agentfleet log file
// config plus whether the TUI should show the path. A flag always wins over its
// environment variable.
func (f *logFileFlags) resolve(cmd *cobra.Command) (cfg agentfleet.LogFileConfig, showPath bool, err error) {
	path := f.path
	if v, ok := os.LookupEnv("RETASK_SANDBOX_LOG_FILE"); ok && !cmd.Flags().Changed("log-file") {
		path = v
	}
	disabled := f.disabled
	if os.Getenv("RETASK_SANDBOX_NO_LOG_FILE") == "1" && !cmd.Flags().Changed("no-log-file") {
		disabled = true
	}

	maxSize := f.maxSize
	if v := os.Getenv("RETASK_SANDBOX_LOG_MAX_SIZE"); v != "" && !cmd.Flags().Changed("log-max-size") {
		maxSize = v
	}
	maxBytes, err := parseByteSize(maxSize)
	if err != nil {
		return cfg, false, fmt.Errorf("--log-max-size: %w", err)
	}

	backups := f.backups
	if v := os.Getenv("RETASK_SANDBOX_LOG_BACKUPS"); v != "" && !cmd.Flags().Changed("log-backups") {
		backups, err = strconv.Atoi(v)
		if err != nil {
			return cfg, false, fmt.Errorf("RETASK_SANDBOX_LOG_BACKUPS: invalid count %q", v)
		}
	}
	if backups < 0 {
		return cfg, false, fmt.Errorf("--log-backups: must not be negative")
	}

	showPath = !f.hidePath
	if os.Getenv("RETASK_SANDBOX_NO_LOG_PATH") == "1" && !cmd.Flags().Changed("no-log-path") {
		showPath = false
	}

	// An empty path is the same intent as --no-log-file, so both spellings work.
	cfg = agentfleet.LogFileConfig{
		Enabled:  !disabled && path != "",
		Path:     path,
		MaxBytes: int64(maxBytes),
		Backups:  backups,
	}
	return cfg, showPath, nil
}
