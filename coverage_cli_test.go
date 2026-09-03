// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"os"
	"path/filepath"
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

// The day key Go writes and the day key the browser looks up have to be the
// same string. They are built by two different languages from two different
// calendars, and a mismatch is not an off-by-one in the card: every day
// would be filed under a key the browser never asks for, and the panel would
// draw a month of blindness out of nothing.
//
// The literal below is lifted from dash.html's dayKey(), and the template is
// checked for it — editing either side alone turns this red.
func TestDashDayKeyMatchesTheBrowsersDayKey(t *testing.T) {
	const jsDayKey = `function dayKey(d) { return d.getFullYear() + "-" + d.getMonth() + "-" + d.getDate(); }`
	if !strings.Contains(dashTemplate, jsDayKey) {
		t.Fatalf("dash.html no longer builds its bucket keys the way Go does — the card would find nothing:\nwant %s", jsDayKey)
	}
	cases := map[string]time.Time{
		"2026-0-5":   time.Date(2026, time.January, 5, 12, 0, 0, 0, time.UTC),  // month 0-based, no leading zero
		"2026-7-9":   time.Date(2026, time.August, 9, 23, 59, 0, 0, time.UTC),  // the day the live wall started
		"2026-11-31": time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC), // December is 11
	}
	for want, at := range cases {
		if got := wall.DashDayKey(at); got != want {
			t.Errorf("DashDayKey(%s) = %q, want %q", at.Format("2006-01-02"), got, want)
		}
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
	key := wall.DashDayKey(anchor.Add(-2 * time.Hour))
	if !strings.Contains(line, fmt.Sprintf("%q:2", key)) {
		t.Errorf("want two commits under the browser's own day key %q, got:\n%s", key, line)
	}
	if !strings.Contains(line, `"from":`) {
		t.Errorf("from is mandatory — without it every bucket before the window renders as zero commits:\n%s", line)
	}
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
