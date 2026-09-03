// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"sort"
	"strings"
)

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

	// Pulse: the conditions the grades were earned under. Turns are what a
	// turn actually cost, pings only prove the API was reachable, and Down is
	// an outage — three different findings, so three counters. A post with no
	// pulse at all is none of them: nobody measured, which must never read as
	// an outage.
	PulseTurns       int   `json:"pulse_turns,omitempty"`
	PulseTurnTotalMS int64 `json:"pulse_turn_total_ms,omitempty"`
	PulsePings       int   `json:"pulse_pings,omitempty"`
	PulseDown        int   `json:"pulse_down,omitempty"`

	// Squeeze: the other half of those conditions — how full the account's
	// rate limits were while these grades were earned. Sums, so the caller
	// divides by the count it prints beside them and cannot quietly average
	// over posts nobody measured: a post with no reading is not a post with
	// an empty budget. Reported and never computed with — no coverage
	// percentage, no per-actor split, and nothing on the wall moves because
	// of them.
	SqueezePosts   int     `json:"squeeze_posts,omitempty"`
	SqueezePTotal  float64 `json:"squeeze_p_total,omitempty"`
	Squeeze5hTotal float64 `json:"squeeze_5h_total,omitempty"`

	WithRefs int `json:"with_refs"`
	// Contradicting counts posts whose grade disagrees with their own
	// message. Nothing stops those from being posted — this is where they
	// surface instead.
	Contradicting int `json:"contradicting"`

	// Grader: how many posts name the cheap path they saw, and in how many
	// different words. Counted, never graded — a fraction with the distinct
	// count beside it is a description, a percentage would be a dial. The
	// distinct count is what makes the filler move visible: "--grader none"
	// on every post reads 483/483 · 1 distinct, and nobody can average that
	// into a good score. Nothing here reads the text; the same idiom as
	// Voice, presence and variety only.
	WithGrader     int `json:"with_grader"`
	GraderDistinct int `json:"grader_distinct"`

	// Signals: the measurement beside the report. SignalsMeasured counts the
	// posts whose session the Stop hook scanned at all — coverage, and a
	// property of posts. The other two are not: signals hang on every post
	// of a session, so counting posts would report one shortcut named once
	// in a three-post session as one named and two unnamed. SignalsShown
	// therefore counts distinct shortcuts — one per (repo, line the diff
	// showed) — and SignalsNamed those of them some post carrying that line
	// answered with a grader. Naming a shortcut once is naming it; the next
	// post of the same session owes nothing further. The difference is the
	// finding — measurement against self-report, the same idiom as mood
	// against the message one level down — and it is reported, never
	// computed with: no percentage, no per-actor split, no challenge raised
	// from it. A signal without a grader is often entirely fine (the hook's
	// environment-guard filter is good, not perfect), and nobody owes a
	// counter an explanation.
	SignalsMeasured int `json:"signals_measured,omitempty"`
	SignalsShown    int `json:"signals_shown,omitempty"`
	SignalsNamed    int `json:"signals_named,omitempty"`

	// Dialogue: reactions and challenges are replies, not work, so they stay
	// out of Posts — but a wall where they are zero is a wall nobody reads.
	// ChallengesAuto is how many of Challenges the lint raised (LintActor):
	// a wall that only ever talked to itself must not read as a wall that
	// talks back, so consumers subtract it before crediting any dialogue.
	// ChallengesOpenAuto splits the open ones the same way, because the
	// digest is told to report "the lint doubts N grades, M still open" and
	// M is otherwise not derivable: ChallengesOpen mixes both kinds, and
	// the difference between a machine waiting for an answer and a
	// colleague waiting for one is the whole point of keeping them apart.
	Reactions          int         `json:"reactions,omitempty"`
	Challenges         int         `json:"challenges,omitempty"`
	ChallengesAuto     int         `json:"challenges_auto,omitempty"`
	ChallengesOpen     int         `json:"challenges_open,omitempty"`
	ChallengesOpenAuto int         `json:"challenges_open_auto,omitempty"`
	ByChallenged       []NameCount `json:"by_challenged,omitempty"`

	// Voice: per-actor style fingerprints — the population's mirror against
	// monoculture. Only actors with enough posts to have a style appear.
	Voice []VoiceStats `json:"voice,omitempty"`

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

// signalKey identifies one shortcut the diff showed: the line, in the repo
// it was shown in. The same `t.Skip(...)` in two repos is two shortcuts;
// the same line seen by three posts of one session is one.
type signalKey struct{ repo, line string }

// Compute folds events into Stats. Events with a Kind (attach/detach) are
// skipped; order does not matter.
func Compute(evs []Event) Stats {
	var s Stats
	repos, topics, moods := map[string]int{}, map[string]int{}, map[string]int{}
	actors := map[string]*ActorStats{}
	actorRepos := map[string]map[string]struct{}{}
	moodSum := 0
	actorMoodSum := map[string]int{}
	// case-folded and trimmed, so "None" and "none " are the same sentence
	// said twice, not two ways of saying it
	graders := map[string]struct{}{}
	signals := map[signalKey]bool{}

	// id → actor of the challenged event, so ByChallenged can name whose
	// posts draw doubt (the parent may be any kind, including a reply)
	actorByID := map[string]string{}
	for _, e := range evs {
		actorByID[e.ID()] = e.Actor
	}
	challenged := map[string]int{}

	for _, e := range evs {
		switch e.Kind {
		case KindReact:
			s.Reactions++
			continue
		case KindChallenge:
			s.Challenges++
			if e.Actor == LintActor {
				// the lint doubts whoever posts most, so counting it into
				// "most challenged" would hand the title to the busiest actor
				s.ChallengesAuto++
				continue
			}
			if a, ok := actorByID[e.Parent]; ok {
				challenged[a]++
			}
			continue
		}
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
		switch e.PulseSrc {
		case "": // nobody measured
		case PulseNone:
			s.PulseDown++
		case PulseProbe:
			s.PulsePings++
		default:
			s.PulseTurns++
			s.PulseTurnTotalMS += e.PulseMS
		}
		if e.SqueezeSrc != "" {
			s.SqueezePosts++
			s.SqueezePTotal += e.SqueezeP
			s.Squeeze5hTotal += e.Squeeze5h
		}
		if len(e.Refs) > 0 {
			s.WithRefs++
			a.WithRefs++
		}
		if len(Contradictions(e)) > 0 {
			s.Contradicting++
		}
		if g := strings.TrimSpace(e.Grader); g != "" {
			s.WithGrader++
			graders[strings.ToLower(g)] = struct{}{}
		}
		if e.SignalSrc != "" {
			s.SignalsMeasured++
		}
		named := strings.TrimSpace(e.Grader) != ""
		for _, sig := range e.Signals {
			k := signalKey{repo: e.Repo, line: sig}
			signals[k] = signals[k] || named
		}
	}
	s.GraderDistinct = len(graders)
	s.SignalsShown = len(signals)
	for _, named := range signals {
		if named {
			s.SignalsNamed++
		}
	}

	if s.Challenges > 0 {
		open := OpenChallenges(evs)
		s.ChallengesOpen = len(open)
		for _, oc := range open {
			if oc.Challenge.Actor == LintActor {
				s.ChallengesOpenAuto++
			}
		}
		s.ByChallenged = sortedCounts(challenged)
	}
	s.Voice = Voices(evs)
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
