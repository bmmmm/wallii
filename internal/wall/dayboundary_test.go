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
