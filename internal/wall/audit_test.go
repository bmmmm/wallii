// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"testing"
	"time"
)

func TestHauntingsPairsOkWithLaterFix(t *testing.T) {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	ok := Event{TS: ts, Repo: "demo", Actor: "bot", Topic: "feature", Outcome: OutcomeOK,
		Msg: "parser survives empty headers now, gates green"}
	fix := Event{TS: ts.Add(48 * time.Hour), Repo: "demo", Actor: "bot", Topic: "fix", Outcome: OutcomeOK,
		Msg: "parser crashed on empty headers again — off-by-one in the header scan"}

	got := Hauntings([]Event{ok, fix})
	if len(got) != 1 {
		t.Fatalf("want 1 haunting, got %+v", got)
	}
	if got[0].OK.Msg != ok.Msg || got[0].Fix.Msg != fix.Msg {
		t.Fatalf("wrong pair: %+v", got[0])
	}
	if len(got[0].Shared) < 2 {
		t.Fatalf("shared ground must name at least 2 words, got %v", got[0].Shared)
	}
}

func TestHauntingsIgnoresUnrelatedAndDistantFixes(t *testing.T) {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	ok := Event{TS: ts, Repo: "demo", Topic: "feature", Outcome: OutcomeOK,
		Msg: "parser survives empty headers now, gates green"}
	cases := map[string]Event{
		"different ground": {TS: ts.Add(24 * time.Hour), Repo: "demo", Topic: "fix",
			Msg: "login tokens rotated after expiry bug"},
		"different repo": {TS: ts.Add(24 * time.Hour), Repo: "other", Topic: "fix",
			Msg: "parser crashed on empty headers again"},
		"not a fix": {TS: ts.Add(24 * time.Hour), Repo: "demo", Topic: "feature",
			Msg: "parser handles empty headers plus folded ones"},
		"too late": {TS: ts.Add(9 * 24 * time.Hour), Repo: "demo", Topic: "fix",
			Msg: "parser crashed on empty headers again"},
	}
	for name, q := range cases {
		if got := Hauntings([]Event{ok, q}); len(got) != 0 {
			t.Fatalf("%s must not haunt, got %+v", name, got)
		}
	}
	// a partial cannot be haunted — it already admitted the leftover
	partial := ok
	partial.Outcome = OutcomePartial
	late := Event{TS: ts.Add(24 * time.Hour), Repo: "demo", Topic: "fix",
		Msg: "parser crashed on empty headers again"}
	if got := Hauntings([]Event{partial, late}); len(got) != 0 {
		t.Fatalf("partial must not haunt, got %+v", got)
	}
}

// An ok that carried a measured shortcut and then drew a fix is the wall
// proving a shortcut instead of suspecting one. The mark follows the
// signals, not the source alone: a scan that found nothing proves nothing.
func TestHauntingsMarksAMeasuredShortcut(t *testing.T) {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	fix := Event{TS: ts.Add(48 * time.Hour), Repo: "webshop", Topic: "fix",
		Msg: "cart totals drifted again on the second discount — the skipped test was the one that knew"}
	base := Event{TS: ts, Repo: "webshop", Actor: "bot/builder", Topic: "feature", Outcome: OutcomeOK,
		Msg: "cart totals stable across discount rounds, gates green"}

	measured := base
	measured.Signals, measured.SignalSrc = []string{`cart_test.go: t.Skip("flaky under load")`}, SignalHook
	got := Hauntings([]Event{measured, fix})
	if len(got) != 1 || !got[0].Measured {
		t.Fatalf("an ok with a signal that drew a fix must be marked measured, got %+v", got)
	}

	clean := base
	clean.SignalSrc = SignalHook
	if got := Hauntings([]Event{clean, fix}); len(got) != 1 || got[0].Measured {
		t.Fatalf("a clean scan must not read as a measured shortcut, got %+v", got)
	}
	if got := Hauntings([]Event{base, fix}); len(got) != 1 || got[0].Measured {
		t.Fatalf("an unmeasured ok must not be marked, got %+v", got)
	}
}

// The summary counts both directions over the window: haunted oks that
// carried a measured shortcut, and oks that named their cheap path and
// were never fixed again. Neither is per actor.
func TestSummarizeCountsBothDirections(t *testing.T) {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	sig := []string{`cart_test.go: t.Skip("flaky under load")`}
	evs := []Event{
		// named and haunted: the cheap path was taken, and it came back
		{TS: ts, Repo: "webshop", Actor: "bot/builder", Topic: "feature", Outcome: OutcomeOK,
			Msg: "cart totals stable across discount rounds", Signals: sig, SignalSrc: SignalHook,
			Grader: "skipped the flaky cart test instead of fixing the race"},
		{TS: ts.Add(time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "fix",
			Msg: "cart totals drifted on discount rounds once more"},
		// named and held: the field was cheap, and nothing came back
		{TS: ts.Add(2 * time.Hour), Repo: "webshop", Actor: "bot/reviewer", Topic: "feature", Outcome: OutcomeOK,
			Msg: "checkout survives an expired voucher", Grader: "none — the timeout guards a missing sandbox binary"},
		// unmeasured, unnamed, haunted: the ordinary case the audit already had
		{TS: ts.Add(3 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "feature", Outcome: OutcomeOK,
			Msg: "invoice numbering strictly monotonic under retries"},
		{TS: ts.Add(4 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "fix",
			Msg: "invoice numbering skipped under retries after all"},
		// unnamed, held: says nothing either way
		{TS: ts.Add(5 * time.Hour), Repo: "webshop", Actor: "bot/reviewer", Topic: "docs", Outcome: OutcomeOK,
			Msg: "readme lists every flag"},
		// not an ok: not counted anywhere
		{TS: ts.Add(6 * time.Hour), Repo: "webshop", Actor: "bot/reviewer", Topic: "docs", Outcome: OutcomePartial,
			Msg: "changelog half written", Grader: "considered calling it done"},
	}
	haunted := Hauntings(evs)
	if len(haunted) != 2 {
		t.Fatalf("fixture must haunt exactly two oks, got %+v", haunted)
	}
	s := Summarize(evs, haunted)
	if s.OKs != 4 || s.Haunted != 2 || s.Measured != 1 || s.NamedHeld != 1 {
		t.Fatalf("summary = %+v, want 4 oks, 2 haunted, 1 measured, 1 named and held", s)
	}
	if s := Summarize(nil, nil); s != (AuditSummary{}) {
		t.Fatalf("an empty window sums to %+v", s)
	}
}

func TestHauntingsReportsEachOkOnce(t *testing.T) {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	ok := Event{TS: ts, Repo: "demo", Topic: "feature", Outcome: OutcomeOK,
		Msg: "parser survives empty headers now, gates green"}
	fix1 := Event{TS: ts.Add(24 * time.Hour), Repo: "demo", Topic: "fix",
		Msg: "parser chokes on empty headers with BOM"}
	fix2 := Event{TS: ts.Add(48 * time.Hour), Repo: "demo", Topic: "fix",
		Msg: "parser once more: empty headers inside multipart"}
	got := Hauntings([]Event{ok, fix1, fix2})
	if len(got) != 1 || got[0].Fix.Msg != fix1.Msg {
		t.Fatalf("one ok, earliest fix — got %+v", got)
	}
}
