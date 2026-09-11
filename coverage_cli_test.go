// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// The binding Goodhart clause, executable. The coverage reading answers "how
// much of the work reached the wall", and the cheap way to satisfy that
// question is to post more and thinner. Nothing about it may appear in the
// output an agent reads after every session: not the number, not the word.
// The moment a commit count shows up in `wallii stats`, posting to move it
// becomes the obvious play, and the wall's account of a day is what gets
// paid with. Put a commits line into cmdStats and this test goes red.
func TestStatsDefaultOutputCarriesNoCommitVocabulary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	t.Setenv("WALLII_PULSE", "off")
	now := time.Now()
	for i, msg := range []string{"retry loop closed", "readme matches the flags again", "flake reproduced at last"} {
		e := wall.Event{TS: now.Add(-time.Duration(i) * time.Hour), Repo: "webshop", Actor: "bot/builder",
			Topic: "fix", Msg: msg, Outcome: wall.OutcomeOK, Mood: "good"}
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	out := strings.ToLower(captureStdout(t, func() error { return cmdStats(nil) }))
	for _, word := range []string{"commit", "blind", "coverage", "per commit", "git log"} {
		if strings.Contains(out, word) {
			t.Errorf("stats must not carry the word %q — it turns the reading into a dial an agent can push:\n%s", word, out)
		}
	}
}

// git may run for coverage and dash, both typed by a person, and nowhere
// else. cmdPost runs on every post and cmdTail inside the Stop hook's
// ten-second budget; a `git log` across every repo on the wall in either
// would be paid for by the hook missing its window.
//
// The shim records what was asked of git and refuses anything but the
// rev-parse that names the current repo.
func TestPostNeverAsksGitForALog(t *testing.T) {
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + argv + "'\n" +
		"case \"$1\" in\n" +
		"  --no-optional-locks|-C) shift 2 ;;\n" +
		"esac\n" +
		"case \"$1\" in\n" +
		"  rev-parse) echo '/fixture/webshop/.git' ;;\n" +
		"  *) echo 'wallii asked git for something a post has no business asking' >&2; exit 3 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("WALLII_DIR", t.TempDir())
	t.Setenv("WALLII_SESSION_START", "")
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv("WALLII_AUTO_CHALLENGE", "off")

	// no -r: the repo name comes from git, so the shim is reached at all
	if err := cmdPost([]string{"-t", "fix", "the retry loop no longer races the fsync"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	b, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("the shim was never called — this test would pass for the wrong reason: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(calls) == 0 || calls[0] == "" {
		t.Fatal("no git call recorded")
	}
	for _, c := range calls {
		for _, f := range strings.Fields(c) {
			if f == "log" || f == "shortlog" || f == "rev-list" {
				t.Errorf("a post asked git to walk history: %q", c)
			}
		}
	}
}

// There is no day-key string any more: the day IS the index, and the whole
// seam is the one invariant below. A string format pinned across two
// languages was what the old bug was made of.
//
// The zone table matters because these are the properties the missing and
// the doubled midnight break: a gap, a duplicate, or a day start that is not
// one.
func TestDashCalendarAndCommitsAreAligned(t *testing.T) {
	// Each zone is walked over a window that contains ITS OWN transition —
	// naming the hard zones is not the same as exercising them. Found in
	// review: the window here used to be October/November for everybody,
	// which misses Santiago's 2022-09-11 midnight jump and Auckland's
	// 2022-09-25 one entirely, and a reverted NextDay stayed green.
	for _, tc := range []struct {
		name string
		end  time.Time // a day shortly after that zone's transition
	}{
		{"Europe/Berlin", time.Date(2022, time.November, 20, 15, 0, 0, 0, time.UTC)},
		{"America/Santiago", time.Date(2022, time.September, 20, 15, 0, 0, 0, time.UTC)}, // midnight jump 09-11
		{"America/Havana", time.Date(2022, time.November, 20, 15, 0, 0, 0, time.UTC)},    // midnight happens twice 11-06
		{"Pacific/Auckland", time.Date(2022, time.October, 5, 15, 0, 0, 0, time.UTC)},    // 09-25
		{"America/Asuncion", time.Date(2022, time.October, 20, 15, 0, 0, 0, time.UTC)},   // midnight jump 10-02
		{"UTC", time.Date(2022, time.November, 20, 15, 0, 0, 0, time.UTC)},
	} {
		name := tc.name
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		now := tc.end.In(loc)
		evs := []wall.Event{
			{TS: now.AddDate(0, 0, -40), Repo: "webshop", Actor: "bot/builder", Msg: "first"},
			{TS: now.Add(-2 * time.Hour), Repo: "webshop", Actor: "bot/builder", Msg: "last"},
		}
		cov := &dashCoverage{
			from: wall.DayStart(now.AddDate(0, 0, -30), loc), to: wall.NextDay(now, loc),
			byDay: map[string]int{now.Format("2006-01-02"): 7},
		}
		cal := buildDashCalendar(evs, cov, now, loc)
		src := *cov // to prove the measurement is not written back into
		cov = indexedCoverage(cov, cal, loc)
		if src.Commits != nil || src.FromI != 0 || src.ToI != 0 {
			t.Fatalf("%s: indexing wrote the projection back into the measurement — under --serve that changes a page already served", name)
		}

		if len(cov.Commits) != len(cal.T0) {
			t.Fatalf("%s: %d commit slots for %d days — the browser indexes one by the other", name, len(cov.Commits), len(cal.T0))
		}
		if cal.Wd0 != (int(time.UnixMilli(cal.T0[0]).In(loc).Weekday())+6)%7 {
			t.Fatalf("%s: wd0 = %d does not match %s", name, cal.Wd0, time.UnixMilli(cal.T0[0]).In(loc))
		}
		seen := map[string]bool{}
		for i, ms := range cal.T0 {
			at := time.UnixMilli(ms).In(loc)
			if at != wall.DayStart(at, loc) {
				t.Fatalf("%s: t0[%d] = %s is not a day start", name, i, at)
			}
			key := at.Format("2006-01-02")
			if seen[key] {
				t.Fatalf("%s: two entries on %s", name, key)
			}
			seen[key] = true
			if i > 0 {
				// 23, 24 or 25 hours: exactly the span a gap or a duplicate
				// day would fall outside of
				if d := at.Sub(time.UnixMilli(cal.T0[i-1]).In(loc)); d < 23*time.Hour || d > 25*time.Hour {
					t.Fatalf("%s: t0[%d] follows its predecessor after %v", name, i, d)
				}
			}
		}
		if cal.End <= cal.T0[len(cal.T0)-1] {
			t.Fatalf("%s: end %d is not after the last day", name, cal.End)
		}
		// The axis reaches End, it does not merely stay below it: a walk
		// that stalls on a day without midnight produces a calendar that is
		// internally consistent and simply stops early — every day after it
		// silently missing from the page.
		lastDay := time.UnixMilli(cal.T0[len(cal.T0)-1]).In(loc)
		if got := wall.NextDay(lastDay, loc).UnixMilli(); got != cal.End {
			t.Fatalf("%s: the calendar's last day is %s, whose day ends at %s — but end is %s: the walk stopped early",
				name, lastDay, time.UnixMilli(got).In(loc), time.UnixMilli(cal.End).In(loc))
		}
		if !(0 <= cov.FromI && cov.FromI <= cov.ToI && cov.ToI <= len(cal.T0)) {
			t.Fatalf("%s: from_i=%d to_i=%d out of bounds for %d days", name, cov.FromI, cov.ToI, len(cal.T0))
		}
		if cal.TZ != name {
			t.Fatalf("%s: calendar names the zone %q", name, cal.TZ)
		}
	}
}

// An empty wall still gets a calendar, and it has exactly one day: the page
// divides by the number of buckets.
func TestDashCalendarIsNeverEmpty(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, time.March, 4, 9, 0, 0, 0, loc)
	cal := buildDashCalendar(nil, nil, now, loc)
	if len(cal.T0) != 1 {
		t.Fatalf("an empty wall got %d days, want exactly 1", len(cal.T0))
	}
	if got := time.UnixMilli(cal.T0[0]).In(loc); got != wall.DayStart(now, loc) {
		t.Fatalf("the single day is %s, want the generation day %s", got, wall.DayStart(now, loc))
	}
}

// The script must not do calendar arithmetic at all: that is the whole point
// of handing it an axis. One Date.now() survives, for the "N days old" note
// in the header, and it decides nothing that is counted.
func TestDashHtmlDoesNoLocalCalendarArithmetic(t *testing.T) {
	i, j := strings.Index(dashTemplate, "<script>"), strings.LastIndex(dashTemplate, "</script>")
	if i < 0 || j < i {
		t.Fatal("dash.html has no script block")
	}
	// Code, not prose: the rule is worth explaining next to the calls that
	// follow it, and explaining it means naming what is forbidden.
	var b strings.Builder
	for _, line := range strings.Split(dashTemplate[i:j], "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	script := b.String()
	for _, banned := range []string{
		"getFullYear(", "getMonth(", "getDate(", "getDay(", "getHours(", "getMinutes(",
		"setHours(", "setDate(", "new Date(", "toLocale",
	} {
		if strings.Contains(script, banned) {
			t.Errorf("dash.html uses %s — the viewer's calendar must not decide what a day is", banned)
		}
	}
	if n := strings.Count(script, "Date.now("); n != 1 {
		t.Errorf("Date.now( appears %d times, want exactly 1 (the staleness note)", n)
	}
}

// The dashboard's own contract, and the plan called it the most dangerous
// bug of the unit: `null` when nobody measured, never `[]` or `{}` — an
// empty container cannot tell "measured, no commits" from "nobody looked",
// and the card that cannot tell them apart paints total blindness out of
// nothing. The placeholder must also be gone: a surviving token would leave
// the browser with a syntax error and a blank page.
func TestDashInlinesNullWhenNothingWasMeasured(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	t.Setenv("WALLII_PULSE", "off")
	// a root with no repository of that name in it: the wall names a repo,
	// nothing on disk answers
	t.Setenv("WALLII_REPO_ROOTS", t.TempDir())
	if err := wall.Append(dir, wall.Event{TS: time.Now(), Repo: "webshop", Actor: "bot/builder", Msg: "shipped a thing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdDash([]string{"-o", filepath.Join(dir, "d.html")}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "d.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if strings.Contains(html, "__WALLII_COMMITS__") {
		t.Error("the commits placeholder survived substitution — the page would not parse")
	}
	if !strings.Contains(html, "const COMMITS = null;") {
		t.Errorf("nothing was measured, so the card must be handed null:\n%s", firstLineWith(html, "const COMMITS"))
	}
}

// And the other half of the same contract: what is measured arrives keyed
// the browser's way, with a from marker that is a real timestamp.
func TestDashInlinesMeasuredCommits(t *testing.T) {
	needGit(t)
	roots := t.TempDir()
	repo := newRepo(t, roots, "webshop", "dev@example.invalid")
	// Anchored to yesterday noon rather than to the clock: hung off time.Now()
	// the fixture straddles midnight for the first hours of every day — the
	// commits land on one calendar day and the wall's first post, the floor
	// under every day it judges, on the next — and the test went red once a
	// night (2026-09-03, 01:08).
	anchor := yesterdayNoon()
	commitAt(t, repo, "a", "dev@example.invalid", anchor.Add(-2*time.Hour))
	commitAt(t, repo, "b", "dev@example.invalid", anchor.Add(-3*time.Hour))

	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv("WALLII_REPO_ROOTS", roots)
	if err := wall.Append(dir, wall.Event{TS: anchor.Add(-time.Hour), Repo: "webshop", Actor: "bot/builder", Msg: "shipped a thing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdDash([]string{"-o", filepath.Join(dir, "d.html")}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "d.html"))
	if err != nil {
		t.Fatal(err)
	}
	line := firstLineWith(string(b), "const COMMITS")
	if strings.Contains(line, "null") {
		t.Fatalf("a measured repo must not read as unmeasured: %s", line)
	}
	if !strings.Contains(line, `"commits":[`) {
		t.Errorf("the commits must arrive as an array indexed by the calendar:\n%s", line)
	}
	if !strings.Contains(line, ",2,") && !strings.Contains(line, "[2,") {
		t.Errorf("want a day carrying the two commits that were made, got:\n%s", line)
	}
	if !strings.Contains(line, `"from_i":`) {
		t.Errorf("from_i is mandatory — without it every bucket before the window renders as zero commits:\n%s", line)
	}
	if !strings.Contains(line, `"to_i":`) {
		t.Errorf("to_i is mandatory — without it a dashboard opened next week renders the days since as zero commits:\n%s", line)
	}
	// the axis itself must be there and must not be null: the page divides
	// by its length
	cal := firstLineWith(string(b), "const CAL")
	if strings.Contains(cal, "__WALLII_CAL__") || strings.Contains(cal, "null") {
		t.Errorf("the calendar is always inlined, even for an empty wall:\n%s", cal)
	}
	if !strings.Contains(line, `"repos":["webshop"]`) {
		t.Errorf("the card must be told which repos were measured, or it counts every repo's posts:\n%s", line)
	}
}

// The browser half is where the two contracts that matter most live — a
// bucket outside the collected window is a gap, never a zero, and the card's
// numerator is the posts of measured repos only — and no Go test can see
// either from Go. So the dashboard's own script runs here under node, with a
// DOM that swallows every call it makes, and aggregate() is read back as
// data. Both contracts were broken once while every Go test was green
// (found in review, 2026-09-03). Skipped, and said so, where node is not on
// the PATH.
func TestDashCardAggregatesWhatTheGoSideCounted(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half of the card cannot be executed without it")
	}
	// Fixed historical dates, in a fixed zone: the axis is built into the
	// file now, so nothing here has to be hung off the clock. yesterdayNoon()
	// existed because this test went red once a night; it cannot any more.
	loc := mustLoc(t, "Europe/Berlin")
	today := wall.DayStart(time.Date(2026, time.April, 2, 15, 0, 0, 0, loc), loc)
	from := today.AddDate(0, 0, -5)     // the collected window: five days back …
	to := today.AddDate(0, 0, -2)       // … up to but not including two days ago
	measured := today.AddDate(0, 0, -4) // a day inside it, with commits and posts
	noon := measured.Add(12 * time.Hour)
	first := today.AddDate(0, 0, -8).Add(12 * time.Hour) // so the file spans more than the range

	// two posts on the measured day: one in the measured repo, one in the
	// repo nobody found a checkout for — the second leaves the numerator the
	// way its repo left the denominator
	f := makeDashFixture(t, loc, today.Add(15*time.Hour), &dashCoverage{
		from: from, to: to,
		byDay:        map[string]int{measured.Format("2006-01-02"): 12},
		Repos:        []string{"webshop"},
		BlindCommits: wall.DefaultBlindCommits, BlindPosts: wall.DefaultBlindPosts,
		Measured: 1, OnWall: 2, Unresolved: []string{"orphan"},
	}, []dashEvent{
		{T: first.UnixMilli(), Repo: "webshop", Actor: "bot/builder", Msg: "shipped earlier"},
		{T: noon.UnixMilli(), Repo: "webshop", Actor: "bot/builder", Msg: "shipped a thing"},
		{T: noon.Add(time.Hour).UnixMilli(), Repo: "orphan", Actor: "bot/builder", Msg: "shipped elsewhere"},
	})

	res := runDashAggregate(t, node, f, 7, "")
	if len(res.Days) != 7 || len(res.Buckets) != 7 {
		t.Fatalf("a 7-day range walked %d days into %d buckets", len(res.Days), len(res.Buckets))
	}
	for i, day := range res.Days {
		at := time.UnixMilli(day.T0).In(loc)
		inside := !at.Before(from) && at.Before(to)
		switch {
		case day.Cov != inside:
			t.Errorf("%s: measured=%v, but the collected window is [%s, %s) — a day outside it is a gap, never a zero",
				at.Format("2006-01-02"), day.Cov, from.Format("01-02"), to.Format("01-02"))
		case !inside && (day.Commits != 0 || day.Posts != 0):
			t.Errorf("%s: a gap carries %d commits and %d posts", at.Format("2006-01-02"), day.Commits, day.Posts)
		case at.Equal(measured) && (day.Commits != 12 || day.Posts != 1):
			t.Errorf("the measured day holds %d commits and %d posts, want 12 and 1 — the orphan's post must not count", day.Commits, day.Posts)
		}
		if b := res.Buckets[i]; b.Cov != day.Cov || b.Commits != day.Commits || b.Mposts != day.Posts {
			t.Errorf("daily bucket %s disagrees with its day: %+v vs %+v", at.Format("2006-01-02"), b, day)
		}
	}
}

// `dash --since` and `coverage --since` have to cut the same window. git is
// asked for whole days — that is what a day bucket is, and coverageWindow
// rounds down to local midnight before asking. Cutting the posts at the raw
// timestamp instead puts a full day of commits against a partial day of
// posts on the window's first day: measured off one wall, `coverage --since
// 3d` read 4 posts against 12 commits and called the day covered, while
// `dash --since 3d` read 1 post against the same 12 and called it blind.
//
// The stamp has to name the day the window really opens on, not the flag:
// `--since 36h` reaches back to the midnight before, and a reader counting
// posts against "36h" would come up short.
func TestDashCutsItsWindowAtLocalMidnightLikeCoverage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	// an empty roots dir, or cmdDash falls back to defaultRepoRoots and forks
	// git against whatever checkouts this machine happens to have
	t.Setenv("WALLII_REPO_ROOTS", t.TempDir())
	now := time.Now()
	loc, err := reportZone()
	if err != nil {
		t.Fatal(err)
	}
	since, err := parseSince("3d", now, loc)
	if err != nil {
		t.Fatal(err)
	}
	day := wall.DayStart(since, loc)
	// At exactly local midnight the two cuts coincide and this fixture tells
	// them apart no better than any other — one millisecond out of a day. It
	// still passes there; it just proves less, which is why it says so here
	// rather than skipping and going quiet.
	early := "cart totals stable across discount rounds"
	for _, e := range []wall.Event{
		{TS: day, Repo: "webshop", Actor: "bot/builder", Msg: early},
		{TS: now.Add(-time.Hour), Repo: "webshop", Actor: "bot/builder", Msg: "invoice numbering holds under retries"},
	} {
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "d.html")
	if err := cmdDash([]string{"--since", "3d", "-o", out}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if !strings.Contains(html, early) {
		t.Errorf("the post at local midnight of the window's first day was cut out — git is asked from that midnight, so the posts have to start there too")
	}
	if want := "only posts since " + day.Format("2006-01-02") + " included"; !strings.Contains(html, want) {
		t.Errorf("the stamp does not say %q — it has to name the day the window opens on, not the flag:\n%s", want, firstLineWith(html, "only posts since"))
	}
}

// "all" must reach back to everything the file knows about, not just to its
// first post. The collected commit window starts at a local midnight, and
// its first day is regularly a day nobody posted on — with --since it is the
// normal case. Starting the walk at RAW[0] leaves those days out of the walk
// entirely, and a day that is never walked cannot render as a gap either:
// its commits leave without a trace, so "all" showed fewer commits than
// "7d" off the same file, with nothing on the page saying anything was
// missing.
func TestDashAllReachesBackToTheCollectedWindow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half of the card cannot be executed without it")
	}
	loc := mustLoc(t, "Europe/Berlin")
	now := time.Date(2026, time.April, 2, 15, 0, 0, 0, loc)
	today := wall.DayStart(now, loc)
	from := today.AddDate(0, 0, -5) // the collected window opens here …
	post := today.AddDate(0, 0, -2) // … but nobody posted until three days later
	to := today.AddDate(0, 0, 1)

	f := makeDashFixture(t, loc, now, &dashCoverage{
		from: from, to: to,
		byDay:        map[string]int{from.Format("2006-01-02"): 12},
		Repos:        []string{"webshop"},
		BlindCommits: wall.DefaultBlindCommits, BlindPosts: wall.DefaultBlindPosts,
		Measured: 1, OnWall: 1,
	}, []dashEvent{
		{T: post.Add(12 * time.Hour).UnixMilli(), Repo: "webshop", Actor: "bot/builder", Msg: "shipped a thing"},
	})
	res := runDashAggregate(t, node, f, 0, "")
	if len(res.Days) == 0 {
		t.Fatal(`"all" walked no days at all`)
	}
	// compared as a calendar day, not as an instant: this test is about which
	// day the walk opens on, and where local midnight does not exist — a DST
	// jump at 00:00, as in America/Santiago — Go's time.Date and the
	// browser's setHours each land an hour to one side of the boundary. An
	// instant comparison would then report two identical dates as unequal
	// and say nothing about the thing being tested.
	first := time.UnixMilli(res.Days[0].T0).In(loc)
	if got, want := first.Format("2006-01-02"), from.Format("2006-01-02"); got != want {
		t.Errorf(`"all" starts its walk at %s, want %s — the collected window opens there and its commits have to be walked`, got, want)
	}
	var commits int
	for _, day := range res.Days {
		commits += day.Commits
	}
	if commits != 12 {
		t.Errorf(`"all" counts %d commits, want 12 — the measured days before the first post fell out of the walk`, commits)
	}
}

// The property the whole calendar exists for: ONE file, read under six
// viewer zones, must read the same. It used to not — the same dashboard said
// "0 blind of 1 worked, 12 commits" under Europe/Berlin and "0 of 0 worked"
// under Pacific/Auckland, all twelve commits gone.
//
// The compared string carries the rendered strings too, not only the
// aggregate: without fmtDay and the feed's clock time the test would be
// green while every feed row under Auckland showed the wrong hour.
func TestDashboardReadsTheSameUnderAnyViewerZone(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half cannot be executed without it")
	}
	// built in a fixed producer zone over a window containing a DST change
	// (Europe/Berlin springs forward on 2026-03-29)
	loc := mustLoc(t, "Europe/Berlin")
	now := time.Date(2026, time.April, 2, 15, 0, 0, 0, loc)
	today := wall.DayStart(now, loc)
	from, to := today.AddDate(0, 0, -10), today.AddDate(0, 0, -1)
	evs := []dashEvent{}
	for i := 10; i >= 1; i-- {
		at := today.AddDate(0, 0, -i)
		// three posts a day at hours that fall on different calendar days in
		// different viewer zones — 00:30 and 23:30 are the whole test
		for _, h := range []int{0, 12, 23} {
			evs = append(evs, dashEvent{
				T:    at.Add(time.Duration(h)*time.Hour + 30*time.Minute).UnixMilli(),
				Repo: "webshop", Actor: "bot/builder", Msg: "post", Out: "ok",
			})
		}
	}
	byDay := map[string]int{}
	for i := 10; i >= 1; i-- {
		byDay[today.AddDate(0, 0, -i).Format("2006-01-02")] = i
	}
	f := makeDashFixture(t, loc, now, &dashCoverage{
		from: from, to: to, byDay: byDay, Repos: []string{"webshop"},
		BlindCommits: wall.DefaultBlindCommits, BlindPosts: wall.DefaultBlindPosts,
		Measured: 1, OnWall: 1,
	}, evs)

	var want string
	for _, zone := range []string{
		"Europe/Berlin", "Pacific/Auckland", "America/Santiago",
		"UTC", "America/Los_Angeles", "Pacific/Kiritimati",
	} {
		res := runDashAggregate(t, node, f, 7, zone)
		got, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			want = string(got)
			if !strings.Contains(want, `"FirstPostTime":"00:30"`) {
				t.Fatalf("the fixture lost its midnight-adjacent post — this test proves nothing without it:\n%s", want)
			}
			continue
		}
		if string(got) != want {
			t.Errorf("read under TZ=%s the same file says something else:\n got %s\nwant %s", zone, got, want)
		}
	}
}

// A post dated after the file was written falls out of every bucket — it did
// before the binary search too, and that property is what CAL.end protects:
// without the upper bound it would stick to the last day and be counted.
func TestDashDropsAPostFromAfterTheFileWasWritten(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half cannot be executed without it")
	}
	loc := mustLoc(t, "Europe/Berlin")
	now := time.Date(2026, time.April, 2, 15, 0, 0, 0, loc)
	today := wall.DayStart(now, loc)
	f := makeDashFixture(t, loc, now, nil, []dashEvent{
		{T: today.Add(9 * time.Hour).UnixMilli(), Repo: "webshop", Actor: "bot/builder", Msg: "today"},
	})
	// a post three days into the future, appended after the calendar was built
	skewed := today.AddDate(0, 0, 3).Add(9 * time.Hour).UnixMilli()
	f.Evs = strings.TrimSuffix(f.Evs, "]") +
		`,{"t":` + strconv.FormatInt(skewed, 10) + `,"repo":"webshop","actor":"bot/builder","msg":"clock skew"}]`

	res := runDashAggregate(t, node, f, 0, "")
	var posts int
	for _, b := range res.Buckets {
		posts += b.Mposts
	}
	if n := len(res.Days); n != 1 {
		t.Fatalf("the axis grew to %d days — a future-dated post must not extend it", n)
	}
	total := 0
	for _, row := range res.Heat {
		for _, v := range row {
			total += v
		}
	}
	if total != 1 {
		t.Errorf("the heatmap counted %d posts, want 1 — the future-dated post was bucketed", total)
	}
}

// runDashAggregate runs dash.html's own script under node with the given
// data inlined and hands back what aggregate(rangeDays) built. A stub DOM
// accepts everything and answers with itself, so the page's top-level
// rendering runs to the end without a browser.
// viewerZone is the TZ the node process reads the file under. "" leaves the
// environment alone; every other value is the whole point of the test that
// passes one — the same file must read the same in every zone.
func runDashAggregate(t *testing.T, node string, f dashFixture, rangeDays int, viewerZone string) dashAggResult {
	t.Helper()
	start, end := strings.Index(dashTemplate, "<script>"), strings.LastIndex(dashTemplate, "</script>")
	if start < 0 || end < 0 {
		t.Fatal("dash.html has no <script> block to run")
	}
	script := strings.NewReplacer(
		"__GENERATED__", "fixture",
		"__WALLII_CAL__", f.Cal,
		"__WALLII_COMMITS__", f.Cov,
		"__WALLII_FAMILIES__", "{}",
		"__WALLII_DATA__", f.Evs,
	).Replace(dashTemplate[start+len("<script>") : end])
	// a DOM that accepts everything and answers with itself, so the page's
	// own top-level rendering runs to the end without a browser
	harness := `const stub = new Proxy(function () {}, {
  get: (_, k) => k === Symbol.toPrimitive ? () => 0 : k === Symbol.iterator ? function* () {} : k === "then" ? undefined : stub,
  set: () => true, apply: () => stub, construct: () => stub, has: () => true,
});
for (const g of ["document", "window", "localStorage", "navigator", "matchMedia", "location", "requestAnimationFrame"]) globalThis[g] = stub;
` + script + `
const agg = aggregate(` + strconv.Itoa(rangeDays) + `);
console.log("RESULT " + JSON.stringify({
  days: agg.days,
  buckets: agg.buckets.map(b => ({ t0: b.t0, cov: b.cov, commits: b.commits, mposts: b.mposts })),
  heat: agg.heat,
  // rendered strings too: without them a viewer in another zone could read
  // the same aggregate and still see the wrong hour on every feed row
  firstDay: agg.days.length ? fmtDay(agg.days[0].t0) : "",
  firstPostTime: RAW.length ? fmtTime(RAW[0].t) : "",
}));
`
	path := filepath.Join(t.TempDir(), "dash-harness.js")
	if err := os.WriteFile(path, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, path)
	if viewerZone != "" {
		cmd.Env = append(os.Environ(), "TZ="+viewerZone)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the dashboard script did not run under node: %v\n%s", err, out)
	}
	_, payload, ok := strings.Cut(string(out), "RESULT ")
	if !ok {
		t.Fatalf("no result line from node:\n%s", out)
	}
	var res dashAggResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &res); err != nil {
		t.Fatalf("cannot read the aggregate back: %v\n%s", err, out)
	}
	return res
}

type dashAggResult struct {
	Days []struct {
		T0             int64
		Commits, Posts int
		Cov            bool
	}
	Buckets []struct {
		T0              int64
		Commits, Mposts int
		Cov             bool
	}
	Heat          [][]int
	FirstDay      string
	FirstPostTime string
}

// dashFixture is the three inlined constants, built the way cmdDash builds
// them: the calendar comes out of buildDashCalendar and the commits are
// indexed onto it, so a test pins the seam rather than a hand-written array
// that could agree with nothing.
type dashFixture struct{ Cal, Cov, Evs string }

func makeDashFixture(t *testing.T, loc *time.Location, now time.Time, cov *dashCoverage, evs []dashEvent) dashFixture {
	t.Helper()
	posts := make([]wall.Event, 0, len(evs))
	for _, e := range evs {
		posts = append(posts, wall.Event{TS: time.UnixMilli(e.T)})
	}
	cal := buildDashCalendar(posts, cov, now, loc)
	cov = indexedCoverage(cov, cal, loc)
	calJSON, err := json.Marshal(cal)
	if err != nil {
		t.Fatal(err)
	}
	covJSON, err := json.Marshal(cov)
	if err != nil {
		t.Fatal(err)
	}
	evsJSON, err := json.Marshal(evs)
	if err != nil {
		t.Fatal(err)
	}
	return dashFixture{Cal: string(calJSON), Cov: string(covJSON), Evs: string(evsJSON)}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// yesterdayNoon is a fixture clock that no day boundary can move across:
// local noon of the previous day, so everything hung a few hours off it
// stays on one calendar day and inside every window a test asks for.
func yesterdayNoon() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d-1, 12, 0, 0, 0, time.Local)
}

func firstLineWith(s, needle string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			if len(l) > 300 {
				return l[:300] + "…"
			}
			return l
		}
	}
	return "(not found)"
}

// The output's own shape is part of the doctrine: the blind days lead, the
// ratio follows, and the ratio never wears a percentage — the same restraint
// the grader line keeps, for the same reason. A percentage is a dial; "0.37
// per commit" describes.
func TestCoverageOutputLeadsWithBlindDaysAndNeverAPercentage(t *testing.T) {
	needGit(t)
	roots := t.TempDir()
	repo := newRepo(t, roots, "webshop", "dev@example.invalid")
	anchor := yesterdayNoon() // off the clock, for the reason given in the dash test above
	for i := 0; i < 12; i++ {
		commitAt(t, repo, fmt.Sprintf("f%d", i), "dev@example.invalid", anchor.Add(-time.Duration(i+2)*time.Minute))
	}
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv("WALLII_REPO_ROOTS", roots)
	if err := wall.Append(dir, wall.Event{TS: anchor.Add(-time.Minute), Repo: "webshop", Actor: "bot/builder", Msg: "shipped a thing"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error { return cmdCoverage([]string{"--since", "7d"}) })

	blind, ratio := strings.Index(out, "blind days"), strings.Index(out, "per commit")
	if blind < 0 || ratio < 0 {
		t.Fatalf("both halves must be printed:\n%s", out)
	}
	if blind > ratio {
		t.Fatalf("the blind days are the headline and the ratio the footnote, not the other way round:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "per commit") && strings.Contains(line, "%") {
			t.Errorf("the ratio must not wear a percentage — that is the dial this reading refuses to be: %q", line)
		}
	}
	if !strings.Contains(out, "never merged") {
		t.Errorf("the head must state the price of counting only HEAD:\n%s", out)
	}
	// 12 commits today, one post: a blind day by the defaults
	if !strings.Contains(out, "1 of 1 worked day") {
		t.Errorf("a day of 12 commits and 1 post is blind at the defaults:\n%s", out)
	}
}

// The window begins at local midnight of its first day. `--since 3d` typed
// at noon would otherwise hand the oldest day only its afternoon — commits
// and posts alike — and then judge it as a whole day. Everything hangs off
// one fixed clock, the command's included: drop the floor in coverageWindow
// and both halves go red, and nothing else can make them.
func TestCoverageWindowStartsAtMidnight(t *testing.T) {
	anchor := yesterdayNoon()
	since, _, err := coverageWindow("3d", "", anchor, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	y, m, d := anchor.AddDate(0, 0, -3).Date()
	want := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	if !since.Equal(want) {
		t.Fatalf("the window starts at %s, want midnight %s — the oldest day would be judged on its last hours alone", since, want)
	}

	// through the command, on the same clock: a commit half an hour into
	// the oldest day has to appear on that day
	needGit(t)
	coverageClock = func() time.Time { return anchor }
	t.Cleanup(func() { coverageClock = time.Now })
	roots := t.TempDir()
	repo := newRepo(t, roots, "webshop", "dev@example.invalid")
	oldest := want.Add(30 * time.Minute)
	commitAt(t, repo, "early", "dev@example.invalid", oldest)
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv("WALLII_REPO_ROOTS", roots)
	if err := wall.Append(dir, wall.Event{TS: want.Add(12 * time.Hour), Repo: "webshop", Actor: "bot/builder", Msg: "noon of the oldest day"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error { return cmdCoverage([]string{"--since", "3d", "--json"}) })
	var c wall.Cov
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	key := want.Format("2006-01-02")
	for _, day := range c.Days {
		if day.Day == key {
			if day.Commits != 1 {
				t.Fatalf("%s must carry its 00:30 commit — a day is judged whole or not at all; got %+v", key, day)
			}
			return
		}
	}
	t.Fatalf("the oldest day %s is not in the window at all: %+v", key, c.Days)
}

// Under --split the measurement — how many of the wall's repos were measured,
// which were not and why — is fixed by the commit map, which spans the whole
// window. Printed in each half it read as two findings. It stands once, above
// both halves; what each half owns is the repos posted to in it.
func TestCoverageSplitNamesTheMeasurementOnce(t *testing.T) {
	needGit(t)
	roots := t.TempDir()
	repo := newRepo(t, roots, "webshop", "dev@example.invalid")
	anchor := yesterdayNoon()
	commitAt(t, repo, "old", "dev@example.invalid", anchor.AddDate(0, 0, -4))
	commitAt(t, repo, "new", "dev@example.invalid", anchor)
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv("WALLII_REPO_ROOTS", roots)
	for _, e := range []wall.Event{
		{TS: anchor.AddDate(0, 0, -4), Repo: "webshop", Actor: "bot/builder", Msg: "before the split"},
		{TS: anchor.AddDate(0, 0, -4).Add(time.Minute), Repo: "ghost", Actor: "bot/builder", Msg: "no checkout anywhere"},
		{TS: anchor, Repo: "webshop", Actor: "bot/builder", Msg: "after the split"},
	} {
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	split := anchor.AddDate(0, 0, -2).Format("2006-01-02")
	out := captureStdout(t, func() error { return cmdCoverage([]string{"--since", "7d", "--split", split}) })

	var measured, notMeasured int
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "measured"):
			measured++
		case strings.HasPrefix(l, "not measured"):
			notMeasured++
		}
	}
	if measured != 1 || notMeasured != 1 {
		t.Errorf("the measurement stands once above both halves; got %d measured and %d not-measured lines:\n%s", measured, notMeasured, out)
	}
	if n := strings.Count(out, "posted to"); n != 2 {
		t.Errorf("each half names the repos posted to in it — want 2 such lines, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "ghost (no checkout found)") {
		t.Errorf("the repo nobody could measure must still be named:\n%s", out)
	}

	// the licence for printing it once: both halves fold the same commit
	// map, so they must agree on what was measured and what was not
	raw := captureStdout(t, func() error { return cmdCoverage([]string{"--since", "7d", "--split", split, "--json"}) })
	var halves []wall.Cov
	if err := json.Unmarshal([]byte(raw), &halves); err != nil || len(halves) != 2 {
		t.Fatalf("two halves expected: %v\n%s", err, raw)
	}
	if halves[0].Measured != halves[1].Measured || fmt.Sprint(halves[0].Unresolved) != fmt.Sprint(halves[1].Unresolved) {
		t.Fatalf("the halves disagree on the measurement, so naming it once would hide one of them:\n%+v\n%+v", halves[0].Unresolved, halves[1].Unresolved)
	}
}

// The dashboard colors by family, and the rule that turns an actor into a
// family lives in Go — so the page is handed the mapping, not the rule.
func TestDashInlinesActorFamilies(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv("WALLII_REPO_ROOTS", t.TempDir())
	now := time.Now()
	for i, actor := range []string{"claude/main", "codex/main", "cron:nightly"} {
		if err := wall.Append(dir, wall.Event{TS: now.Add(-time.Duration(i+1) * time.Minute), Repo: "webshop", Actor: actor, Msg: "posted"}); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "d.html")
	if _, err := captureStdout(t, func() error { return cmdDash([]string{"-o", out}) }), error(nil); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	line := firstLineWith(string(html), "const FAMILIES")
	for _, want := range []string{`"claude/main":"claude"`, `"codex/main":"codex"`, `"cron:nightly":"cron"`} {
		if !strings.Contains(line, want) {
			t.Errorf("the page must be handed %s: %s", want, line)
		}
	}
}

// The family chip narrows every card but one. The blind-days card counts
// every post in range whatever the chip says — a blind day is a repo's day,
// and a per-family numerator over a per-repo denominator would be the actor
// split the ratio refuses. And the series the activity chart stacks are
// families, so claude/main and claude/ops are one color, not two.
func TestDashFamilyFilterLeavesTheBlindDaysAlone(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half cannot be executed without it")
	}
	loc := mustLoc(t, "Europe/Berlin")
	now := time.Date(2026, time.April, 2, 15, 0, 0, 0, loc)
	today := wall.DayStart(now, loc)
	from, to := today.AddDate(0, 0, -5), today.AddDate(0, 0, -2)
	measured := today.AddDate(0, 0, -4)
	noon := measured.Add(12 * time.Hour)
	f := makeDashFixture(t, loc, now, &dashCoverage{
		from: from, to: to,
		byDay: map[string]int{measured.Format("2006-01-02"): 12}, Repos: []string{"webshop"},
		BlindCommits: wall.DefaultBlindCommits, BlindPosts: wall.DefaultBlindPosts, Measured: 1, OnWall: 1,
	}, []dashEvent{
		{T: noon.UnixMilli(), Repo: "webshop", Actor: "claude/main", Msg: "one"},
		{T: noon.Add(time.Hour).UnixMilli(), Repo: "webshop", Actor: "claude/ops", Msg: "two"},
		{T: noon.Add(2 * time.Hour).UnixMilli(), Repo: "webshop", Actor: "codex/main", Msg: "three"},
	})
	start, end := strings.Index(dashTemplate, "<script>"), strings.LastIndex(dashTemplate, "</script>")
	if start < 0 || end < 0 {
		t.Fatal("dash.html has no <script> block to run")
	}
	script := dashTemplate[start+len("<script>") : end]
	script = strings.Replace(script, "__GENERATED__", "fixture", 1)
	script = strings.Replace(script, "__WALLII_CAL__", f.Cal, 1)
	script = strings.Replace(script, "__WALLII_COMMITS__", f.Cov, 1)
	script = strings.Replace(script, "__WALLII_FAMILIES__", `{"claude/main":"claude","claude/ops":"claude","codex/main":"codex"}`, 1)
	script = strings.Replace(script, "__WALLII_DATA__", f.Evs, 1)
	harness := `const stub = new Proxy(function () {}, {
  get: (_, k) => k === Symbol.toPrimitive ? () => 0 : k === Symbol.iterator ? function* () {} : k === "then" ? undefined : stub,
  set: () => true, apply: () => stub, construct: () => stub, has: () => true,
});
for (const g of ["document", "window", "localStorage", "navigator", "matchMedia", "location", "requestAnimationFrame"]) globalThis[g] = stub;
` + script + `
const sum = (xs, f) => xs.reduce((s, x) => s + f(x), 0);
const shape = agg => ({
  evs: agg.evs.length,
  mposts: sum(agg.buckets, b => b.mposts),
  dayPosts: sum(agg.days, d => d.posts),
  families: Object.keys(agg.buckets.reduce((o, b) => Object.assign(o, b.byFamily), {})).sort(),
  actors: Object.keys(agg.actors).sort(),
});
const all = shape(aggregate(7));
currentFamily = "codex";
const codex = shape(aggregate(7));
console.log("RESULT " + JSON.stringify({ all, codex }));`
	path := filepath.Join(t.TempDir(), "dash.js")
	if err := os.WriteFile(path, []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, raw)
	}
	line := firstLineWith(string(raw), "RESULT ")
	type shape struct {
		Evs, Mposts, DayPosts int
		Families, Actors      []string
	}
	var res struct{ All, Codex shape }
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "RESULT ")), &res); err != nil {
		t.Fatalf("result: %v\n%s", err, raw)
	}
	if res.All.Evs != 3 || fmt.Sprint(res.All.Families) != "[claude codex]" {
		t.Errorf("unfiltered: the series are families, not actors: %+v", res.All)
	}
	if res.Codex.Evs != 1 || fmt.Sprint(res.Codex.Families) != "[codex]" || fmt.Sprint(res.Codex.Actors) != "[codex/main]" {
		t.Errorf("the codex chip must narrow the posts, the series and the agents: %+v", res.Codex)
	}
	if res.Codex.Mposts != 3 || res.Codex.DayPosts != 3 || res.All.Mposts != 3 {
		t.Errorf("the blind-days numerator must count every family's posts under any chip: all=%+v codex=%+v", res.All, res.Codex)
	}
}
