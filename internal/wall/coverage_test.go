// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"testing"
	"time"
)

// All fixtures below are invented: repo names, actors and messages exist
// nowhere but in this file.

func covDay(loc *time.Location, y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, loc)
}

func covPost(loc *time.Location, repo string, y int, m time.Month, d, h int) Event {
	return Event{TS: covDay(loc, y, m, d, h), Repo: repo, Actor: "bot/builder", Msg: "shipped a thing"}
}

// A repo with posts and no key in the commit map was never measured. It has
// to leave BOTH sides of the ratio and be named — a repo silently dropped
// leaves the figure standing over a subset nobody can see, which is the one
// mistake this whole reading exists to stop making.
//
// This is also the test that dies the day somebody flattens RepoCommits back
// into a map[string]int: Go's zero for the missing key would be "measured,
// no commits", and nothing here could tell the difference.
func TestCoverageUnmeasuredRepoLeavesBothSides(t *testing.T) {
	loc := time.UTC
	from, to := covDay(loc, 2026, 4, 1, 0), covDay(loc, 2026, 4, 5, 0)
	evs := []Event{
		covPost(loc, "webshop", 2026, 4, 2, 9),
		covPost(loc, "webshop", 2026, 4, 2, 11),
		covPost(loc, "ledger", 2026, 4, 2, 15), // ledger is on the wall and nowhere else
	}
	commits := map[string]RepoCommits{
		"webshop": {ByDay: map[string]int{"2026-04-02": 4}, Dir: "/x/webshop", Src: "roots"},
	}
	c := Coverage(evs, commits, loc, from, to, from, DefaultBlindCommits, DefaultBlindPosts)

	if c.OnWall != 2 || c.Measured != 1 {
		t.Fatalf("on wall %d, measured %d — want 2 and 1", c.OnWall, c.Measured)
	}
	if c.Measured >= c.OnWall {
		t.Fatalf("a repo nobody measured must leave Measured below OnWall, got %d/%d", c.Measured, c.OnWall)
	}
	if c.Posts != 2 || c.Commits != 4 {
		t.Fatalf("ledger's post must leave the numerator: posts %d, commits %d — want 2 and 4", c.Posts, c.Commits)
	}
	if c.PostsUnmeasured != 1 {
		t.Fatalf("the dropped post must be counted apart, got %d", c.PostsUnmeasured)
	}
	if len(c.Unresolved) != 1 || c.Unresolved[0].Name != "ledger" {
		t.Fatalf("the unmeasured repo must be named, got %+v", c.Unresolved)
	}
}

// The twin: measured, and it had nothing. An empty ByDay is a finding, and
// it must not read like the repo above.
func TestCoverageEmptyByDayIsMeasured(t *testing.T) {
	loc := time.UTC
	from, to := covDay(loc, 2026, 4, 1, 0), covDay(loc, 2026, 4, 5, 0)
	evs := []Event{covPost(loc, "ledger", 2026, 4, 2, 15)}
	commits := map[string]RepoCommits{"ledger": {ByDay: map[string]int{}, Dir: "/x/ledger", Src: "roots"}}

	c := Coverage(evs, commits, loc, from, to, from, DefaultBlindCommits, DefaultBlindPosts)
	if c.Measured != 1 || len(c.Unresolved) != 0 {
		t.Fatalf("an empty ByDay is a measurement: measured %d, unresolved %+v", c.Measured, c.Unresolved)
	}
	if c.Posts != 1 || c.Commits != 0 || c.PostsUnmeasured != 0 {
		t.Fatalf("posts %d commits %d dropped %d — want 1, 0, 0", c.Posts, c.Commits, c.PostsUnmeasured)
	}

	// and a repo that timed out is the other one again: counts nobody took,
	// never a repo where nothing happened
	timedOut := map[string]RepoCommits{"ledger": {Err: "timed out"}}
	c = Coverage(evs, timedOut, loc, from, to, from, DefaultBlindCommits, DefaultBlindPosts)
	if c.Measured != 0 || len(c.Unresolved) != 1 || c.Unresolved[0].Why != "timed out" {
		t.Fatalf("a timeout must read as unmeasured with its reason, got measured %d, %+v", c.Measured, c.Unresolved)
	}
}

// The thresholds decide, and they are arbitrary — which is why they travel
// as parameters and appear in the output.
func TestCoverageBlindThresholds(t *testing.T) {
	loc := time.UTC
	from, to := covDay(loc, 2026, 4, 1, 0), covDay(loc, 2026, 4, 4, 0)
	// 2026-04-01: 10 commits, 2 posts → blind
	// 2026-04-02: 10 commits, 3 posts → worked, not blind
	// 2026-04-03:  9 commits, 0 posts → below the work threshold, not judged
	evs := []Event{
		covPost(loc, "webshop", 2026, 4, 1, 9), covPost(loc, "webshop", 2026, 4, 1, 10),
		covPost(loc, "webshop", 2026, 4, 2, 9), covPost(loc, "webshop", 2026, 4, 2, 10), covPost(loc, "webshop", 2026, 4, 2, 11),
	}
	commits := map[string]RepoCommits{"webshop": {ByDay: map[string]int{
		"2026-04-01": 10, "2026-04-02": 10, "2026-04-03": 9,
	}}}
	c := Coverage(evs, commits, loc, from, to, from, 10, 2)
	if c.WorkDays != 2 || c.BlindDays != 1 {
		t.Fatalf("work days %d, blind days %d — want 2 and 1", c.WorkDays, c.BlindDays)
	}
	if len(c.Days) != 3 || !c.Days[0].Blind || c.Days[1].Blind || c.Days[2].Blind {
		t.Fatalf("blind flags wrong: %+v", c.Days)
	}
	// the flags move the verdict, or they are decoration
	if loose := Coverage(evs, commits, loc, from, to, from, 10, 1); loose.BlindDays != 0 {
		t.Fatalf("--blind-posts 1 must clear the 2-post day, got %d blind", loose.BlindDays)
	}
	if tight := Coverage(evs, commits, loc, from, to, from, 9, 2); tight.WorkDays != 3 || tight.BlindDays != 2 {
		t.Fatalf("--blind-commits 9 must pull the 9-commit day in, got %d worked / %d blind", tight.WorkDays, tight.BlindDays)
	}
}

// The day boundary is the whole measurement, so the timezone is a parameter
// and never time.Local: t.Setenv("TZ") does not move time.Local after
// process start, and a fold that read the machine's zone could only ever
// prove the machine's.
func TestCoverageDayBoundaryFollowsTheLocation(t *testing.T) {
	utc := time.UTC
	tokyo := time.FixedZone("test+9", 9*3600)
	// 23:30 UTC on the 10th is 08:30 on the 11th nine hours east
	e := Event{TS: time.Date(2026, 4, 10, 23, 30, 0, 0, time.UTC), Repo: "webshop", Actor: "bot/builder", Msg: "late one"}
	commits := map[string]RepoCommits{"webshop": {ByDay: map[string]int{"2026-04-11": 12}}}

	from, to := time.Date(2026, 4, 1, 0, 0, 0, 0, utc), time.Date(2026, 4, 20, 0, 0, 0, 0, utc)
	west := Coverage([]Event{e}, commits, utc, from, to, from, 10, 0)
	if west.BlindDays != 1 {
		t.Fatalf("in UTC the post lands on the 10th and the 11th is blind, got %d blind", west.BlindDays)
	}
	from, to = time.Date(2026, 4, 1, 0, 0, 0, 0, tokyo), time.Date(2026, 4, 20, 0, 0, 0, 0, tokyo)
	east := Coverage([]Event{e}, commits, tokyo, from, to, from, 10, 0)
	if east.BlindDays != 0 {
		t.Fatalf("nine hours east the post lands on the 11th and covers it, got %d blind", east.BlindDays)
	}
}

// A window that reaches past the wall's first post must not report the days
// before it as blind. Measured against the live wall, five such days sat
// inside a plain 30-day window and produced five sixths of its blind days —
// out of a wall that did not exist yet.
func TestCoverageDaysBeforeTheWallAreJudgedByNothing(t *testing.T) {
	loc := time.UTC
	from, to := covDay(loc, 2026, 4, 1, 0), covDay(loc, 2026, 4, 5, 0)
	wallStart := covDay(loc, 2026, 4, 3, 8)
	evs := []Event{covPost(loc, "webshop", 2026, 4, 3, 8)}
	commits := map[string]RepoCommits{"webshop": {ByDay: map[string]int{
		"2026-04-01": 40, "2026-04-02": 40, "2026-04-03": 12, "2026-04-04": 40,
	}}}
	c := Coverage(evs, commits, loc, from, to, wallStart, 10, 0)

	if c.PreWallDays != 2 || c.PreWallCommits != 80 {
		t.Fatalf("pre-wall days %d carrying %d commits — want 2 and 80", c.PreWallDays, c.PreWallCommits)
	}
	if c.WorkDays != 2 || c.BlindDays != 1 {
		t.Fatalf("only the days the wall existed for are judged: %d worked, %d blind — want 2 and 1", c.WorkDays, c.BlindDays)
	}
	if c.Commits != 52 {
		t.Fatalf("commits from before the wall must leave the denominator, got %d — want 52", c.Commits)
	}
	if !c.Days[0].PreWall || c.Days[0].Commits != 40 {
		t.Fatalf("a pre-wall day is still shown with its real count: %+v", c.Days[0])
	}
	if c.Days[0].Blind {
		t.Fatal("a wall that did not exist yet cannot have missed a day")
	}
}

// The foreign authors are carried per day, so a window — and a --split
// half — can hold only its own. A plain total reported the whole month's
// bot commits inside each half of the split, twice.
func TestCoverageOthersAreWindowedLikeCommits(t *testing.T) {
	loc := time.UTC
	commits := map[string]RepoCommits{"webshop": {
		ByDay:       map[string]int{"2026-04-01": 3, "2026-04-09": 3},
		OthersByDay: map[string]int{"2026-04-01": 5, "2026-04-09": 7},
	}}
	first := Coverage(nil, commits, loc, covDay(loc, 2026, 4, 1, 0), covDay(loc, 2026, 4, 5, 0), covDay(loc, 2026, 4, 1, 0), 10, 2)
	if first.Others != 5 {
		t.Fatalf("the first half may only carry its own foreign commits, got %d — want 5", first.Others)
	}
	second := Coverage(nil, commits, loc, covDay(loc, 2026, 4, 5, 0), covDay(loc, 2026, 4, 12, 0), covDay(loc, 2026, 4, 1, 0), 10, 2)
	if second.Others != 7 {
		t.Fatalf("the second half likewise, got %d — want 7", second.Others)
	}
}

// The ratio is described, never scored: PerCommit over nothing is a blank,
// not a NaN and not a zero-division panic.
func TestCoveragePerCommitOverNothing(t *testing.T) {
	loc := time.UTC
	from, to := covDay(loc, 2026, 4, 1, 0), covDay(loc, 2026, 4, 3, 0)
	c := Coverage(nil, map[string]RepoCommits{"webshop": {ByDay: map[string]int{}}}, loc, from, to, from, 10, 2)
	if got := c.PerCommit(); got != 0 {
		t.Fatalf("no commits must yield a plain 0, got %v", got)
	}
}
