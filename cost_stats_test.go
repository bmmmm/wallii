// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// The cost line reports sums with their denominator and a per-unit figure,
// and grades nobody: no percentage, no actor, nothing against the mood.
func TestCostLineReportsTheSpendAndGradesNobody(t *testing.T) {
	got := costLine(wall.Stats{CostPosts: 380, CostTotal: 41.2, TokTotal: 1_300_000, CostFromStart: 12})
	for _, want := range []string{"$41.20", "1.3M tok", "across 380 measured posts", "$0.11 per unit", "12 counted from session start", "never in them"} {
		if !strings.Contains(got, want) {
			t.Errorf("cost line %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "%") {
		t.Errorf("cost line %q turned a reading into a dial", got)
	}
	if quiet := costLine(wall.Stats{Posts: 400}); quiet != "" {
		t.Errorf("a wall with no readings printed %q", quiet)
	}
	if one := costLine(wall.Stats{CostPosts: 1, CostTotal: 0.3, TokTotal: 900}); strings.Contains(one, "from session start") {
		t.Errorf("no first-post units, yet the line says %q", one)
	}
}

// tail --json carries the unit reading beside each post, read over the whole
// wall: the second post of a session shows its delta, the first says it
// counts from session start, and an unmeasured post has no key at all.
func TestTailJSONCarriesTheUnitCost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	ts := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	for i, e := range []wall.Event{
		{Repo: "x", Actor: "a", Msg: "first", Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 1.00, TokCum: 1000},
		{Repo: "x", Actor: "a", Msg: "second", Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 1.25, TokCum: 1400},
		{Repo: "x", Actor: "a", Msg: "unmeasured"},
	} {
		e.TS = ts.Add(time.Duration(i) * time.Hour)
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	// -n 2 cuts the first post out of the listing; its reading must still
	// feed the second post's delta
	out := captureStdout(t, func() error { return cmdTail([]string{"-n", "2", "--json"}) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", out)
	}
	if !strings.Contains(lines[0], `"unit_cost":{"cost_usd":0.25,"from_start":false,"tok":400}`) {
		t.Errorf("second post: %s", lines[0])
	}
	if strings.Contains(lines[1], "unit_cost") {
		t.Errorf("unmeasured post carries a unit_cost: %s", lines[1])
	}
	full := captureStdout(t, func() error { return cmdTail([]string{"-n", "0", "--json"}) })
	if !strings.Contains(full, `"unit_cost":{"cost_usd":1,"from_start":true,"tok":1000}`) {
		t.Errorf("first post lacks the from-start reading: %s", full)
	}
}

// edgeWall writes a session whose baseline post sits OUTSIDE the window the
// commands below ask for: a post ten days back, then two inside the last
// three days. The units are 1.20 and 0.06 — but only if they were read off
// the whole wall. Read off the window instead, the first post inside it has
// no predecessor and gets priced as the whole session (5.44), which is 4.4×
// the truth and still a perfectly plausible number.
func edgeWall(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	now := time.Now()
	for _, e := range []wall.Event{
		{TS: now.AddDate(0, 0, -10), Repo: "webshop", Actor: "bot/builder", Topic: "docs", Outcome: wall.OutcomeOK,
			Msg: "readme lists every flag", Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 4.24, TokCum: 4240},
		{TS: now.Add(-36 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "feature", Outcome: wall.OutcomeOK,
			Msg: "cart totals stable across discount rounds", Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 5.44, TokCum: 5440},
		{TS: now.Add(-35 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "fix",
			Msg: "cart totals drifted on discount rounds once more", Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 5.50, TokCum: 5500},
	} {
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
}

// `wallii stats --since` reads the units off the whole wall and windows
// only which of them it sums. The library half of this is pinned in
// internal/wall; this is the command half, and it was the half nobody
// guarded: turning stats.go's wall.UnitCosts(all) back into UnitCosts(evs)
// left the whole suite green while the reported spend went from $1.26 to
// $5.50 and the per-unit figure from $0.63 to $2.75.
func TestStatsReadsUnitsOffTheWholeWallNotTheWindow(t *testing.T) {
	edgeWall(t)
	out := captureStdout(t, func() error { return cmdStats([]string{"--since", "3d"}) })
	for _, want := range []string{"$1.26", "across 2 measured posts", "$0.63 per unit"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats is missing %q — the units were read off the window, not the wall:\n%s", want, out)
		}
	}
	// the post at the window's edge has a predecessor on the wall, so
	// nothing inside the window counts from a session start
	if strings.Contains(out, "from session start") {
		t.Errorf("a post at the window edge was priced as a whole session:\n%s", out)
	}
}

// The same for `wallii audit --since`: the pair's two readings and both
// sums come off the whole wall. Under UnitCosts(evs) the haunted ok reads
// $5.44 instead of $1.20 — the audit's whole point is what a shortcut cost,
// and that number would be the session's, not the unit's.
func TestAuditReadsUnitsOffTheWholeWallNotTheWindow(t *testing.T) {
	edgeWall(t)
	out := captureStdout(t, func() error { return cmdAudit([]string{"--since", "3d"}) })
	for _, want := range []string{"ok cost $1.20", "fix cost $0.06", "haunted oks cost $1.20", "of $1.26 measured in the window"} {
		if !strings.Contains(out, want) {
			t.Errorf("audit is missing %q — the units were read off the window, not the wall:\n%s", want, out)
		}
	}
	if strings.Contains(out, "$5.44") || strings.Contains(out, "$5.50") {
		t.Errorf("audit priced a post at the window edge as a whole session:\n%s", out)
	}
}
