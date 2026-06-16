package sandbox

import (
	"strings"

	tuipkg "github.com/hoaitan/agentfleet/tui"
)

// filterLines is the FilterLines callback registered with agentfleet TUI.
// It trims the agent shell's input UI block from the tail of PTY output,
// then removes remaining chrome lines (spinners, dividers, prompts).
func filterLines(lines []string) []string {
	lines = trimInputFooter(lines)
	return tuipkg.FilterAgentChrome(lines)
}

// trimInputFooter detects and removes the agent shell's input UI block from
// the end of PTY output. The block looks like:
//
//	──────────────────────────────────────
//	❯ [input text]
//	──────────────────────────────────────
//	  ⏵⏵ [mode/permission line]
//
// Everything from the dashes line immediately above the ❯ prompt to the end
// of the output is discarded.
func trimInputFooter(lines []string) []string {
	const scanWindow = 15

	start := len(lines) - scanWindow
	if start < 0 {
		start = 0
	}

	// Scan from the end looking for the ❯ input prompt.
	for i := len(lines) - 1; i >= start; i-- {
		s := strings.TrimSpace(tuipkg.StripANSI(lines[i]))
		if s != "❯" && !strings.HasPrefix(s, "❯ ") {
			continue
		}
		// Found ❯. Scan back up to 5 lines for the dashes line above it.
		for j := i - 1; j >= 0 && j >= i-5; j-- {
			sj := strings.TrimSpace(tuipkg.StripANSI(lines[j]))
			if isFullWidthDashes(sj) {
				return lines[:j]
			}
		}
		// No dashes found above ❯; cut at ❯ itself.
		return lines[:i]
	}
	return lines
}

func isFullWidthDashes(s string) bool {
	return len(s) >= 3 &&
		(strings.Trim(s, "─") == "" ||
			strings.Trim(s, "━") == "" ||
			(strings.Trim(s, "-") == "" && len([]rune(s)) >= 10))
}
