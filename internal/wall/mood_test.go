// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"testing"
	"time"
)

func moodEvents(moods ...string) []Event {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.Local)
	evs := make([]Event, 0, len(moods))
	for i, m := range moods {
		evs = append(evs, Event{TS: ts.Add(time.Duration(i) * time.Hour), Repo: "alpha", Actor: "worker", Msg: "work", Mood: m})
	}
	return evs
}

func TestMoodTrailOrderAndAvg(t *testing.T) {
	s := MoodTrail(moodEvents("good", "stuck", "great"))
	if s.Count != 3 || s.Total != 3 {
		t.Fatalf("count/total = %d/%d, want 3/3", s.Count, s.Total)
	}
	if want := (4.0 + 1.0 + 5.0) / 3; s.Avg != want {
		t.Errorf("avg = %.3f, want %.3f", s.Avg, want)
	}
	// oldest first: the view draws left to right in post order
	if s.Points[0].Score != 4 || s.Points[1].Score != 1 || s.Points[2].Score != 5 {
		t.Errorf("scores = %d,%d,%d, want 4,1,5", s.Points[0].Score, s.Points[1].Score, s.Points[2].Score)
	}
	for _, p := range s.Points {
		if p.N != 1 || p.Avg != float64(p.Score) {
			t.Errorf("post point = N %d, avg %.1f, want N 1 and avg == score %d", p.N, p.Avg, p.Score)
		}
	}
}

func TestMoodTrailSkipsUngradedAndReplies(t *testing.T) {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.Local)
	evs := []Event{
		{TS: ts, Repo: "alpha", Msg: "no grade"},
		{TS: ts, Repo: "alpha", Msg: "graded", Mood: "ok"},
		{TS: ts, Repo: "alpha", Msg: "attached", Kind: KindAttach},
		{TS: ts, Repo: "alpha", Msg: "answer", Kind: KindReact, Parent: "abc1234"},
	}
	s := MoodTrail(evs)
	if s.Total != 2 {
		t.Errorf("total = %d, want 2 (attach and react are not posts)", s.Total)
	}
	if s.Count != 1 || len(s.Points) != 1 {
		t.Errorf("count/points = %d/%d, want 1/1 (ungraded posts count toward total only)", s.Count, len(s.Points))
	}
}

func TestMoodSummaryCountsKeepZeros(t *testing.T) {
	s := MoodTrail(moodEvents("good", "good", "ok"))
	if len(s.Counts) != len(Moods) {
		t.Fatalf("counts = %d wide, want %d", len(s.Counts), len(Moods))
	}
	if s.Counts[1] != 2 || s.Counts[2] != 1 {
		t.Errorf("counts = %v, want good 2 / ok 1 at indexes 1,2", s.Counts)
	}
	if s.Used() != 2 {
		t.Errorf("used = %d, want 2", s.Used())
	}
	if s.Low() {
		t.Error("Low() true, but nothing below ok was ever posted")
	}
}

func TestMoodSummaryLow(t *testing.T) {
	for _, m := range []string{"rough", "stuck"} {
		if !MoodTrail(moodEvents("great", m)).Low() {
			t.Errorf("Low() false with a %q post", m)
		}
	}
}

func TestMoodTrailEmpty(t *testing.T) {
	s := MoodTrail(nil)
	if s.Count != 0 || s.Avg != 0 || s.Used() != 0 || len(s.Points) != 0 {
		t.Errorf("empty trail = %+v, want zeros", s)
	}
}

// A post whose message disagrees with its own grade is the one worth seeing.
func TestMoodTrailCarriesContradictions(t *testing.T) {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.Local)
	evs := []Event{
		{TS: ts, Repo: "alpha", Msg: "clean run", Mood: "good"},
		{TS: ts.Add(time.Hour), Repo: "alpha", Msg: "der native Pfad war eine Sackgasse, der Shim tut es", Mood: "great"},
	}
	s := MoodTrail(evs)
	if s.Contradicting != 1 {
		t.Fatalf("contradicting = %d, want 1", s.Contradicting)
	}
	if s.Points[0].Contradicts() || !s.Points[1].Contradicts() {
		t.Errorf("marks = %v,%v, want false,true", s.Points[0].Contradicts(), s.Points[1].Contradicts())
	}
}

func TestMoodLevelRoundsAndClamps(t *testing.T) {
	cases := map[float64]int{5.4: 5, 4.5: 5, 4.4: 4, 3.5: 4, 2.5: 3, 1.4: 1, 1: 1, 0: 1, -3: 1, 9: 5}
	for avg, want := range cases {
		if got := MoodLevel(avg); got != want {
			t.Errorf("MoodLevel(%.1f) = %d, want %d", avg, got, want)
		}
	}
	// the inverse of MoodScore on exact values
	for _, m := range Moods {
		if got := MoodLevel(float64(MoodScore(m))); got != MoodScore(m) {
			t.Errorf("%s: MoodLevel(%d) = %d", m, MoodScore(m), got)
		}
	}
}

func dayEvents() []Event {
	d1 := time.Date(2026, 8, 9, 9, 0, 0, 0, time.Local)
	d2 := d1.Add(24 * time.Hour)
	return []Event{
		{TS: d1, Repo: "alpha", Actor: "w", Msg: "a", Mood: "great", Outcome: OutcomeOK},
		{TS: d1.Add(time.Hour), Repo: "alpha", Actor: "w", Msg: "b", Mood: "ok", Outcome: OutcomeFailed},
		{TS: d1.Add(2 * time.Hour), Repo: "beta", Actor: "w", Msg: "c", Mood: "ok", Outcome: OutcomeOK},
		{TS: d2, Repo: "alpha", Actor: "w", Msg: "d", Mood: "good", Outcome: OutcomeOK},
	}
}

func TestMoodDaysFolds(t *testing.T) {
	days := MoodDays(MoodTrail(dayEvents()).Points)
	if len(days) != 2 {
		t.Fatalf("days = %d, want 2", len(days))
	}
	if days[0].N != 3 || days[1].N != 1 {
		t.Errorf("sizes = %d,%d, want 3,1", days[0].N, days[1].N)
	}
	if want := (5.0 + 3 + 3) / 3; days[0].Avg != want {
		t.Errorf("day avg = %.3f, want %.3f", days[0].Avg, want)
	}
	if days[0].Score != MoodLevel(days[0].Avg) {
		t.Errorf("day score %d does not match its own average %.2f", days[0].Score, days[0].Avg)
	}
	if !days[0].TS.Before(days[1].TS) {
		t.Error("days are not oldest first")
	}
}

// One failed must not vanish behind the oks it shares a day with.
func TestMoodDaysKeepTheWorstOutcome(t *testing.T) {
	days := MoodDays(MoodTrail(dayEvents()).Points)
	if days[0].Outcome != OutcomeFailed {
		t.Errorf("day outcome = %q, want %q", days[0].Outcome, OutcomeFailed)
	}
	if days[1].Outcome != OutcomeOK {
		t.Errorf("second day outcome = %q, want %q", days[1].Outcome, OutcomeOK)
	}
}

func TestMoodDaysFoldNamesOnlyWhenTheyAgree(t *testing.T) {
	days := MoodDays(MoodTrail(dayEvents()).Points)
	if days[0].Repo != "—" {
		t.Errorf("mixed day claims repo %q", days[0].Repo)
	}
	if days[1].Repo != "alpha" {
		t.Errorf("single-repo day = %q, want alpha", days[1].Repo)
	}
	if days[0].Actor != "w" {
		t.Errorf("one actor all day reads as %q", days[0].Actor)
	}
}

// A day sums its contradictions instead of flagging on any one of them:
// almost every busy day holds one mismatch, and a mark that fires on every
// column marks nothing.
func TestMoodDaysSumContradictions(t *testing.T) {
	d := time.Date(2026, 8, 9, 9, 0, 0, 0, time.Local)
	evs := []Event{
		{TS: d, Repo: "alpha", Msg: "clean", Mood: "good"},
		{TS: d.Add(time.Hour), Repo: "alpha", Msg: "war eine Sackgasse, dann ging es", Mood: "great"},
	}
	days := MoodDays(MoodTrail(evs).Points)
	if len(days) != 1 || days[0].ContraN != 1 {
		t.Errorf("a day holding a contradicting post does not carry the mark: %+v", days)
	}
}

func TestMoodActorsGroupsAndRanks(t *testing.T) {
	d := time.Date(2026, 8, 9, 9, 0, 0, 0, time.Local)
	evs := []Event{
		{TS: d, Repo: "a", Actor: "one", Msg: "x", Mood: "great"},
		{TS: d, Repo: "a", Actor: "two", Msg: "x", Mood: "ok"},
		{TS: d, Repo: "a", Actor: "one", Msg: "x", Mood: "good"},
		{TS: d, Repo: "a", Actor: "one", Msg: "x", Mood: "good"},
	}
	as := MoodActors(MoodTrail(evs).Points)
	if len(as) != 2 {
		t.Fatalf("actors = %d, want 2", len(as))
	}
	if as[0].Actor != "one" || len(as[0].Points) != 3 {
		t.Errorf("busiest = %q with %d points, want one with 3", as[0].Actor, len(as[0].Points))
	}
	if want := (5.0 + 4 + 4) / 3; as[0].Avg != want {
		t.Errorf("avg = %.3f, want %.3f", as[0].Avg, want)
	}
	if as[1].Actor != "two" || as[1].Avg != 3 {
		t.Errorf("second = %q avg %.1f, want two 3.0", as[1].Actor, as[1].Avg)
	}
}

// A folded day weighs as many posts as it holds, or a quiet day would pull
// an actor's average as hard as a busy one.
func TestMoodActorsWeighFoldedDays(t *testing.T) {
	d := time.Date(2026, 8, 9, 9, 0, 0, 0, time.Local)
	evs := []Event{
		{TS: d, Repo: "a", Actor: "one", Msg: "x", Mood: "great"},
		{TS: d, Repo: "a", Actor: "one", Msg: "x", Mood: "great"},
		{TS: d, Repo: "a", Actor: "one", Msg: "x", Mood: "great"},
		{TS: d.Add(24 * time.Hour), Repo: "a", Actor: "one", Msg: "x", Mood: "stuck"},
	}
	days := MoodDays(MoodTrail(evs).Points)
	as := MoodActors(days)
	if want := (5.0*3 + 1) / 4; as[0].Avg != want {
		t.Errorf("avg over folded days = %.3f, want %.3f", as[0].Avg, want)
	}
}

// "Any" stops meaning anything once a column folds a whole day, so the mark
// takes a majority. A single post is its own majority.
func TestMoodPointContradictsTakesAMajority(t *testing.T) {
	cases := []struct {
		contra, n int
		want      bool
	}{{0, 1, false}, {1, 1, true}, {1, 18, false}, {9, 18, false}, {10, 18, true}, {0, 18, false}}
	for _, c := range cases {
		if got := (MoodPoint{ContraN: c.contra, N: c.n}).Contradicts(); got != c.want {
			t.Errorf("%d of %d contradicting = %v, want %v", c.contra, c.n, got, c.want)
		}
	}
}
