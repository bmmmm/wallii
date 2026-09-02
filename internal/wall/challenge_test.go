// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Invented posts that draw a doubt, in the style of lint_test.go. The
// leftover set ends with the longest markers on the list, the friction set
// too — the cap test has to see the worst case, not the typical one.
func doubtfulPosts() []Event {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	leftover := []string{
		"cache layer landed, invalidation not yet wired",
		"queue drained except for the poison messages",
		"import path migration done, the legacy shim is parked for now",
		"retry budget raised, der Consumer haengt noch immer offen",
		"deps bumped, the smoke suite is still missing on the runner",
		"pagination shipped, only the first page carries the totals",
	}
	friction := []string{
		"backup share endlich read-only gemountet, der FUSE-Weg war Sackgasse",
		"shipped with a workaround for the upstream panic",
		"deploy rolled back, the health check never went green",
		"submit button pinned at 552px — im fuenften versuch, scroll anchor an 5 Stellen",
		"tracked it down after several attempts, the map was a red herring",
		"build hung on the module cache until the lock was cleared",
	}
	var out []Event
	for i, m := range leftover {
		out = append(out, Event{TS: ts.Add(time.Duration(i) * time.Minute), Repo: "webshop", Actor: "bot/builder", Msg: m, Outcome: OutcomeOK})
	}
	for i, m := range friction {
		out = append(out, Event{TS: ts.Add(time.Duration(10+i) * time.Minute), Repo: "api-gateway", Actor: "bot/builder", Msg: m, Mood: "good"})
	}
	return out
}

func leftoverPost(ts time.Time, actor, repo string) Event {
	return Event{TS: ts, Repo: repo, Actor: actor, Msg: "cache layer landed, invalidation not yet wired", Outcome: OutcomeOK, Mood: "ok"}
}

func frictionPost(ts time.Time, actor, repo string) Event {
	return Event{TS: ts, Repo: repo, Actor: actor, Msg: "deploy rolled back, the health check never went green", Outcome: OutcomePartial, Mood: "good"}
}

// mustAppend lives in store_test.go.

func openLint(t *testing.T, dir string, now time.Time, actor string) []OpenChallenge {
	t.Helper()
	evs, _, err := ParseFile(CurrentFile(dir, now))
	if err != nil {
		t.Fatal(err)
	}
	var out []OpenChallenge
	for _, c := range OpenChallenges(evs) {
		if c.Challenge.Actor == LintActor && c.HasTarget && c.Target.Actor == actor {
			out = append(out, c)
		}
	}
	return out
}

// The stderr notes do not fit: the longest is 124 runes, and a challenge is
// a message like any other. Every doubt's Ask must fit the cap, and the
// event built from it must be storable.
func TestLintAskFitsTheMessageCap(t *testing.T) {
	for _, e := range doubtfulPosts() {
		ds := Doubts(e)
		if len(ds) == 0 {
			t.Errorf("fixture draws no doubt: %q", e.Msg)
			continue
		}
		for _, d := range ds {
			if n := utf8.RuneCountInString(d.Ask); n > MaxMsgRunes {
				t.Errorf("ask is %d runes for %q: %q", n, d.Marker, d.Ask)
			}
			ch := Event{TS: e.TS, Repo: e.Repo, Actor: LintActor, Kind: KindChallenge, Parent: e.ID(), Msg: d.Ask}
			if err := ch.Validate(); err != nil {
				t.Errorf("challenge for %q is not storable: %v", d.Marker, err)
			}
		}
	}
}

// The ask has to point at the grade. A challenge that only asked "really?"
// leaves the conclusion to the agent, and the cheapest conclusion is to stop
// writing the word — this test is what goes red if anyone rewrites it that
// way.
func TestLintAskNamesTheRegrade(t *testing.T) {
	for _, e := range doubtfulPosts() {
		for _, d := range Doubts(e) {
			target := map[DoubtClass]string{DoubtLeftover: "partial", DoubtFriction: "ok"}[d.Class]
			if !strings.Contains(d.Ask, target) {
				t.Errorf("%s ask does not name %q: %q", d.Class, target, d.Ask)
			}
			if !strings.Contains(d.Ask, "regrade") {
				t.Errorf("%s ask does not offer the regrade: %q", d.Class, d.Ask)
			}
			if !strings.Contains(d.Ask, d.Marker) {
				t.Errorf("%s ask does not quote the words that fired: %q", d.Class, d.Ask)
			}
		}
	}
}

// The property everything rests on: a later post by the same actor does not
// close the lint's challenge. Only a react does — to the challenge or to the
// doubted post — exactly as for a challenge from a colleague.
func TestOpenLintChallengeSurvivesTheActorsNextPost(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	post := leftoverPost(now, "bot/builder", "webshop")
	mustAppend(t, dir, post)
	res, err := RaiseLintChallenge(dir, post, now)
	if err != nil || !res.Raised || res.Doubt.Class != DoubtLeftover {
		t.Fatalf("first doubt must raise a challenge, got %+v (%v)", res, err)
	}
	if res.Challenge.Actor != LintActor || res.Challenge.Kind != KindChallenge || res.Challenge.Parent != post.ID() {
		t.Fatalf("challenge is malformed: %+v", res.Challenge)
	}

	next := Event{TS: now.Add(time.Hour), Repo: "webshop", Actor: "bot/builder", Msg: "checkout total recomputed after a coupon change", Outcome: OutcomeOK, Mood: "good"}
	mustAppend(t, dir, next)
	if open := openLint(t, dir, now, "bot/builder"); len(open) != 1 || open[0].Challenge.ID() != res.Challenge.ID() {
		t.Fatalf("the actor's next post must not close the challenge, open: %+v", open)
	}

	// answered via the thread: a react by the doubted actor to their own post
	mustAppend(t, dir, Event{TS: now.Add(2 * time.Hour), Repo: "webshop", Actor: "bot/builder", Kind: KindReact, Parent: post.ID(), Msg: "regraded in my head: partial, the shim is on the board"})
	if open := openLint(t, dir, now, "bot/builder"); len(open) != 0 {
		t.Fatalf("a react by the doubted actor must close it, still open: %+v", open)
	}
}

// Dedup runs on (actor, class), across repos: an open challenge of the same
// class means nothing is written, an answered one lets the next post raise
// again, and the other class is written regardless. One post draws at most
// one challenge, leftover first. Across a long run of doubtful posts an actor
// never has more than two open lint challenges.
func TestLintChallengeDedupByClass(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tick := func() time.Time { now = now.Add(time.Minute); return now }

	first := leftoverPost(tick(), "bot/builder", "webshop")
	mustAppend(t, dir, first)
	res, err := RaiseLintChallenge(dir, first, now)
	if err != nil || !res.Raised {
		t.Fatalf("first leftover: %+v (%v)", res, err)
	}
	firstID := res.Challenge.ID()

	// same class, other repo: nothing written, the open one is named
	second := leftoverPost(tick(), "bot/builder", "docs-site")
	mustAppend(t, dir, second)
	res, err = RaiseLintChallenge(dir, second, now)
	if err != nil || res.Raised || !res.Open || res.Challenge.ID() != firstID {
		t.Fatalf("same class must dedup across repos, got %+v (%v)", res, err)
	}
	if res.Doubt.Class != DoubtLeftover {
		t.Fatalf("the suppressed doubt must still be reported, got %+v", res.Doubt)
	}

	// other class: written
	third := frictionPost(tick(), "bot/builder", "webshop")
	mustAppend(t, dir, third)
	res, err = RaiseLintChallenge(dir, third, now)
	if err != nil || !res.Raised || res.Doubt.Class != DoubtFriction {
		t.Fatalf("other class must raise, got %+v (%v)", res, err)
	}

	// both classes on one post: one challenge at most, leftover first — and
	// here that one is already open, so nothing is written
	both := Event{TS: tick(), Repo: "webshop", Actor: "bot/builder", Msg: "der erste Ansatz war Sackgasse, rest parked", Outcome: OutcomeOK, Mood: "great"}
	mustAppend(t, dir, both)
	res, err = RaiseLintChallenge(dir, both, now)
	if err != nil || res.Raised || !res.Open || res.Doubt.Class != DoubtLeftover {
		t.Fatalf("leftover goes first and is already open, got %+v (%v)", res, err)
	}
	if open := openLint(t, dir, now, "bot/builder"); len(open) != 2 {
		t.Fatalf("want 2 open (one per class), got %d", len(open))
	}

	// answered → the next one raises again, no cooldown
	mustAppend(t, dir, Event{TS: tick(), Repo: "webshop", Actor: "bot/builder", Kind: KindReact, Parent: firstID, Msg: "fair — partial"})
	fourth := leftoverPost(tick(), "bot/builder", "webshop")
	mustAppend(t, dir, fourth)
	res, err = RaiseLintChallenge(dir, fourth, now)
	if err != nil || !res.Raised {
		t.Fatalf("after an answer the class must raise again, got %+v (%v)", res, err)
	}

	// another actor has their own budget
	other := leftoverPost(tick(), "bot/reviewer", "webshop")
	mustAppend(t, dir, other)
	res, err = RaiseLintChallenge(dir, other, now)
	if err != nil || !res.Raised {
		t.Fatalf("dedup is per actor, got %+v (%v)", res, err)
	}

	// a post without a doubt does nothing at all
	quiet := Event{TS: tick(), Repo: "webshop", Actor: "bot/builder", Msg: "order lookup by number highlights the matching row", Outcome: OutcomeOK, Mood: "good"}
	mustAppend(t, dir, quiet)
	res, err = RaiseLintChallenge(dir, quiet, now)
	if err != nil || res.Raised || res.Open || res.Doubt.Class != "" {
		t.Fatalf("a clean post must not produce a challenge, got %+v (%v)", res, err)
	}

	// a long run of doubtful posts, both classes alternating, never leaves
	// more than two open per actor
	for i := 0; i < 30; i++ {
		e := leftoverPost(tick(), "bot/loop", "api-gateway")
		if i%2 == 1 {
			e = frictionPost(now, "bot/loop", "api-gateway")
		}
		mustAppend(t, dir, e)
		if _, err := RaiseLintChallenge(dir, e, now); err != nil {
			t.Fatal(err)
		}
	}
	if open := openLint(t, dir, now, "bot/loop"); len(open) > 2 {
		t.Fatalf("30 doubtful posts left %d open lint challenges, max 2", len(open))
	}
}

// Validate forbids grades on dialogue, so the lint cannot doubt what it
// wrote: no recursion guard, just the store's own rule.
func TestLintIsInertOnItsOwnChallenges(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	post := leftoverPost(now, "bot/builder", "webshop")
	mustAppend(t, dir, post)
	res, err := RaiseLintChallenge(dir, post, now)
	if err != nil || !res.Raised {
		t.Fatal(err)
	}
	if ds := Doubts(res.Challenge); len(ds) != 0 {
		t.Fatalf("a challenge drew a doubt: %+v", ds)
	}
	again, err := RaiseLintChallenge(dir, res.Challenge, now)
	if err != nil || again.Raised || again.Open {
		t.Fatalf("the lint challenged its own challenge: %+v (%v)", again, err)
	}
}

// The dedup reads only the current month: a challenge whose target sits in
// last month's file cannot be classified and is not seen. That is the
// documented cost of never opening the archives on the post path — the
// budget of two open per actor is per month, and this pins it rather than
// letting it drift into a surprise.
func TestLintChallengeDedupSeesOnlyTheCurrentMonth(t *testing.T) {
	dir := t.TempDir()
	lastMonth := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	old := leftoverPost(lastMonth, "bot/builder", "webshop")
	mustAppend(t, dir, old)
	if res, err := RaiseLintChallenge(dir, old, lastMonth); err != nil || !res.Raised {
		t.Fatalf("last month's challenge: %+v (%v)", res, err)
	}
	fresh := leftoverPost(now, "bot/builder", "webshop")
	mustAppend(t, dir, fresh)
	res, err := RaiseLintChallenge(dir, fresh, now)
	if err != nil || !res.Raised {
		t.Fatalf("across the month boundary the class raises again, got %+v (%v)", res, err)
	}
	evs, _, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if open := OpenChallenges(evs); len(open) != 2 {
		t.Fatalf("the whole wall holds %d open, want the documented 2", len(open))
	}
}

func TestAutoChallengeSwitch(t *testing.T) {
	t.Setenv("WALLII_AUTO_CHALLENGE", "")
	if !AutoChallengeEnabled() {
		t.Error("unset must mean on")
	}
	t.Setenv("WALLII_AUTO_CHALLENGE", "OFF")
	if AutoChallengeEnabled() {
		t.Error("off must be off, whatever the case")
	}
	// a value read out of a file arrives with its newline attached, and an
	// off switch that quietly stays on is worse than none at all
	t.Setenv("WALLII_AUTO_CHALLENGE", " off\n")
	if AutoChallengeEnabled() {
		t.Error("off must be off, whatever the whitespace")
	}
}
