// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// The selected row and the detail view carry the grader in the poster's own
// words — the row as its own line before the refs, the detail as a paragraph
// wrapped to the terminal and never cut at a field's width.
func TestTUIShowsGraderOnSelectedRowAndInDetail(t *testing.T) {
	grader := "considered raising the timeout to make the flake go away, fixed the retry loop instead — which took the whole afternoon"
	evs := []wall.Event{{
		TS: time.Now(), Repo: "webshop", Topic: "fix", Actor: "bot/builder", Msg: "retry loop fixed",
		Grader: grader, Refs: []string{"https://git.example.com/x/webshop/commit/abc"},
	}}
	m := newTUI(t.TempDir(), evs)
	m.width, m.height = 60, 24

	row := m.line(evs[0], true)
	if !strings.Contains(row, "↷") {
		t.Fatalf("selected row must carry the grader line, got:\n%s", row)
	}
	if strings.Index(row, "↷") > strings.Index(row, "↗") {
		t.Errorf("the grader comes before the refs, got:\n%s", row)
	}
	if plain := m.line(evs[0], false); strings.Contains(plain, "↷") {
		t.Errorf("an unselected row stays one line, got:\n%s", plain)
	}

	detail := m.viewDetail()
	if !strings.Contains(detail, "↷") {
		t.Fatalf("detail view must show the grader, got:\n%s", detail)
	}
	// wrapped, not truncated: the last word survives, and no single line
	// holds the whole sentence at this width
	if !strings.Contains(detail, "afternoon") {
		t.Errorf("the grader was cut off in the detail view:\n%s", detail)
	}
	for _, l := range strings.Split(detail, "\n") {
		if strings.Contains(l, grader) {
			t.Errorf("a 110-rune grader on one line at width 60 is not wrapped:\n%s", detail)
		}
	}
}
