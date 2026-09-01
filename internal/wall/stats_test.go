// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"testing"
	"time"
)

func statsEvents() []Event {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	return []Event{
		{TS: ts, Repo: "alpha", Actor: "worker", Topic: "ci", Msg: "green", Outcome: OutcomeOK, Mood: "good", TookS: 600, Refs: []string{"https://x.example/1"}},
		{TS: ts, Repo: "alpha", Actor: "worker", Topic: "ci", Msg: "red", Outcome: OutcomeFailed, Mood: "stuck"},
		{TS: ts, Repo: "beta", Actor: "worker", Topic: "release", Msg: "shipped", Outcome: OutcomeOK},
		{TS: ts, Repo: "beta", Actor: "manual", Msg: "note"},
		{TS: ts, Repo: "beta", Actor: "manual", Msg: "second note"},
		{TS: ts, Repo: "beta", Actor: "manual", Kind: KindAttach, Msg: "attached"},
	}
}

func TestComputeCounts(t *testing.T) {
	s := Compute(statsEvents())
	if s.Posts != 5 {
		t.Fatalf("posts = %d, want 5 (attach events must not count)", s.Posts)
	}
	if s.Repos != 2 || s.Actors != 2 {
		t.Errorf("repos/actors = %d/%d, want 2/2", s.Repos, s.Actors)
	}
	if s.OK != 2 || s.Partial != 0 || s.Failed != 1 || s.Unreported != 2 {
		t.Errorf("outcome = ok %d partial %d failed %d unreported %d, want 2/0/1/2", s.OK, s.Partial, s.Failed, s.Unreported)
	}
	if s.WithRefs != 1 || s.TookCount != 1 || s.TookTotalS != 600 {
		t.Errorf("refs/took = %d refs, %d took, %ds total", s.WithRefs, s.TookCount, s.TookTotalS)
	}
}

func TestComputeMoodAvg(t *testing.T) {
	s := Compute(statsEvents())
	// good=4, stuck=1 → avg 2.5 over 2 mood-carrying posts
	if s.MoodCount != 2 || s.MoodAvg != 2.5 {
		t.Fatalf("mood = avg %.2f from %d, want 2.50 from 2", s.MoodAvg, s.MoodCount)
	}
}

func TestComputeSortsByCount(t *testing.T) {
	s := Compute(statsEvents())
	if len(s.ByRepo) != 2 || s.ByRepo[0].Name != "beta" || s.ByRepo[0].Count != 3 {
		t.Fatalf("by_repo = %+v, want beta(3) first", s.ByRepo)
	}
	if len(s.ByActor) != 2 || s.ByActor[0].Actor != "worker" || s.ByActor[0].Repos != 2 {
		t.Fatalf("by_actor = %+v, want worker first with 2 repos", s.ByActor)
	}
}

// The lint's challenges are dialogue the wall had with itself. They are
// counted apart, and never into "most challenged" — the lint doubts whoever
// posts most, so the title would go to the busiest actor.
func TestStatsSeparatesLintChallenges(t *testing.T) {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	a := Event{TS: ts, Repo: "webshop", Actor: "bot/builder", Msg: "cache layer landed, invalidation not yet wired", Outcome: OutcomeOK}
	b := Event{TS: ts.Add(time.Minute), Repo: "webshop", Actor: "bot/reviewer", Msg: "deploy rolled back, the health check never went green", Mood: "good"}
	lintA := Event{TS: ts.Add(2 * time.Minute), Repo: "webshop", Actor: LintActor, Kind: KindChallenge, Parent: a.ID(), Msg: Doubts(a)[0].Ask}
	lintB := Event{TS: ts.Add(3 * time.Minute), Repo: "webshop", Actor: LintActor, Kind: KindChallenge, Parent: b.ID(), Msg: Doubts(b)[0].Ask}

	s := Compute([]Event{a, b, lintA, lintB})
	if s.Posts != 2 || s.Challenges != 2 || s.ChallengesAuto != 2 || s.ChallengesOpen != 2 {
		t.Fatalf("counts wrong: %+v", s)
	}
	if len(s.ByChallenged) != 0 {
		t.Fatalf("lint challenges must not crown a most-challenged actor, got %+v", s.ByChallenged)
	}

	critic := Event{TS: ts.Add(4 * time.Minute), Repo: "webshop", Actor: "bot/critic", Kind: KindChallenge, Parent: a.ID(), Msg: "which gate ran?"}
	s = Compute([]Event{a, b, lintA, lintB, critic})
	if s.Challenges != 3 || s.ChallengesAuto != 2 {
		t.Fatalf("an agent's challenge must count apart from the lint's: %+v", s)
	}
	if len(s.ByChallenged) != 1 || s.ByChallenged[0].Name != "bot/builder" || s.ByChallenged[0].Count != 1 {
		t.Fatalf("most challenged must come from agents only, got %+v", s.ByChallenged)
	}
}

func TestMoodScore(t *testing.T) {
	if MoodScore("great") != 5 || MoodScore("stuck") != 1 || MoodScore("") != 0 || MoodScore("meh") != 0 {
		t.Fatal("mood score mapping broken")
	}
}

func TestValidateOutcomeMoodTook(t *testing.T) {
	e := statsEvents()[0]
	if err := e.Validate(); err != nil {
		t.Fatalf("valid telemetry event rejected: %v", err)
	}
	bad := []func(*Event){
		func(e *Event) { e.Outcome = "success" },
		func(e *Event) { e.Mood = "fantastic" },
		func(e *Event) { e.TookS = -1 },
		func(e *Event) { e.TookS = 400 * 24 * 3600 },
	}
	for i, mut := range bad {
		e := statsEvents()[0]
		mut(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("case %d: invalid telemetry accepted", i)
		}
	}
}
