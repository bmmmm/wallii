// SPDX-License-Identifier: GPL-3.0-or-later

package wall

import (
	"testing"
	"time"
	_ "time/tzdata"
)

// dayBoundaryZones are the zones that break naive date arithmetic, plus two
// controls. Santiago and Asuncion have jumped at 00:00, so local midnight did
// not exist; Havana has fallen back through 00:00, so it existed twice; Beirut
// and Sao_Paulo (before 2019) carry their own transitions.
var dayBoundaryZones = []string{
	"America/Santiago",
	"America/Havana",
	"Asia/Beirut",
	"America/Asuncion",
	"America/Sao_Paulo",
	"Europe/Berlin",
	"UTC",
}

// TestDayStartIsTheDaysFirstInstant walks fifty years in six-hour steps
// through every zone that has ever moved a day boundary, because the zones
// that break this are exactly the ones nobody thinks to pick by hand.
//
// Two properties, together: the result carries the date that was asked for
// (time.Date normalizes a missing midnight backwards, onto the day before),
// and nothing one nanosecond earlier carries it too (when midnight happens
// twice, the first occurrence is the day's start).
func TestDayStartIsTheDaysFirstInstant(t *testing.T) {
	for _, name := range dayBoundaryZones {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("LoadLocation(%q): %v", name, err)
		}
		for at := time.Date(1985, 1, 1, 0, 0, 0, 0, time.UTC); at.Year() < 2035; at = at.Add(6 * time.Hour) {
			y, m, d := at.In(loc).Date()
			start := DayStart(at, loc)
			if sy, sm, sd := start.Date(); sy != y || sm != m || sd != d {
				t.Fatalf("%s: DayStart(%s) = %s — wrong calendar day, want %04d-%02d-%02d",
					name, at.In(loc), start, y, m, d)
			}
			if py, pm, pd := start.Add(-time.Nanosecond).Date(); py == y && pm == m && pd == d {
				t.Fatalf("%s: DayStart(%s) = %s is not the day's first instant — %s is earlier and on the same day",
					name, at.In(loc), start, start.Add(-time.Nanosecond))
			}
		}
	}
}

// TestNextDayAdvancesExactlyOneCalendarDay is the property AddDate(0, 0, 1)
// does not have: the day after a day without midnight may itself be a day
// without midnight.
func TestNextDayAdvancesExactlyOneCalendarDay(t *testing.T) {
	for _, name := range dayBoundaryZones {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("LoadLocation(%q): %v", name, err)
		}
		for at := time.Date(1985, 1, 1, 0, 0, 0, 0, time.UTC); at.Year() < 2035; at = at.Add(6 * time.Hour) {
			start := DayStart(at, loc)
			next := NextDay(at, loc)
			if next != DayStart(next, loc) {
				t.Fatalf("%s: NextDay(%s) = %s is not a day start", name, at.In(loc), next)
			}
			if got := next.Sub(start); got < 22*time.Hour || got > 26*time.Hour {
				t.Fatalf("%s: %s → %s spans %v — a calendar day is 23, 24 or 25 hours",
					name, start, next, got)
			}
			// Exactly one date forward, never two and never zero. The
			// expected date is stepped in UTC: AddDate in loc would
			// normalize a missing midnight backwards and make the test
			// repeat the very bug it is here to catch.
			sy, sm, sd := start.Date()
			wantY, wantM, wantD := time.Date(sy, sm, sd+1, 12, 0, 0, 0, time.UTC).Date()
			if gy, gm, gd := next.Date(); gy != wantY || gm != wantM || gd != wantD {
				t.Fatalf("%s: NextDay(%s) = %s, want the day %04d-%02d-%02d",
					name, start, next, wantY, wantM, wantD)
			}
		}
	}
}

// inWindow decides which day a per-repo commit count belongs to, and it
// takes the day as a string. Parsed in the zone, a date whose local midnight
// does not exist normalizes backwards onto the day before — and the whole
// day is then judged by the previous day's bounds. Found in review: in
// America/Santiago the commits of 2022-09-11 were filed as "before the wall
// existed", counted into nothing and judged by nothing.
func TestInWindowKeepsADayWithoutMidnightInsideItsOwnWindow(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	// the window the day sits squarely inside
	from := DayStart(time.Date(2022, 9, 5, 12, 0, 0, 0, loc), loc)
	to := NextDay(time.Date(2022, 9, 14, 12, 0, 0, 0, loc), loc)
	if !inWindow("2022-09-11", loc, from, to) {
		t.Error("2022-09-11 fell out of a window containing it — its commits are counted into nothing")
	}
	// and the edges still hold: the day before the window opens is out, the
	// day the window closes on is out
	if inWindow("2022-09-04", loc, from, to) {
		t.Error("the day before the window was let in")
	}
	if inWindow("2022-09-15", loc, from, to) {
		t.Error("the day after the window was let in")
	}
	// the first and last day of the window are in
	for _, day := range []string{"2022-09-05", "2022-09-14"} {
		if !inWindow(day, loc, from, to) {
			t.Errorf("%s is the window's own edge and fell out", day)
		}
	}

	// The edge that catches the backwards normalization: a window that OPENS
	// on the missing-midnight day. Shifted onto 09-10, the day's end lands
	// exactly on `from` and `After` says no — the day leaves the window it
	// starts. This is the case `--since 2022-09-11` would hit.
	opens := DayStart(time.Date(2022, 9, 11, 12, 0, 0, 0, loc), loc)
	if !inWindow("2022-09-11", loc, opens, to) {
		t.Error("2022-09-11 fell out of a window that opens on it")
	}
	// and the mirror case at the other end: a window that CLOSES on it must
	// not let it in
	if inWindow("2022-09-11", loc, from, opens) {
		t.Error("2022-09-11 was let into a window that ends where it begins")
	}
}

// Every day of a missing-midnight month belongs to exactly one window half —
// no day may fall out of a --split, and none may land in both.
func TestInWindowSplitsEveryDayExactlyOnce(t *testing.T) {
	for _, name := range []string{"America/Santiago", "America/Havana", "Europe/Berlin"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		from := DayStart(time.Date(2022, 9, 1, 12, 0, 0, 0, loc), loc)
		split := DayStart(time.Date(2022, 9, 15, 12, 0, 0, 0, loc), loc)
		to := NextDay(time.Date(2022, 9, 30, 12, 0, 0, 0, loc), loc)
		for d := from; d.Before(to); d = NextDay(d, loc) {
			day := d.Format("2006-01-02")
			first, second := inWindow(day, loc, from, split), inWindow(day, loc, split, to)
			if first == second {
				t.Errorf("%s: %s is in %v halves, want exactly one", name, day, map[bool]string{true: "both", false: "neither"}[first])
			}
		}
	}
}

// TestDayStartKeepsTheFirstOfTwoMidnights pins the case that must NOT be
// "fixed": when the clocks go back through 00:00, midnight happens twice and
// the earlier one is the day's start. Havana did this on 2021-11-07.
func TestDayStartKeepsTheFirstOfTwoMidnights(t *testing.T) {
	loc, err := time.LoadLocation("America/Havana")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	start := DayStart(time.Date(2021, 11, 7, 12, 0, 0, 0, loc), loc)
	if got, want := start.UTC(), time.Date(2021, 11, 7, 4, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("DayStart = %s (%s UTC), want the first midnight at %s UTC", start, got, want)
	}
}

// TestDayStartJumpsForwardWhereMidnightIsMissing is the concrete counterpart:
// Santiago moved the clocks from 2022-09-11 00:00 to 01:00, so the day starts
// at 01:00 and not at 23:00 the evening before.
func TestDayStartJumpsForwardWhereMidnightIsMissing(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	start := DayStart(time.Date(2022, 9, 11, 12, 0, 0, 0, loc), loc)
	if h, m := start.Hour(), start.Minute(); h != 1 || m != 0 {
		t.Fatalf("DayStart = %s, want 01:00 on 2022-09-11 — midnight does not exist there", start)
	}
	if d := start.Day(); d != 11 {
		t.Fatalf("DayStart = %s fell onto day %d, want 11", start, d)
	}
}
