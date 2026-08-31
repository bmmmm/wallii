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
