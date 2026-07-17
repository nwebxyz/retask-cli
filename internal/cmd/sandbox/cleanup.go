// internal/cmd/sandbox/cleanup.go
package sandbox

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nwebxyz/retask-cli/internal/flags"
)

func newCleanupCommand(gf *flags.Global) *cobra.Command {
	var olderThan string
	var dryRun bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "cleanup [sandbox-id]",
		Short: "Delete old session folders in the current directory",
		Long: `Delete session folders left behind by stopped or disconnected sessions.

Only folders recorded in a <sandbox-id>.json session log are considered; any
other directory is left alone. With no argument, every session log in the
current directory is swept.

Usage example:
  retask sandbox cleanup
  retask sandbox cleanup --older-than 7d
  retask sandbox cleanup <sandbox-id> --older-than 7d
  retask sandbox cleanup --older-than 0 --yes
  retask sandbox cleanup --dry-run

Flags:
  --older-than string  Delete folders older than this. Values: 30d, 12h, 0 (0 = everything) (default: 30d)
  --dry-run            Print what would be deleted and exit
  --yes                Skip the confirmation prompt for --older-than 0`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			window, err := parseDuration(olderThan)
			if err != nil {
				return err
			}
			baseDir, err := os.Getwd()
			if err != nil {
				return err
			}

			var logs []*sessionLog
			if len(args) == 1 {
				logs = []*sessionLog{newSessionLog(baseDir, args[0])}
			} else if logs, err = discoverSessionLogs(baseDir); err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			// Dry-run first, so both --dry-run and the prompt report real counts.
			planned := map[*sessionLog][]string{}
			total := 0
			for _, l := range logs {
				ids, sweepErr := l.sweep(baseDir, time.Now(), window, nil, true)
				if sweepErr != nil {
					fmt.Fprintf(out, "skipping %s: %v\n", filepath.Base(l.path), sweepErr)
					continue
				}
				if len(ids) > 0 {
					planned[l] = ids
					total += len(ids)
				}
			}

			if total == 0 {
				fmt.Fprintln(out, "Nothing to clean up.")
				return nil
			}

			for _, l := range logs {
				for _, id := range planned[l] {
					fmt.Fprintf(out, "%s  %s\n", l.sandboxID, id)
				}
			}

			if dryRun {
				fmt.Fprintf(out, "\n%d session folder(s) would be deleted (--dry-run).\n", total)
				return nil
			}

			// A separate process cannot know which sessions are live elsewhere,
			// so wiping everything asks first.
			if window == 0 && !yes {
				prompt := fmt.Sprintf("\nThis will delete %d session folder(s) across %d sandbox(es). Continue? [y/N]: ", total, len(planned))
				if !confirm(cmd.InOrStdin(), out, prompt) {
					fmt.Fprintln(out, "Aborted.")
					return nil
				}
			}

			deletedTotal := 0
			for _, l := range logs {
				if len(planned[l]) == 0 {
					continue
				}
				deleted, sweepErr := l.sweep(baseDir, time.Now(), window, nil, false)
				deletedTotal += len(deleted)
				if sweepErr != nil {
					err = errors.Join(err, sweepErr)
				}
			}
			fmt.Fprintf(out, "\nDeleted %d session folder(s).\n", deletedTotal)
			return err
		},
	}

	cmd.Flags().StringVar(&olderThan, "older-than", "30d", "Delete folders older than this (e.g. 30d, 12h); 0 deletes everything")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be deleted and exit")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt for --older-than 0")
	return cmd
}

// discoverSessionLogs returns every valid session log in baseDir. A working
// directory holds ordinary JSON (package.json, tsconfig.json); anything failing
// the schema check is skipped, so cleanup can never act on it.
func discoverSessionLogs(baseDir string) (logs []*sessionLog, err error) {
	matches, err := filepath.Glob(filepath.Join(baseDir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	for _, p := range matches {
		d, loadErr := loadSessionLogFile(p)
		if loadErr != nil {
			if errors.Is(loadErr, errNewerLog) {
				continue // written by a newer CLI — not ours to rewrite
			}
			return nil, loadErr
		}
		if d == nil {
			continue // not a session log
		}
		logs = append(logs, newSessionLog(baseDir, d.SandboxID))
	}
	return logs, nil
}

// confirm reads a y/N answer. Anything other than y/yes is a no.
func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
