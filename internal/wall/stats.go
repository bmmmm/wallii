// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import "sort"

// Stats aggregates regular posts (attach/detach events are registry state,
// not work) into the numbers the stats command and the digest skill read.
// Coverage counters exist because every telemetry field is optional: a
// consumer must know how much of the wall actually carries outcome/mood data
// before trusting a ratio computed from it. ByMood and TookAuto answer the
// next question down: full coverage of a single value is not a measurement
// either, and a derived duration is not one the poster stood behind.
type Stats struct {
	Posts      int `json:"posts"`
	Repos      int `json:"repos"`
	Actors     int `json:"actors"`
	OK         int `json:"ok"`
	Partial    int `json:"partial"`
	Failed     int `json:"failed"`
	Unreported int `json:"unreported"`

	MoodCount int     `json:"mood_count"`
	MoodAvg   float64 `json:"mood_avg,omitempty"` // 1 (stuck) … 5 (great)

	TookCount  int   `json:"took_count"`
	TookTotalS int64 `json:"took_total_s"`
	TookAuto   int   `json:"took_auto"` // of TookCount, derived rather than measured

	WithRefs int `json:"with_refs"`
	// Contradicting counts posts whose grade disagrees with their own
	// message. Nothing stops those from being posted — this is where they
	// surface instead.
	Contradicting int `json:"contradicting"`

	ByRepo  []NameCount  `json:"by_repo"`
	ByTopic []NameCount  `json:"by_topic"`
	ByMood  []NameCount  `json:"by_mood"`
	ByActor []ActorStats `json:"by_actor"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ActorStats is one actor's row: enough to see who does the work, whether it
// lands, and how it feels — across all repos the actor posts to.
type ActorStats struct {
	Actor     string  `json:"actor"`
	Posts     int     `json:"posts"`
	Repos     int     `json:"repos"`
	OK        int     `json:"ok"`
	Partial   int     `json:"partial"`
	Failed    int     `json:"failed"`
	MoodCount int     `json:"mood_count"`
	MoodAvg   float64 `json:"mood_avg,omitempty"`
	WithRefs  int     `json:"with_refs"`
}

// Compute folds events into Stats. Events with a Kind (attach/detach) are
// skipped; order does not matter.
func Compute(evs []Event) Stats {
	var s Stats
	repos, topics, moods := map[string]int{}, map[string]int{}, map[string]int{}
	actors := map[string]*ActorStats{}
	actorRepos := map[string]map[string]struct{}{}
	moodSum := 0
	actorMoodSum := map[string]int{}

	for _, e := range evs {
		if e.Kind != "" {
			continue
		}
		s.Posts++
		repos[e.Repo]++
		if e.Topic != "" {
			topics[e.Topic]++
		}
		a, ok := actors[e.Actor]
		if !ok {
			a = &ActorStats{Actor: e.Actor}
			actors[e.Actor] = a
			actorRepos[e.Actor] = map[string]struct{}{}
		}
		a.Posts++
		actorRepos[e.Actor][e.Repo] = struct{}{}
		switch e.Outcome {
		case OutcomeOK:
			s.OK++
			a.OK++
		case OutcomePartial:
			s.Partial++
			a.Partial++
		case OutcomeFailed:
			s.Failed++
			a.Failed++
		default:
			s.Unreported++
		}
		if sc := MoodScore(e.Mood); sc > 0 {
			s.MoodCount++
			moodSum += sc
			moods[e.Mood]++
			a.MoodCount++
			actorMoodSum[e.Actor] += sc
		}
		if e.TookS > 0 {
			s.TookCount++
			s.TookTotalS += e.TookS
			if e.TookSrc == TookAuto {
				s.TookAuto++
			}
		}
		if len(e.Refs) > 0 {
			s.WithRefs++
			a.WithRefs++
		}
		if len(Contradictions(e)) > 0 {
			s.Contradicting++
		}
	}

	s.Repos = len(repos)
	s.Actors = len(actors)
	if s.MoodCount > 0 {
		s.MoodAvg = float64(moodSum) / float64(s.MoodCount)
	}
	s.ByRepo = sortedCounts(repos)
	s.ByTopic = sortedCounts(topics)
	// mood keeps its scale order (great → stuck) rather than count order: the
	// point of the breakdown is which part of the range never gets used
	for _, m := range Moods {
		if n := moods[m]; n > 0 {
			s.ByMood = append(s.ByMood, NameCount{m, n})
		}
	}
	for name, a := range actors {
		a.Repos = len(actorRepos[name])
		if a.MoodCount > 0 {
			a.MoodAvg = float64(actorMoodSum[name]) / float64(a.MoodCount)
		}
		s.ByActor = append(s.ByActor, *a)
	}
	sort.Slice(s.ByActor, func(i, j int) bool {
		if s.ByActor[i].Posts != s.ByActor[j].Posts {
			return s.ByActor[i].Posts > s.ByActor[j].Posts
		}
		return s.ByActor[i].Actor < s.ByActor[j].Actor
	})
	return s
}

func sortedCounts(m map[string]int) []NameCount {
	out := make([]NameCount, 0, len(m))
	for n, c := range m {
		out = append(out, NameCount{n, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}
