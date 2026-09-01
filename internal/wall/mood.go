// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"sort"
	"time"
)

// MoodPoint is one column on the mood timeline: a single graded post, or —
// at day resolution — every graded post of one day folded into one.
type MoodPoint struct {
	TS      time.Time
	Score   int     // 1 (stuck) … 5 (great): the level the curve draws it at
	Avg     float64 // the exact average behind Score; equals Score for a post
	Mood    string  // the post's own value; empty on a folded day
	Repo    string
	Actor   string
	Topic   string
	Msg     string
	Outcome string // ok | partial | failed | none — the worst one when folded
	// ContraN counts the posts here whose message disagrees with their own
	// grade — the finding stats counts, carried into the curve so the honest
	// posts are visible in the picture and not only in a number.
	ContraN int
	N       int // posts folded here: 1 for a post, more for a day
	// The conditions the grade was earned under: what the API answered in
	// (the mean over the posts of a folded day that carry a reading), how
	// many carry one, and how many were written while nothing answered.
	PulseMS   int64
	PulseN    int
	PulseDown int
}

// Contradicts reports whether this column should be drawn as a doubted one.
// A count rather than a flag, because "any" stops meaning anything once a
// column folds a whole day: almost every busy day holds one mismatch, and a
// mark that fires on every column marks nothing. A majority does say
// something — that day's grades mostly disagreed with their own messages.
func (p MoodPoint) Contradicts() bool { return p.ContraN*2 > p.N }

// MoodSummary is everything a mood view needs: the points to draw, and the
// coverage numbers that say how much of the wall they speak for. Total counts
// every post, graded or not — a series of 12 over 400 posts is a sample, not
// a measurement, and the view has to be able to say so.
type MoodSummary struct {
	Points []MoodPoint // graded posts, oldest first
	Count  int         // graded posts
	Total  int         // posts seen, graded or not
	Avg    float64     // 1 … 5 over all graded posts, 0 when Count is 0
	// Counts indexes Moods: great … stuck. Zeros are kept — that a value is
	// never used is the finding, so it must survive into the view.
	Counts []int
	// Contradicting counts points whose grade disagrees with their message.
	Contradicting int
}

// MoodLevel rounds an average onto the 5..1 scale, clamped: the level a curve
// draws an aggregate at. MoodScore's inverse for averages, and the only place
// that rounding happens — so the word, the face and the curve cannot disagree.
func MoodLevel(avg float64) int {
	l := int(avg + 0.5)
	if l < 1 {
		return 1
	}
	if l > len(Moods) {
		return len(Moods)
	}
	return l
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

// MoodTrail folds events into a MoodSummary. Replies carry no grades, so
// attach/detach and react/challenge events are skipped the way Compute skips
// them. Every point is kept: the view's time window is the only limit on how
// far back it reaches, and a limit the reader cannot see is one they cannot
// correct for.
func MoodTrail(evs []Event) MoodSummary {
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
		p := MoodPoint{TS: e.TS, Score: sc, Avg: float64(sc), Mood: e.Mood, Repo: e.Repo,
			Actor: e.Actor, Topic: e.Topic, Msg: e.Msg, Outcome: e.Outcome, N: 1}
		switch e.PulseSrc {
		case "": // nobody measured — not the same as nothing answering
		case PulseNone:
			p.PulseDown = 1
		default:
			p.PulseMS, p.PulseN = e.PulseMS, 1
		}
		// regex work, once per post per refold — the trail is rebuilt on
		// ingest, not per frame, so this stays off the render path
		if len(Contradictions(e)) > 0 {
			p.ContraN = 1
			s.Contradicting++
		}
		s.Points = append(s.Points, p)
	}
	if s.Count > 0 {
		s.Avg = float64(sum) / float64(s.Count)
	}
	return s
}

// MoodDays folds points into one per local calendar day, oldest first. The
// day's outcome is the worst one in it — a single failed must not disappear
// behind twenty oks — while its contradictions add up, so a day is doubted
// only when most of it was (see MoodPoint.Contradicts).
func MoodDays(pts []MoodPoint) []MoodPoint {
	var out []MoodPoint
	sum := make(map[int]float64)
	psum := make(map[int]int64)
	for _, p := range pts {
		d := p.TS.Local()
		day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
		i := len(out) - 1
		if i < 0 || !out[i].TS.Equal(day) {
			out = append(out, MoodPoint{TS: day})
			i = len(out) - 1
		}
		out[i].N += p.N
		sum[i] += p.Avg * float64(p.N)
		out[i].ContraN += p.ContraN
		psum[i] += p.PulseMS * int64(p.PulseN)
		out[i].PulseN += p.PulseN
		out[i].PulseDown += p.PulseDown
		if outcomeRank(p.Outcome) > outcomeRank(out[i].Outcome) {
			out[i].Outcome = p.Outcome
		}
		out[i].Repo, out[i].Actor = foldName(out[i].Repo, p.Repo), foldName(out[i].Actor, p.Actor)
	}
	for i := range out {
		out[i].Avg = sum[i] / float64(out[i].N)
		out[i].Score = MoodLevel(out[i].Avg)
		if out[i].PulseN > 0 {
			out[i].PulseMS = psum[i] / int64(out[i].PulseN)
		}
	}
	return out
}

// foldName keeps a name while every point folded so far agrees on it, and
// blanks it once they do not — a day spent in one repo says so, a day across
// four does not claim one of them.
func foldName(have, next string) string {
	if have == "" {
		return next
	}
	if have != next {
		return "—"
	}
	return have
}

// outcomeRank orders outcomes by how much they should survive aggregation:
// failed outranks partial outranks ok outranks nothing reported.
func outcomeRank(o string) int {
	switch o {
	case OutcomeFailed:
		return 3
	case OutcomePartial:
		return 2
	case OutcomeOK:
		return 1
	}
	return 0
}

// ActorMood is one actor's line in the population view — the mirror against
// monoculture that stats draws in words, drawn as a shape instead.
type ActorMood struct {
	Actor  string
	Points []MoodPoint
	Avg    float64
}

// MoodActors groups points by actor, busiest first.
func MoodActors(pts []MoodPoint) []ActorMood {
	idx := map[string]int{}
	var out []ActorMood
	sum := map[string]float64{}
	for _, p := range pts {
		i, ok := idx[p.Actor]
		if !ok {
			i = len(out)
			idx[p.Actor] = i
			out = append(out, ActorMood{Actor: p.Actor})
		}
		out[i].Points = append(out[i].Points, p)
		sum[p.Actor] += p.Avg * float64(p.N)
	}
	for i := range out {
		n := 0
		for _, p := range out[i].Points {
			n += p.N
		}
		out[i].Avg = sum[out[i].Actor] / float64(n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Points) != len(out[j].Points) {
			return len(out[i].Points) > len(out[j].Points)
		}
		return out[i].Actor < out[j].Actor
	})
	return out
}
