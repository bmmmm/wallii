// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"strings"
	"testing"
	"time"
)

func costPost(min int, actor, sess string, cost float64, tok int64) Event {
	e := Event{TS: time.Date(2026, 9, 10, 12, min, 0, 0, time.UTC), Repo: "x", Actor: actor, Msg: "u" + sess + string(rune('0'+min))}
	if sess != "" {
		e.Sess, e.CostSrc, e.CostCum, e.TokCum = sess, CostSession, cost, tok
	}
	return e
}

// Two sessions interleaved on the wall read as two separate ledgers; an
// actor switch inside one session is still one ledger; a counter that went
// backwards yields no reading at all.
func TestUnitCostsReadTheDeltaPerSession(t *testing.T) {
	a1 := costPost(1, "claude/main", "aaaaaaaa", 1.00, 1000)
	b1 := costPost(2, "codex/auto", "bbbbbbbb", 5.00, 9000)
	a2 := costPost(3, "claude/main", "aaaaaaaa", 1.50, 1400)
	b2 := costPost(4, "codex/auto", "bbbbbbbb", 5.25, 9100)
	a3 := costPost(5, "claude/ops", "aaaaaaaa", 2.00, 1900) // actor switch, same session
	a4 := costPost(6, "claude/ops", "aaaaaaaa", 0.10, 50)   // cache reset: negative delta
	a5 := costPost(7, "claude/ops", "aaaaaaaa", 0.40, 120)  // reads against the reset
	none := costPost(8, "claude/main", "", 0, 0)            // nobody measured
	reply := costPost(9, "claude/main", "aaaaaaaa", 9, 9)
	reply.Kind, reply.Parent = KindReact, "abcd123"

	// deliberately out of order: the reader sorts
	got := UnitCosts([]Event{b2, a3, a1, b1, a2, none, a4, a5, reply})
	want := map[string]Unit{
		a1.ID(): {1.00, 1000, true},
		b1.ID(): {5.00, 9000, true},
		a2.ID(): {0.50, 400, false},
		b2.ID(): {0.25, 100, false},
		a3.ID(): {0.50, 500, false},
		a5.ID(): {0.30, 70, false},
	}
	if len(got) != len(want) {
		t.Errorf("%d units read, want %d: %+v", len(got), len(want), got)
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok || !nearly(g.CostUSD, w.CostUSD) || g.Tok != w.Tok || g.FromStart != w.FromStart {
			t.Errorf("unit %s = %+v (present %v), want %+v", id, g, ok, w)
		}
	}
	for _, e := range []Event{a4, none, reply} {
		if u, ok := got[e.ID()]; ok {
			t.Errorf("%q read as %+v, want no entry", e.Msg, u)
		}
	}
}

func nearly(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }

// The note names the delta, says what it is a delta to, and stays silent
// when there is nothing measured to say.
func TestUnitNote(t *testing.T) {
	first := costPost(1, "claude/main", "aaaaaaaa", 3.84, 111961)
	if got := UnitNote(nil, first); !strings.Contains(got, "$3.84") || !strings.Contains(got, "112.0k tok since session start") {
		t.Errorf("first post: %q", got)
	}
	second := costPost(2, "claude/main", "aaaaaaaa", 4.15, 121800)
	got := UnitNote([]Event{first}, second)
	if !strings.Contains(got, "$0.31") || !strings.Contains(got, "9.8k tok since your last post in this session") || !strings.Contains(got, "cost_cum 4.15") {
		t.Errorf("second post: %q", got)
	}
	if got := UnitNote([]Event{first}, costPost(3, "claude/main", "", 0, 0)); got != "" {
		t.Errorf("unmeasured post got a note: %q", got)
	}
	if got := UnitNote([]Event{second}, costPost(4, "claude/main", "aaaaaaaa", 0.05, 10)); got != "" {
		t.Errorf("negative delta got a note: %q", got)
	}
}

func TestFmtTokAndUSD(t *testing.T) {
	for n, want := range map[int64]string{812: "812", 9800: "9.8k", 1_300_000: "1.3M"} {
		if got := FmtTok(n); got != want {
			t.Errorf("FmtTok(%d) = %q, want %q", n, got, want)
		}
	}
	if got := FmtTok(999_999); got != "1.0M" {
		t.Errorf("FmtTok(999999) = %q, want 1.0M", got)
	}
	for v, want := range map[float64]string{0.31: "$0.31", 41.2: "$41.20", 250: "$250", 0.004: "<$0.01", 0: "$0.00", 99.996: "$100"} {
		if got := FmtUSD(v); got != want {
			t.Errorf("FmtUSD(%g) = %q, want %q", v, got, want)
		}
	}
}

// Stats fold the unit readings: sums with their denominator, first-post
// units counted apart, and a wall with no readings folds to nothing.
func TestStatsFoldTheUnitCosts(t *testing.T) {
	evs := []Event{
		costPost(1, "claude/main", "aaaaaaaa", 1.00, 1000),
		costPost(2, "claude/main", "aaaaaaaa", 1.50, 1400),
		costPost(3, "codex/auto", "bbbbbbbb", 2.00, 3000),
		costPost(4, "codex/auto", "bbbbbbbb", 0.10, 10), // reset: no reading
		costPost(5, "claude/main", "", 0, 0),
	}
	s := Compute(evs)
	if s.CostPosts != 3 || !nearly(s.CostTotal, 3.50) || s.TokTotal != 4400 || s.CostFromStart != 2 {
		t.Errorf("stats = posts %d total %g tok %d fromStart %d, want 3 3.50 4400 2",
			s.CostPosts, s.CostTotal, s.TokTotal, s.CostFromStart)
	}
	if q := Compute([]Event{costPost(1, "claude/main", "", 0, 0)}); q.CostPosts != 0 || q.CostTotal != 0 {
		t.Errorf("a wall without readings folded to %+v", q)
	}
}

// A haunted pair carries what each side cost when both were measured, and
// the summary adds the haunted oks and their fixes up against the window.
func TestHauntingsCarryTheUnitCost(t *testing.T) {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	mk := func(h int, topic, msg, sess string, cost float64, tok int64, ok bool) Event {
		e := Event{TS: ts.Add(time.Duration(h) * time.Hour), Repo: "webshop", Actor: "bot", Topic: topic, Msg: msg}
		if ok {
			e.Outcome = OutcomeOK
		}
		if sess != "" {
			e.Sess, e.CostSrc, e.CostCum, e.TokCum = sess, CostSession, cost, tok
		}
		return e
	}
	evs := []Event{
		mk(0, "docs", "readme lists every flag", "aaaaaaaa", 0.50, 500, true),
		mk(1, "feature", "cart totals stable across discount rounds", "aaaaaaaa", 0.81, 900, true),       // unit 0.31
		mk(2, "fix", "cart totals drifted on discount rounds once more", "aaaaaaaa", 1.25, 1400, false),  // unit 0.44
		mk(3, "feature", "invoice numbering strictly monotonic under retries", "", 0, 0, true),           // unmeasured
		mk(4, "fix", "invoice numbering skipped under retries after all", "aaaaaaaa", 1.30, 1450, false), // unit 0.05
	}
	haunted := Hauntings(evs)
	if len(haunted) != 2 {
		t.Fatalf("fixture must haunt exactly two oks, got %+v", haunted)
	}
	if c := haunted[0].Cost; c == nil || !nearly(c.OK.CostUSD, 0.31) || !nearly(c.Fix.CostUSD, 0.44) {
		t.Errorf("measured pair cost = %+v, want ok 0.31 fix 0.44", c)
	}
	if c := haunted[1].Cost; c != nil {
		t.Errorf("pair with an unmeasured ok carries %+v, want none", c)
	}
	s := Summarize(evs, haunted, ts.Add(30*24*time.Hour))
	// the haunted oks: 0.31 measured (the other ok is not); the fixes: both
	// measured, 0.44 + 0.05; the window: 0.50 + 0.31 + 0.44 + 0.05
	if s.CostHauntedN != 1 || s.CostFixesN != 2 {
		t.Errorf("summary counts = haunted %d fixes %d, want 1 2", s.CostHauntedN, s.CostFixesN)
	}
	if !nearly(s.CostHaunted, 0.31) || !nearly(s.CostFixes, 0.49) || !nearly(s.CostWindow, 1.30) {
		t.Errorf("summary cost = haunted %g fixes %g window %g, want 0.31 0.49 1.30", s.CostHaunted, s.CostFixes, s.CostWindow)
	}
}

// A window cut through a session must not price its first post inside the
// window as the whole session: the audit reads its units off the whole
// wall, and the window only decides which posts are summed.
func TestAuditUnitsSurviveTheWindowEdge(t *testing.T) {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	mk := func(h int, topic, msg string, cost float64, ok bool) Event {
		e := Event{TS: ts.Add(time.Duration(h) * time.Hour), Repo: "shop", Actor: "bot", Topic: topic, Msg: msg,
			Sess: "aaaaaaaa", CostSrc: CostSession, CostCum: cost, TokCum: int64(cost * 1000)}
		if ok {
			e.Outcome = OutcomeOK
		}
		return e
	}
	all := []Event{
		mk(0, "docs", "readme lists every flag", 4.24, true),
		mk(1, "feature", "cart totals stable across discount rounds", 5.44, true),     // unit 1.20
		mk(2, "fix", "cart totals drifted on discount rounds once more", 5.50, false), // unit 0.06
	}
	window := all[1:] // --since cuts after the first post
	units := UnitCosts(all)
	haunted := HauntingsWith(window, units)
	if len(haunted) != 1 || haunted[0].Cost == nil || !nearly(haunted[0].Cost.OK.CostUSD, 1.20) {
		t.Fatalf("haunted = %+v, want one pair priced at 1.20", haunted)
	}
	s := SummarizeWith(window, haunted, ts.Add(30*24*time.Hour), units)
	if !nearly(s.CostHaunted, 1.20) || !nearly(s.CostWindow, 1.26) {
		t.Errorf("summary = haunted %g window %g, want 1.20 1.26 (the post before the edge is not in the window)", s.CostHaunted, s.CostWindow)
	}
	// the cut alone would have said 5.44: the regression this guards
	if cut := Hauntings(window); cut[0].Cost == nil || !nearly(cut[0].Cost.OK.CostUSD, 5.44) {
		t.Errorf("fixture no longer shows the cut reading, got %+v", cut[0].Cost)
	}
}
