// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"

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
