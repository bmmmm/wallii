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

// cmdAudit asks the question the landed-% never can: did the ok HOLD? A
// later fix on the same ground within a week is the wall indicting its own
// grade — mechanically, from the record, no judgment call anywhere.
func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	sinceS := fs.String("since", "14d", "window: 2006-01-02, 36h or 3d")
	repoF := fs.String("repo", "", "filter: repo name")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)

	since, err := parseSince(*sinceS, time.Now())
	if err != nil {
		return err
	}
	flt := filter{repo: *repoF, since: since}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	evs, rstats, err := wall.ReadLast(dir, 0, flt.match)
	if err != nil {
		return err
	}
	reportStats(rstats)
	haunted := wall.Hauntings(evs)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(haunted)
	}

	sum := wall.Summarize(evs, haunted)
	if len(haunted) == 0 {
		fmt.Printf("no haunted oks in the last %s (%d ok posts) — either they held, or nothing was posted honestly enough to check\n", *sinceS, sum.OKs)
		if sum.NamedHeld > 0 {
			fmt.Println(namedHeldLine(sum))
		}
		return nil
	}
	for _, h := range haunted {
		line := fmt.Sprintf("haunted %s  %s  ✓ %s  ·%s", h.OK.ID(), h.OK.TS.Local().Format("01-02 15:04"), h.OK.Msg, orDash(h.OK.Actor))
		// the one case where the wall can show the shortcut instead of
		// suspecting it: the line the diff carried, then the fix that came
		// back through it
		if h.Measured {
			line += " · measured shortcut"
		}
		fmt.Println(line)
		for _, sig := range h.OK.Signals {
			fmt.Printf("    signal %s\n", sig)
		}
		fmt.Printf("    fix %s  %s  %s — shared: %s\n", h.Fix.ID(), h.Fix.TS.Local().Format("01-02 15:04"), h.Fix.Msg, strings.Join(h.Shared, ", "))
	}
	fmt.Printf("%d of %d ok posts drew a fix on the same ground within 7d — ok must hold, not just land.\n", len(haunted), sum.OKs)
	if sum.Measured > 0 {
		fmt.Printf("%d of them carried a measured shortcut — the skipped check was the gap it came back through.\n", sum.Measured)
	} else {
		fmt.Println("none of them carried a measured shortcut.")
	}
	fmt.Println(namedHeldLine(sum))
	fmt.Println("challenge one: wallii challenge <id> \"held how, exactly?\"")
	return nil
}

// namedHeldLine is the audit's other direction: the oks that named their
// cheap path and drew no fix. Printed as a count over the window, never per
// actor — the point is that the field is cheap to fill, not who fills it.
func namedHeldLine(sum wall.AuditSummary) string {
	return fmt.Sprintf("%s named a grader moment and drew no fix — naming the cheap path costs nothing; leaving it out costs later.",
		plural(sum.NamedHeld, "ok post"))
}
