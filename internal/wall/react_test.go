// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"strings"
	"testing"
	"time"
)

func mkPost(ts time.Time, actor, repo, msg string) Event {
	return Event{TS: ts, Actor: actor, Repo: repo, Msg: msg}
}

func TestEventIDStableAndDistinct(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	a := mkPost(ts, "bot/one", "demo", "the cache invalidation was the cache all along")
	if a.ID() != a.ID() {
		t.Fatal("ID must be deterministic")
	}
	if len(a.ID()) != 7 || strings.TrimLeft(a.ID(), "0123456789abcdef") != "" {
		t.Fatalf("ID %q is not 7 lowercase hex chars", a.ID())
	}
	b := mkPost(ts, "bot/two", "demo", "the cache invalidation was the cache all along")
	if a.ID() == b.ID() {
		t.Fatal("different actors, same second must not collide")
	}
}

func TestFindByID(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	evs := []Event{
		mkPost(ts, "bot/one", "demo", "first"),
		mkPost(ts.Add(time.Minute), "bot/one", "demo", "second"),
	}
	got, err := FindByID(evs, evs[1].ID()[:4])
	if err != nil {
		t.Fatalf("prefix lookup: %v", err)
	}
	if got.Msg != "second" {
		t.Fatalf("resolved wrong event: %q", got.Msg)
	}
	if _, err := FindByID(evs, "ffffff"); err == nil {
		t.Fatal("unknown ID must error, not guess")
	}
	if _, err := FindByID(evs, "ab"); err == nil {
		t.Fatal("a 2-char prefix must be rejected as too short")
	}
}

func TestReplyValidation(t *testing.T) {
	ts := time.Now().UTC()
	// red: a reply without a parent is unanchored
	r := Event{TS: ts, Repo: "demo", Actor: "bot", Kind: KindReact, Msg: "says who?"}
	if err := r.Validate(); err == nil {
		t.Fatal("react without parent must be rejected")
	}
	// red: dialogue must not smuggle in grades
	r = Event{TS: ts, Repo: "demo", Actor: "bot", Kind: KindChallenge, Parent: "abcd123", Msg: "prove it", Outcome: OutcomeOK}
	if err := r.Validate(); err == nil {
		t.Fatal("a graded challenge must be rejected — dialogue is not telemetry")
	}
	// red: a plain post cannot claim a parent
	p := mkPost(ts, "bot", "demo", "regular work")
	p.Parent = "abcd123"
	if err := p.Validate(); err == nil {
		t.Fatal("parent on a plain post must be rejected")
	}
	// green: a well-formed challenge passes
	c := Event{TS: ts, Repo: "demo", Actor: "bot", Kind: KindChallenge, Parent: "abcd123", Msg: "gates green — which gate ran?"}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid challenge rejected: %v", err)
	}
}

func TestOpenChallenges(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	post := mkPost(ts, "bot/builder", "demo", "all gates green, shipped")
	doubt := Event{TS: ts.Add(time.Hour), Repo: "demo", Actor: "bot/critic", Kind: KindChallenge,
		Parent: post.ID(), Msg: "which gate ran? CI shows no run"}

	open := OpenChallenges([]Event{post, doubt})
	if len(open) != 1 || open[0].Challenge.Msg != doubt.Msg || !open[0].HasTarget {
		t.Fatalf("expected exactly the one open challenge with its target, got %+v", open)
	}

	// answered directly: react to the challenge itself, by anyone
	answer := Event{TS: ts.Add(2 * time.Hour), Repo: "demo", Actor: "bot/builder", Kind: KindReact,
		Parent: doubt.ID(), Msg: "gate 3, run 4711 — link attached"}
	if open := OpenChallenges([]Event{post, doubt, answer}); len(open) != 0 {
		t.Fatalf("answered challenge must close, still open: %+v", open)
	}

	// answered via the thread: the challenged actor reacts to their own post
	// after the challenge was raised
	threadAnswer := Event{TS: ts.Add(2 * time.Hour), Repo: "demo", Actor: "bot/builder", Kind: KindReact,
		Parent: post.ID(), Msg: "context: gate 3 ran in CI"}
	if open := OpenChallenges([]Event{post, doubt, threadAnswer}); len(open) != 0 {
		t.Fatalf("thread answer by the challenged actor must close, still open: %+v", open)
	}

	// NOT answered: someone else piling onto the post is not the challenged
	// actor responding
	bystander := Event{TS: ts.Add(2 * time.Hour), Repo: "demo", Actor: "bot/other", Kind: KindReact,
		Parent: post.ID(), Msg: "same question"}
	if open := OpenChallenges([]Event{post, doubt, bystander}); len(open) != 1 {
		t.Fatalf("a bystander react must not close the challenge, got %+v", open)
	}

	// NOT answered: the challenged actor's react from BEFORE the challenge
	early := Event{TS: ts.Add(30 * time.Minute), Repo: "demo", Actor: "bot/builder", Kind: KindReact,
		Parent: post.ID(), Msg: "self-note"}
	if open := OpenChallenges([]Event{post, early, doubt}); len(open) != 1 {
		t.Fatalf("a react older than the challenge must not close it, got %+v", open)
	}
}

func TestStatsCountDialogue(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	post := mkPost(ts, "bot/builder", "demo", "shipped")
	post.Outcome = OutcomeOK
	doubt := Event{TS: ts.Add(time.Hour), Repo: "demo", Actor: "bot/critic", Kind: KindChallenge,
		Parent: post.ID(), Msg: "proof?"}
	nod := Event{TS: ts.Add(time.Hour), Repo: "demo", Actor: "bot/other", Kind: KindReact,
		Parent: post.ID(), Msg: "nice"}

	s := Compute([]Event{post, doubt, nod})
	if s.Posts != 1 {
		t.Fatalf("dialogue must not count as posts, got %d", s.Posts)
	}
	if s.Reactions != 1 || s.Challenges != 1 || s.ChallengesOpen != 1 {
		t.Fatalf("dialogue counts wrong: %+v", s)
	}
	if len(s.ByChallenged) != 1 || s.ByChallenged[0].Name != "bot/builder" {
		t.Fatalf("most-challenged must name the builder, got %+v", s.ByChallenged)
	}
}
