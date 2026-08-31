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

	oks := 0
	for _, e := range evs {
		if e.Kind == "" && e.Outcome == wall.OutcomeOK {
			oks++
		}
	}
	if len(haunted) == 0 {
		fmt.Printf("no haunted oks in the last %s (%d ok posts) — either they held, or nothing was posted honestly enough to check\n", *sinceS, oks)
		return nil
	}
	for _, h := range haunted {
		fmt.Printf("haunted %s  %s  ✓ %s  ·%s\n", h.OK.ID(), h.OK.TS.Local().Format("01-02 15:04"), h.OK.Msg, orDash(h.OK.Actor))
		fmt.Printf("    fix %s  %s  %s — shared: %s\n", h.Fix.ID(), h.Fix.TS.Local().Format("01-02 15:04"), h.Fix.Msg, strings.Join(h.Shared, ", "))
	}
	fmt.Printf("%d of %d ok posts drew a fix on the same ground within 7d — ok must hold, not just land.\n", len(haunted), oks)
	fmt.Println("challenge one: wallii challenge <id> \"held how, exactly?\"")
	return nil
}
