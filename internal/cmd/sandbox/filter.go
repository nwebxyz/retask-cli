package sandbox

import (
	"strings"

	tuipkg "github.com/hoaitan/agentfleet/tui"
)

// filterLines is the FilterLines callback registered with agentfleet TUI.
// It trims the agent shell's input UI block from the tail of PTY output,
// removes chrome lines, then joins fragmented streaming lines into coherent text.
func filterLines(lines []string) []string {
	lines = trimInputFooter(lines)
	lines = tuipkg.FilterAgentChrome(lines)
	return joinFragmentedLines(lines)
}

// joinFragmentedLines merges consecutive non-blank, non-structural lines that
// arrived as multiple short fragments due to token-by-token streaming. A line
// is a "fragment candidate" if it doesn't start with a list/code marker and
// isn't blank. Adjacent fragment candidates are joined with a single space.
// Blank lines and structural lines (bullets, numbered lists, code fences) are
// always emitted as-is and break the current join group.
func joinFragmentedLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	acc := ""

	flush := func() {
		if acc != "" {
			out = append(out, acc)
			acc = ""
		}
	}

	for _, l := range lines {
		stripped := strings.TrimSpace(tuipkg.StripANSI(l))

		// Blank lines end the current group and pass through.
		if stripped == "" {
			flush()
			out = append(out, l)
			continue
		}

		// Structural lines (bullets, numbered lists, code fences, headings)
		// end the current group and pass through unchanged.
		if isStructuralLine(stripped) {
			flush()
			out = append(out, l)
			continue
		}

		// Ordinary text: append to accumulator.
		if acc == "" {
			acc = strings.TrimSpace(l)
		} else {
			acc += " " + strings.TrimSpace(l)
		}
	}
	flush()
	return out
}

func isStructuralLine(s string) bool {
	if len(s) == 0 {
		return false
	}
	// Markdown headings
	if s[0] == '#' {
		return true
	}
	// Code fences
	if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
		return true
	}
	// Unordered list bullets (-, *, +)
	if len(s) >= 2 && (s[0] == '-' || s[0] == '*' || s[0] == '+') && s[1] == ' ' {
		return true
	}
	// Numbered list: "1. " pattern
	for i, c := range s {
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '.' && i > 0 && i+1 < len(s) && s[i+1] == ' ' {
			return true
		}
		break
	}
	return false
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
