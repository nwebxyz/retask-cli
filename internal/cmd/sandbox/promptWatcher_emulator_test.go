package sandbox

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hoaitan/agentfleet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Byte sequences Claude Code 2.1.270 writes for its startup trust dialog at 85
// columns, captured from a real PTY (workspace path shortened). The first frame
// parks the cursor on the focused option's marker cell and ends with terminal
// queries, including the kitty keyboard query CSI ? u. Every later redraw is
// relative to that parked cursor.
var (
	claudeTrustFrame = "\x1b7\x1b[r\x1b8\x1b[?25h\x1b[?25l\x1b[?2004h\x1b[?2031h\x1b[?1004h\x1b[<u\x1b[>5u\x1b[>4;2m\x1b[?2026h\r\n" +
		"\x1b[38;2;255;193;7m" + strings.Repeat("─", 85) + "\x1b[39m\r\n" +
		"\x1b[2G\x1b[38;2;255;193;7m\x1b[1mAccessing\x1b[12Gworkspace:\x1b[22m\x1b[39m\r\n\r\n" +
		"\x1b[2G\x1b[1m/tmp/session\x1b[22m\r\n\r\n" +
		"\x1b[2GQuick safety check: Is this a project you created or one you trust? (Like your own\r\n" +
		"\x1b[2Gcode, a well-known open source project, or work from your team). If not, take a\r\n" +
		"\x1b[2Gmoment to review what's in this folder first.\r\n\r\n" +
		"\x1b[2GClaude Code'll be able to read, edit, and execute files here.\r\n\r\n" +
		"\x1b[2G\x1b[38;2;153;153;153mSecurity guide\x1b[39m\r\n\r\n" +
		"\x1b[2G\x1b[38;2;177;185;249m❯\x1b[4GNo,\x1b[8Gexit\x1b[39m\r\n" +
		"\x1b[4GYes,\x1b[9GI\x1b[11Gtrust\x1b[17Gthis\x1b[22Gfolder\r\n\r\n" +
		"\x1b[2G\x1b[38;2;153;153;153mEnter\x1b[8Gto\x1b[11Gconfirm\x1b[19G·\x1b[21GEsc\x1b[25Gto\x1b[28Gcancel\x1b[39m\r\n" +
		"\x1b[1C\x1b[4A\x1b[?2026l\x1b[>0q\x1b[?u\x1b[c"
	claudeTrustFocusYes = "\x1b[?2026h\x1b[1D\x1b[4B\r\x1b[1C\x1b[4A \x1b[4GNo, exit\r\x1b[1C\x1b[1B" +
		"\x1b[38;2;177;185;249m❯\x1b[4GYes, I trust this folder\x1b[39m\r\n\n\n\x1b[1C\x1b[3A\x1b[?2026l"
	claudeTrustFocusNo = "\x1b[?2026h\x1b[1D\x1b[3B\r\x1b[1C\x1b[4A\x1b[38;2;177;185;249m❯\x1b[4GNo, exit" +
		"\r\x1b[1C\x1b[1B\x1b[39m \x1b[4GYes, I trust this folder\r\n\n\n\x1b[1C\x1b[4A\x1b[?2026l"
)

// fakeClaudeTrustDialog plays Claude Code's side of the dialog on a MockAgent:
// it draws the first frame, answers each arrow key with the captured redraw
// (the menu wraps), and reports the option confirmed with Enter.
func fakeClaudeTrustDialog(t *testing.T, ag *agentfleet.MockAgent) <-chan string {
	confirmed := make(chan string, 1)
	go func() {
		defer close(confirmed)
		if ag.SimulateOutput([]byte(claudeTrustFrame)) != nil {
			return
		}
		yes := false
		for {
			in, err := ag.ReadInput(30 * time.Second)
			if err != nil {
				return
			}
			for s := string(in); s != ""; {
				switch {
				case strings.HasPrefix(s, keyDown), strings.HasPrefix(s, keyUp):
					s = s[len(keyDown):]
					yes = !yes
					redraw := claudeTrustFocusNo
					if yes {
						redraw = claudeTrustFocusYes
					}
					if ag.SimulateOutput([]byte(redraw)) != nil {
						return
					}
				case strings.HasPrefix(s, keyConfirm):
					if yes {
						confirmed <- "Yes, I trust this folder"
					} else {
						confirmed <- "No, exit"
					}
					return
				default:
					t.Logf("fake dialog ignoring input %q", s)
					s = ""
				}
			}
		}
	}()
	return confirmed
}

// Drives the watcher through the same agentfleet emulator a session uses. The
// dialog's CSI ? u query used to move the emulator's cursor to the top-left, so
// the redraw after the first arrow key was painted over the top rows, the
// watcher chased that stale copy of the menu, and the prompt was abandoned.
func TestPromptWatcher_AcceptsClaudeTrustDialogThroughEmulator(t *testing.T) {
	agCfg := agentfleet.AgentConfig{PTYRows: 48, PTYCols: 85}
	ag := agentfleet.NewMockAgent()
	r := agentfleet.NewRunner(&agentfleet.BasicTask{TaskID: "trust", TaskName: "trust", Cmd: "claude"}, ag, agentfleet.FleetConfig{}, agCfg)
	r.Start()
	t.Cleanup(func() { r.Stop() }) //nolint:errcheck
	confirmed := fakeClaudeTrustDialog(t, ag)

	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	w := newPromptWatcher(r.Lines, r.StdinWriter(), ruleNamed("claude-trust"), 5*time.Millisecond, 5*time.Second, log)
	w.Run(context.Background())

	select {
	case got := <-confirmed:
		assert.Equal(t, "Yes, I trust this folder", got)
	case <-time.After(2 * time.Second):
		require.Fail(t, "dialog was never confirmed", "watcher log:\n%s\nscreen:\n%s", logBuf.String(), strings.Join(r.Lines(), "\n"))
	}
	assert.Contains(t, logBuf.String(), "prompt_autoresponded")
}
