package sandbox

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentfleet "github.com/hoaitan/agentfleet"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLogFlagsCmd builds a command with the log flags registered and the given
// argv applied, mirroring how cobra hands them to connect's RunE.
func newLogFlagsCmd(t *testing.T, argv ...string) (*cobra.Command, *logFileFlags) {
	t.Helper()
	f := &logFileFlags{}
	cmd := &cobra.Command{Use: "connect", RunE: func(*cobra.Command, []string) error { return nil }}
	f.register(cmd)
	require.NoError(t, cmd.ParseFlags(argv))
	return cmd, f
}

func TestLogFileFlagsDefaults(t *testing.T) {
	cmd, f := newLogFlagsCmd(t)

	cfg, showPath, err := f.resolve(cmd)
	require.NoError(t, err)

	assert.True(t, cfg.Enabled, "the log file is written by default")
	assert.Equal(t, defaultLogFileName, cfg.Path)
	assert.Equal(t, int64(10*1024*1024), cfg.MaxBytes)
	assert.Equal(t, defaultLogBackups, cfg.Backups)
	assert.True(t, showPath, "the TUI shows the log path by default")
}

func TestLogFileFlagsOverrides(t *testing.T) {
	cmd, f := newLogFlagsCmd(t,
		"--log-file", "/var/log/retask.log",
		"--log-max-size", "512KB",
		"--log-backups", "2",
		"--no-log-path",
	)

	cfg, showPath, err := f.resolve(cmd)
	require.NoError(t, err)

	assert.True(t, cfg.Enabled)
	assert.Equal(t, "/var/log/retask.log", cfg.Path)
	assert.Equal(t, int64(512*1024), cfg.MaxBytes)
	assert.Equal(t, 2, cfg.Backups)
	assert.False(t, showPath)
}

func TestLogFileFlagsNoLogFile(t *testing.T) {
	cmd, f := newLogFlagsCmd(t, "--no-log-file")

	cfg, _, err := f.resolve(cmd)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
}

func TestLogFileFlagsEmptyPathDisables(t *testing.T) {
	cmd, f := newLogFlagsCmd(t, "--log-file", "")

	cfg, _, err := f.resolve(cmd)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled, "an empty path means the same as --no-log-file")
}

func TestLogFileFlagsZeroMaxSizeDisablesRotation(t *testing.T) {
	cmd, f := newLogFlagsCmd(t, "--log-max-size", "0")

	cfg, _, err := f.resolve(cmd)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.MaxBytes)
}

func TestLogFileFlagsEnvFallbacks(t *testing.T) {
	t.Setenv("RETASK_SANDBOX_LOG_FILE", "/env/retask.log")
	t.Setenv("RETASK_SANDBOX_LOG_MAX_SIZE", "1MB")
	t.Setenv("RETASK_SANDBOX_LOG_BACKUPS", "9")
	t.Setenv("RETASK_SANDBOX_NO_LOG_PATH", "1")

	cmd, f := newLogFlagsCmd(t)

	cfg, showPath, err := f.resolve(cmd)
	require.NoError(t, err)

	assert.Equal(t, "/env/retask.log", cfg.Path)
	assert.Equal(t, int64(1024*1024), cfg.MaxBytes)
	assert.Equal(t, 9, cfg.Backups)
	assert.False(t, showPath)
}

func TestLogFileFlagsEnvNoLogFile(t *testing.T) {
	t.Setenv("RETASK_SANDBOX_NO_LOG_FILE", "1")

	cmd, f := newLogFlagsCmd(t)

	cfg, _, err := f.resolve(cmd)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
}

func TestLogFileFlagsFlagBeatsEnv(t *testing.T) {
	t.Setenv("RETASK_SANDBOX_LOG_FILE", "/env/retask.log")
	t.Setenv("RETASK_SANDBOX_LOG_MAX_SIZE", "1MB")
	t.Setenv("RETASK_SANDBOX_LOG_BACKUPS", "9")
	t.Setenv("RETASK_SANDBOX_NO_LOG_PATH", "1")

	cmd, f := newLogFlagsCmd(t,
		"--log-file", "/flag/retask.log",
		"--log-max-size", "2MB",
		"--log-backups", "1",
	)

	cfg, showPath, err := f.resolve(cmd)
	require.NoError(t, err)

	assert.Equal(t, "/flag/retask.log", cfg.Path)
	assert.Equal(t, int64(2*1024*1024), cfg.MaxBytes)
	assert.Equal(t, 1, cfg.Backups)
	assert.False(t, showPath, "--no-log-path was not passed, so the env var still applies")
}

func TestLogFileFlagsEmptyEnvPathDisables(t *testing.T) {
	t.Setenv("RETASK_SANDBOX_LOG_FILE", "")

	cmd, f := newLogFlagsCmd(t)

	cfg, _, err := f.resolve(cmd)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled, "an explicitly empty env path disables the file")
}

func TestLogFileFlagsInvalidValues(t *testing.T) {
	cmd, f := newLogFlagsCmd(t, "--log-max-size", "10XB")
	_, _, err := f.resolve(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--log-max-size")

	cmd, f = newLogFlagsCmd(t, "--log-backups=-1")
	_, _, err = f.resolve(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--log-backups")

	t.Setenv("RETASK_SANDBOX_LOG_BACKUPS", "many")
	cmd, f = newLogFlagsCmd(t)
	_, _, err = f.resolve(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RETASK_SANDBOX_LOG_BACKUPS")
}

// TestConnectLogTeeWritesBothPlaces covers the wiring connect performs: the
// same slog handler feeds the TUI buffer and the rotating file.
func TestConnectLogTeeWritesBothPlaces(t *testing.T) {
	dir := t.TempDir()
	cmd, f := newLogFlagsCmd(t, "--log-file", filepath.Join(dir, "retask.log"))

	cfg, showPath, err := f.resolve(cmd)
	require.NoError(t, err)
	assert.True(t, showPath)

	logFile, err := agentfleet.OpenLogFile(cfg)
	require.NoError(t, err)
	defer logFile.Close() //nolint:errcheck

	logBuf := agentfleet.NewLogBuffer(500)
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(logBuf, logFile), &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("retask sandbox connect", "sandbox_id", "sandbox_abc123")

	lines := logBuf.Lines()
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "sandbox_abc123")

	onDisk, err := os.ReadFile(filepath.Join(dir, "retask.log"))
	require.NoError(t, err)
	assert.Contains(t, string(onDisk), "sandbox_abc123")
	assert.Equal(t, strings.TrimRight(string(onDisk), "\n"), lines[0])
}

// TestRotationProducesNumberedGenerations pins the file names an operator sees
// in the sandbox folder once the log outgrows --log-max-size.
func TestRotationProducesNumberedGenerations(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cmd, f := newLogFlagsCmd(t, "--log-max-size", "1KB", "--log-backups", "2")
	cfg, _, err := f.resolve(cmd)
	require.NoError(t, err)

	logFile, err := agentfleet.OpenLogFile(cfg)
	require.NoError(t, err)
	defer logFile.Close() //nolint:errcheck

	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	for i := 0; i < 200; i++ {
		logger.Info("session output flushed", "session_id", "session_0123456789abcdef", "bytes", i)
	}

	assert.FileExists(t, filepath.Join(dir, "retask.log"))
	assert.FileExists(t, filepath.Join(dir, "retask.log.1"))
	assert.FileExists(t, filepath.Join(dir, "retask.log.2"))
	assert.NoFileExists(t, filepath.Join(dir, "retask.log.3"), "--log-backups caps the generations kept")
}

// TestDisabledLogFileWritesNowhere confirms the nil *LogFile stays a safe tee
// target, so connect needs no branch when the file is turned off.
func TestDisabledLogFileWritesNowhere(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cmd, f := newLogFlagsCmd(t, "--no-log-file")
	cfg, _, err := f.resolve(cmd)
	require.NoError(t, err)

	logFile, err := agentfleet.OpenLogFile(cfg)
	require.NoError(t, err)
	assert.Equal(t, "", logFile.Path())

	logBuf := agentfleet.NewLogBuffer(500)
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(logBuf, logFile), &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("still reaches the panel")

	assert.Len(t, logBuf.Lines(), 1)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no log file is created")
}
