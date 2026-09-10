// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// mirror is the one line an agent reads about itself at session start:
// what its units cost, how many of its oks were haunted, how often it
// graded rough or stuck, what doubt still waits on it. Every number with
// its denominator, no percentage, no ranking against anybody else — the
// SessionStart recap prints it, and a line in the agent's own context is
// the one place feedback has been shown to act.
type mirror struct {
	Actor      string  `json:"actor"`
	Since      string  `json:"since"`
	Posts      int     `json:"posts"`
	Measured   int     `json:"measured,omitempty"`
	CostTotal  float64 `json:"cost_total,omitempty"`
	OKs        int     `json:"oks,omitempty"`
	Haunted    int     `json:"haunted,omitempty"`
	Graded     int     `json:"graded,omitempty"`
	RoughStuck int     `json:"rough_stuck,omitempty"`
	Open       int     `json:"open_challenges,omitempty"`
}

func cmdMirror(args []string) error {
	fs := flag.NewFlagSet("mirror", flag.ExitOnError)
	actor := fs.String("actor", "", "the actor to mirror (required, exact)")
	sinceS := fs.String("since", "7d", "window: 2006-01-02, 36h or 3d")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)
	if strings.TrimSpace(*actor) == "" {
		return errors.New("mirror needs --actor <name> — it reflects one actor, never the room")
	}
	now := time.Now()
	since, err := parseSince(*sinceS, now)
	if err != nil {
		return err
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	// unfiltered on purpose: a haunted ok needs the other actor's fix, a
	// unit cost needs the session's previous post, a challenge needs its
	// target — every reading here crosses the actor line, only the count
	// stays on this side of it
	all, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		return err
	}
	m := reflect(all, *actor, since, *sinceS)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(m)
	}
	fmt.Println(m.line())
	return nil
}

// reflect reads one actor's window out of the whole wall.
func reflect(all []wall.Event, actor string, since time.Time, window string) mirror {
	m := mirror{Actor: actor, Since: window}
	units := wall.UnitCosts(all)
	mine := func(e wall.Event) bool { return e.Actor == actor && !e.TS.Before(since) }
	for _, e := range all {
		if e.Kind != "" || !mine(e) {
			continue
		}
		m.Posts++
		if u, ok := units[e.ID()]; ok {
			m.Measured++
			m.CostTotal += u.CostUSD
		}
		if e.Outcome == wall.OutcomeOK {
			m.OKs++
		}
		if e.Mood != "" {
			m.Graded++
			if e.Mood == "rough" || e.Mood == "stuck" {
				m.RoughStuck++
			}
		}
	}
	for _, h := range wall.Hauntings(all) {
		if mine(h.OK) {
			m.Haunted++
		}
	}
	for _, c := range wall.OpenChallenges(all) {
		if c.HasTarget && c.Target.Actor == actor {
			m.Open++
		}
	}
	return m
}

// line renders the mirror; a segment whose source is empty is left out,
// never printed as 0 — an actor nobody measured did not work for free.
func (m mirror) line() string {
	parts := []string{"mirror " + m.Actor, m.Since, plural(m.Posts, "post")}
	if m.Measured > 0 {
		parts = append(parts, fmt.Sprintf("%s per unit over %d measured", wall.FmtUSD(m.CostTotal/float64(m.Measured)), m.Measured))
	}
	if m.OKs > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d oks haunted", m.Haunted, m.OKs))
	}
	if m.Graded > 0 {
		parts = append(parts, fmt.Sprintf("rough/stuck %d of %d graded", m.RoughStuck, m.Graded))
	}
	if m.Open > 0 {
		parts = append(parts, plural(m.Open, "open challenge"))
	}
	return strings.Join(parts, " · ")
}
