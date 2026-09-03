// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// cmdTriggers reads the Stop hook's protocol: one line per Stop, whatever the
// hook decided. It asks the question a firing counter cannot — how many Stops
// ever reached a trigger — and it asks it of the hook's own record rather
// than of the wall. The Stops deliberately never become events: Validate
// rejects an empty Repo and an empty Msg for good reasons, a loop-breaker
// Stop has neither, and roughly 500 lines a day against 534 posts in total
// would poison the very denominator the wall is measured by.
func cmdTriggers(args []string) error {
	fs := flag.NewFlagSet("triggers", flag.ExitOnError)
	sinceS := fs.String("since", "", "window: 2006-01-02, 36h or 3d (default: everything)")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)

	since, err := parseSince(*sinceS, time.Now())
	if err != nil {
		return err
	}
	dir, err := wall.StopLogDir()
	if err != nil {
		return err
	}
	stops, read, err := wall.ReadStops(dir, since)
	if err != nil {
		return err
	}
	t := wall.FoldTriggers(stops, read)

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(t)
	}
	// No file is not zero firings, and saying "0" here would be the same
	// confusion the protocol was built against — one level up, in the reader.
	if read.Files == 0 {
		fmt.Printf("no protocol — the Stop hook writes one line per Stop to %s/stops-YYYY-MM.log,\n", dir)
		fmt.Println("and nothing is there yet: which trigger ran is unknown, not zero. A hook that is not")
		fmt.Println("installed writes no line, and neither does one older than the protocol — the first")
		fmt.Println("record lands at the next Stop either way (README: Claude Code hook).")
		return nil
	}
	if t.Stops == 0 {
		fmt.Printf("no stops in this window — %s hold nothing that recent\n", plural(read.Files, "protocol file"))
		return nil
	}

	fmt.Printf("%-8s %d of %s reached the trigger block (%d%%) — the rest exited above it\n",
		"reached", t.Reached, plural(t.Stops, "stop"), pct(t.Reached, t.Stops))
	fmt.Printf("%-8s %s → %s · %s\n", "window",
		t.First.Local().Format("2006-01-02 15:04"), t.Last.Local().Format("2006-01-02 15:04"),
		plural(read.Files, "protocol file"))
	for _, row := range []struct {
		label  string
		counts []wall.NameCount
	}{
		{"exit", t.Exit}, {"sig", t.Sig}, {"idle", t.Idle}, {"commit", t.Commit},
	} {
		fmt.Printf("%-8s %s\n", row.label, countLine(row.counts))
	}
	// The honest gap, printed rather than filed away in a comment: the record
	// is written from an EXIT trap, so a hook the 10s budget killed leaves
	// nothing. Every number above counts Stops the hook finished.
	fmt.Println("note     a Stop killed by the hook's 10s budget leaves no line — this counts what the hook finished")
	if read.Bad > 0 {
		fmt.Printf("skipped  %s in the protocol could not be read as a record\n", plural(read.Bad, "line"))
	}
	if len(read.Skipped) > 0 {
		fmt.Printf("skipped  unreadable: %s\n", strings.Join(read.Skipped, ", "))
	}
	return nil
}

// countLine renders one trigger's states, most frequent first. Every word the
// hook wrote appears under its own name, including one this build has never
// heard of: folding an unknown state into a known bucket would let a hook
// that learned a new word be read as a trigger whose condition was false.
func countLine(counts []wall.NameCount) string {
	if len(counts) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", c.Name, c.Count))
	}
	return strings.Join(parts, " · ")
}
