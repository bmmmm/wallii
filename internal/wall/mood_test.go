// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"testing"
	"time"
)

func moodEvents(moods ...string) []Event {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	evs := make([]Event, 0, len(moods))
	for i, m := range moods {
		evs = append(evs, Event{TS: ts.Add(time.Duration(i) * time.Hour), Repo: "alpha", Actor: "worker", Msg: "work", Mood: m})
	}
	return evs
}

func TestMoodTrailOrderAndAvg(t *testing.T) {
	s := MoodTrail(moodEvents("good", "stuck", "great"), 0)
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
}

func TestMoodTrailSkipsUngradedAndReplies(t *testing.T) {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	evs := []Event{
		{TS: ts, Repo: "alpha", Msg: "no grade"},
		{TS: ts, Repo: "alpha", Msg: "graded", Mood: "ok"},
		{TS: ts, Repo: "alpha", Msg: "attached", Kind: KindAttach},
		{TS: ts, Repo: "alpha", Msg: "answer", Kind: KindReact, Parent: "abc1234"},
	}
	s := MoodTrail(evs, 0)
	if s.Total != 2 {
		t.Errorf("total = %d, want 2 (attach and react are not posts)", s.Total)
	}
	if s.Count != 1 || len(s.Points) != 1 {
		t.Errorf("count/points = %d/%d, want 1/1 (ungraded posts count toward total only)", s.Count, len(s.Points))
	}
}

// keep bounds what the view draws without touching what it reports: the
// header still says "of 5 posts" when only the last 2 fit on screen.
func TestMoodTrailKeepBoundsPointsNotCounts(t *testing.T) {
	s := MoodTrail(moodEvents("stuck", "rough", "ok", "good", "great"), 2)
	if len(s.Points) != 2 {
		t.Fatalf("points = %d, want 2", len(s.Points))
	}
	if s.Points[0].Mood != "good" || s.Points[1].Mood != "great" {
		t.Errorf("kept %q,%q, want the last two (good,great)", s.Points[0].Mood, s.Points[1].Mood)
	}
	if s.Count != 5 || s.Avg != 3 {
		t.Errorf("count/avg = %d/%.1f, want 5/3.0 — keep must not skew the summary", s.Count, s.Avg)
	}
}

func TestMoodSummaryCountsKeepZeros(t *testing.T) {
	s := MoodTrail(moodEvents("good", "good", "ok"), 0)
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
		if !MoodTrail(moodEvents("great", m), 0).Low() {
			t.Errorf("Low() false with a %q post", m)
		}
	}
}

func TestMoodTrailEmpty(t *testing.T) {
	s := MoodTrail(nil, 0)
	if s.Count != 0 || s.Avg != 0 || s.Used() != 0 || len(s.Points) != 0 {
		t.Errorf("empty trail = %+v, want zeros", s)
	}
}
