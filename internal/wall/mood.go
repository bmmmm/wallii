// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import "time"

// MoodPoint is one graded post on the timeline. The mood series is the
// sequence of them in post order — a wall read as a line rather than as an
// average, because an average hides the shape: three stuck stretches
// followed by three great ones score the same as an unbroken run of ok.
type MoodPoint struct {
	TS    time.Time
	Score int // 1 (stuck) … 5 (great)
	Mood  string
	Repo  string
	Actor string
}

// MoodSummary is everything a mood view needs: the recent points to draw,
// and the coverage numbers that say how much of the wall they speak for.
// Total counts every post, graded or not — a series of 12 over 400 posts is
// a sample, not a measurement, and the view has to be able to say so.
type MoodSummary struct {
	Points []MoodPoint // graded posts, oldest first, at most keep of them
	Count  int         // graded posts seen (may exceed len(Points))
	Total  int         // posts seen, graded or not
	Avg    float64     // 1 … 5 over all graded posts, 0 when Count is 0
	// Counts indexes Moods: great … stuck. Zeros are kept — that a value is
	// never used is the finding, so it must survive into the view.
	Counts []int
}

// Used counts how many of the five values ever appear. One value over
// hundreds of posts is a flat line, not a reading.
func (s MoodSummary) Used() int {
	n := 0
	for _, c := range s.Counts {
		if c > 0 {
			n++
		}
	}
	return n
}

// Low reports whether the scale's bottom half was ever reached. A wall that
// never goes below ok is either a lucky one or a polite one, and only the
// poster knows which.
func (s MoodSummary) Low() bool {
	for i, c := range s.Counts {
		if c > 0 && len(Moods)-i <= 2 { // rough, stuck
			return true
		}
	}
	return false
}

// MoodTrail folds events into a MoodSummary, keeping at most the last keep
// points (keep <= 0 keeps all). Replies carry no grades, so attach/detach
// and react/challenge events are skipped the way Compute skips them.
func MoodTrail(evs []Event, keep int) MoodSummary {
	s := MoodSummary{Counts: make([]int, len(Moods))}
	sum := 0
	for _, e := range evs {
		if e.Kind != "" {
			continue
		}
		s.Total++
		sc := MoodScore(e.Mood)
		if sc == 0 {
			continue
		}
		s.Count++
		sum += sc
		s.Counts[len(Moods)-sc]++
		s.Points = append(s.Points, MoodPoint{TS: e.TS, Score: sc, Mood: e.Mood, Repo: e.Repo, Actor: e.Actor})
		if keep > 0 && len(s.Points) > keep {
			s.Points = s.Points[len(s.Points)-keep:]
		}
	}
	if s.Count > 0 {
		s.Avg = float64(sum) / float64(s.Count)
	}
	return s
}
