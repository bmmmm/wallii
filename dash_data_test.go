// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// inlinedConst reads `const NAME = <json> || …;` back out of a written
// dashboard — the value Go handed the page, before the page's fallback.
func inlinedConst(t *testing.T, html, name string, into any) {
	t.Helper()
	line := firstLineWith(html, "const "+name+" = ")
	if len(line) > 300 { // firstLineWith truncates for messages; read the real line
		for _, l := range strings.Split(html, "\n") {
			if strings.HasPrefix(l, "const "+name+" = ") {
				line = l
				break
			}
		}
	}
	v := strings.TrimPrefix(line, "const "+name+" = ")
	if i := strings.Index(v, " || "); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSuffix(v, ";")
	if err := json.Unmarshal([]byte(v), into); err != nil {
		t.Fatalf("cannot read %s back: %v\n%.300s", name, err, v)
	}
}

// The dashboard's cost, doubt and limit data, read the way the page gets
// it. The window cut is the point: a unit is the delta to the session's
// previous post, and that post may lie before --since.
func TestDashInlinesCostsDoubtAndLimits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_REPO_ROOTS", t.TempDir()) // no git: this test is about the wall side
	now := time.Now()
	day := 24 * time.Hour
	early := now.Add(-5 * day) // before the --since 2d window
	late := now.Add(-day)      // inside it

	ok := wall.Event{TS: late.Add(2 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "feature",
		Msg: "retry loop waits for the fsync before rename", Outcome: wall.OutcomeOK}
	fix := wall.Event{TS: late.Add(3 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "fix",
		Msg: "fsync in the retry loop was skipped on close", Outcome: wall.OutcomeOK}
	earlyOK := wall.Event{TS: early.Add(time.Hour), Repo: "garden", Actor: "bot/builder", Topic: "feature",
		Msg: "sprinkler schedule honours the frost guard", Outcome: wall.OutcomeOK}
	earlyFix := wall.Event{TS: early.Add(2 * time.Hour), Repo: "garden", Actor: "bot/builder", Topic: "fix",
		Msg: "frost guard skipped the sprinkler schedule at dawn", Outcome: wall.OutcomeOK}
	first := wall.Event{TS: early, Repo: "webshop", Actor: "bot/builder", Msg: "first unit of the session",
		Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 1.00, TokCum: 1000}
	second := wall.Event{TS: late, Repo: "webshop", Actor: "bot/builder", Msg: "second unit of the session",
		Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 1.25, TokCum: 1400,
		SqueezeP: 61, Squeeze5h: 12, SqueezeSrc: "session"}
	unmeasured := wall.Event{TS: late.Add(time.Hour), Repo: "webshop", Actor: "bot/builder", Msg: "nobody read the meter"}

	openC := wall.Event{TS: early.Add(3 * time.Hour), Repo: first.Repo, Actor: "wallii/lint", Kind: wall.KindChallenge,
		Parent: first.ID(), Msg: "graded ok with a leftover — regrade or say why not"}
	closedC := wall.Event{TS: early.Add(4 * time.Hour), Repo: first.Repo, Actor: "bot/critic", Kind: wall.KindChallenge,
		Parent: first.ID(), Msg: "which commit is this"}
	answer := wall.Event{TS: early.Add(5 * time.Hour), Repo: first.Repo, Actor: "bot/builder", Kind: wall.KindReact,
		Parent: closedC.ID(), Msg: "the one in the ref"}

	for _, e := range []wall.Event{first, earlyOK, earlyFix, openC, closedC, answer, second, unmeasured, ok, fix} {
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "d.html")
	if err := cmdDash([]string{"-o", out, "--since", "2d"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)

	var raw []dashEvent
	inlinedConst(t, html, "RAW", &raw)
	byMsg := map[string]dashEvent{}
	for _, e := range raw {
		byMsg[e.Msg] = e
	}
	if _, in := byMsg[first.Msg]; in {
		t.Fatal("the fixture's first post is inside the window — the edge this test is about is not there")
	}
	switch s := byMsg[second.Msg]; {
	case s.Usd == nil:
		t.Errorf("the second unit carries no cost")
	case *s.Usd < 0.2499 || *s.Usd > 0.2501:
		t.Errorf("the second unit reads $%.2f, want the $0.25 delta — a unit read over the window counts from session start", *s.Usd)
	}
	if s := byMsg[second.Msg]; s.Sq7 != 61 || s.Sq5 != 12 || s.Sess != "aaaaaaaa" {
		t.Errorf("limits or session lost on the way: %+v", s)
	}
	if u := byMsg[unmeasured.Msg]; u.Usd != nil {
		t.Errorf("an unmeasured post arrives as $%.2f — absent means nobody measured, never free", *u.Usd)
	}
	for _, e := range raw {
		if e.ID == "" {
			t.Errorf("post %q has no id — challenges and haunted pairs point at it", e.Msg)
		}
	}

	var doubt dashDoubt
	inlinedConst(t, html, "DOUBT", &doubt)
	if len(doubt.Challenges) != 1 || doubt.Challenges[0].Msg != openC.Msg {
		t.Fatalf("want exactly the unanswered challenge, from before the window as it is — got %+v", doubt.Challenges)
	}
	if c := doubt.Challenges[0]; c.Target != first.ID() || c.Repo != "webshop" || c.TMsg != first.Msg {
		t.Errorf("the challenge must name its target, which is not inlined: %+v", c)
	}
	if len(doubt.Haunted) != 1 || doubt.Haunted[0].OK != ok.ID() || doubt.Haunted[0].Fix != fix.ID() {
		t.Errorf("want the one haunted pair whose ok is inside the window, got %+v", doubt.Haunted)
	}
	// Positive control for the exclusion: the early pair is a haunting too,
	// or dropping it proves nothing.
	if n := len(wall.Hauntings([]wall.Event{earlyOK, earlyFix})); n != 1 {
		t.Fatalf("the early pair is not a haunting (%d) — the window cut above is untested", n)
	}
}
