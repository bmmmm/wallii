// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// Two actors on one wall: a haunted ok whose fix came from the other actor
// is charged to the ok's actor; unit costs read against the session's
// previous post even across the window edge; and a segment nobody measured
// is left out rather than printed as zero.
func TestMirrorReflectsOneActorOutOfTheWholeWall(t *testing.T) {
	ts := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return ts.Add(time.Duration(h) * time.Hour) }
	sess := func(e wall.Event, cost float64) wall.Event {
		e.Sess, e.CostSrc, e.CostCum, e.TokCum = "aaaaaaaa", wall.CostSession, cost, int64(cost*1000)
		return e
	}
	all := []wall.Event{
		// before the window: sets the session's cumulative baseline
		sess(wall.Event{TS: at(0), Repo: "shop", Actor: "claude/main", Topic: "docs", Msg: "readme lists every flag", Mood: "good"}, 2.00),
		// in the window: unit 0.30, not 2.30
		sess(wall.Event{TS: at(24), Repo: "shop", Actor: "claude/main", Topic: "feature", Outcome: wall.OutcomeOK, Mood: "good",
			Msg: "cart totals stable across discount rounds"}, 2.30),
		// the other actor's fix haunts the ok above
		{TS: at(26), Repo: "shop", Actor: "codex/auto", Topic: "fix", Msg: "cart totals drifted on discount rounds once more"},
		// an ok that held, graded rough, unmeasured
		{TS: at(27), Repo: "shop", Actor: "claude/main", Topic: "feature", Outcome: wall.OutcomeOK, Mood: "rough",
			Msg: "checkout survives an expired voucher"},
		// the other actor's own post: not counted here
		{TS: at(28), Repo: "shop", Actor: "codex/auto", Topic: "feature", Outcome: wall.OutcomeOK, Mood: "ok", Msg: "invoice numbering monotonic"},
	}
	// a challenge against the actor's ok, unanswered
	all = append(all, wall.Event{TS: at(29), Repo: "shop", Actor: "codex/auto", Kind: wall.KindChallenge, Parent: all[1].ID(), Msg: "held how, exactly?"})

	m := reflect(all, "claude/main", at(12), "7d")
	if m.Posts != 2 || m.Measured != 1 || m.CostTotal < 0.299 || m.CostTotal > 0.301 || m.OKs != 2 || m.Haunted != 1 || m.Graded != 2 || m.RoughStuck != 1 || m.Open != 1 {
		t.Errorf("mirror = %+v", m)
	}
	line := m.line()
	for _, want := range []string{"mirror claude/main", "7d", "2 posts", "$0.30 per unit over 1 measured", "1 of 2 oks haunted", "rough/stuck 1 of 2 graded", "1 open challenge"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "%") {
		t.Errorf("line %q carries a percentage", line)
	}
	// the other actor: no cost reading, no open challenge — neither printed
	// as 0 — while its one ok is a count with a denominator and stays
	other := reflect(all, "codex/auto", at(12), "7d").line()
	for _, banned := range []string{"per unit", "challenge"} {
		if strings.Contains(other, banned) {
			t.Errorf("unmeasured segment printed: %q", other)
		}
	}
	if !strings.Contains(other, "0 of 1 oks haunted") {
		t.Errorf("codex line %q", other)
	}
}
