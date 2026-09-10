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
