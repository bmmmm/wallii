// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Style measurement. The wall's failure mode after the grade lints was not
// dishonesty but monoculture: every post pressed against the rune cap in the
// same telegram shape. Style is content-adjacent, so everything here follows
// the lint doctrine's hard line — observed and reported, never rejected, and
// measured on *form* signals (openings, vocabulary overlap) rather than on
// what a post says.

// tokenize lowercases and keeps letter-runs of >=3 runes: long enough to be
// words, short enough to work for German and English alike.
func tokenize(msg string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= 3 {
			out = append(out, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	for _, r := range msg {
		if unicode.IsLetter(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func tokenSet(msg string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, t := range tokenize(msg) {
		set[t] = struct{}{}
	}
	return set
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if _, ok := b[t]; ok {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// SamenessRun is the window the sameness note looks at — the same length
// that turns a grade streak into a habit.
const SamenessRun = CalibrationRun

// samenessOverlap is the median self-overlap past which recent posts read
// like drafts of one another. Posts about different work share connectives
// and repo vocabulary, so healthy walls sit well below this.
const samenessOverlap = 0.5

// Sameness reports when an actor's recent posts have collapsed into one
// shape, or "". prior is the actor's earlier posts oldest-first; e counts
// toward the window. Two form signals, no judgment on content: the opening
// token repeating, and the vocabulary overlapping. Like every grade lint it
// is a note after the fact — the post already stands exactly as written.
func Sameness(prior []Event, e Event) string {
	window := make([]Event, 0, SamenessRun)
	for i := len(prior) - 1; i >= 0 && len(window) < SamenessRun-1; i-- {
		if prior[i].Kind == "" {
			window = append(window, prior[i])
		}
	}
	if len(window) < SamenessRun-1 {
		return ""
	}
	window = append([]Event{e}, window...)

	openings := map[string]int{}
	for _, ev := range window {
		if ts := tokenize(ev.Msg); len(ts) > 0 {
			openings[ts[0]]++
		}
	}
	for word, n := range openings {
		if n >= 5 {
			return fmt.Sprintf("sameness — %d of your last %d posts open with %q. The first word is prime real estate; vary the attack.", n, len(window), word)
		}
	}

	self := tokenSet(e.Msg)
	overlaps := make([]float64, 0, len(window)-1)
	for _, ev := range window[1:] {
		overlaps = append(overlaps, jaccard(self, tokenSet(ev.Msg)))
	}
	sort.Float64s(overlaps)
	if median := overlaps[len(overlaps)/2]; median >= samenessOverlap {
		return fmt.Sprintf("sameness — this post shares most of its words with your last %d (median overlap %.0f%%). Same shape, same words: the wall stops being worth reading twice.", len(window)-1, median*100)
	}
	return ""
}

// VoiceStats is one actor's stylistic fingerprint over the window: which
// word they lean on, how often they open the same way, how wide their
// vocabulary runs. A mirror, not a grade.
type VoiceStats struct {
	Actor      string `json:"actor"`
	Posts      int    `json:"posts"`
	FavWord    string `json:"fav_word,omitempty"`
	FavCount   int    `json:"fav_count,omitempty"`
	Opening    string `json:"opening,omitempty"`
	OpeningPct int    `json:"opening_pct,omitempty"`
	Distinct   int    `json:"distinct_words"`
}

// voiceStopwords are connectives too common to be anyone's voice, in the
// wall's two languages.
var voiceStopwords = map[string]struct{}{}

func init() {
	for _, w := range []string{
		"the", "and", "for", "with", "was", "now", "not", "von", "und",
		"der", "die", "das", "den", "auf", "mit", "ist", "war", "nach",
		"aus", "als", "ein", "eine", "jetzt", "nicht", "noch", "alle",
	} {
		voiceStopwords[w] = struct{}{}
	}
}

// Voices computes per-actor style fingerprints for actors with enough posts
// to have a style (>= SamenessRun), most posts first.
func Voices(evs []Event) []VoiceStats {
	type acc struct {
		posts    int
		words    map[string]int
		openings map[string]int
	}
	byActor := map[string]*acc{}
	for _, e := range evs {
		if e.Kind != "" {
			continue
		}
		a, ok := byActor[e.Actor]
		if !ok {
			a = &acc{words: map[string]int{}, openings: map[string]int{}}
			byActor[e.Actor] = a
		}
		a.posts++
		ts := tokenize(e.Msg)
		if len(ts) > 0 {
			a.openings[ts[0]]++
		}
		seen := map[string]struct{}{}
		for _, t := range ts {
			if _, stop := voiceStopwords[t]; stop || len([]rune(t)) < 4 {
				continue
			}
			if _, dup := seen[t]; dup {
				continue // count per post, or one listy post owns the word
			}
			seen[t] = struct{}{}
			a.words[t]++
		}
	}
	var out []VoiceStats
	for actor, a := range byActor {
		if a.posts < SamenessRun {
			continue
		}
		v := VoiceStats{Actor: actor, Posts: a.posts, Distinct: len(a.words)}
		for w, n := range a.words {
			if n > v.FavCount || (n == v.FavCount && w < v.FavWord) {
				v.FavWord, v.FavCount = w, n
			}
		}
		for w, n := range a.openings {
			if n > v.OpeningPct || (n == v.OpeningPct && w < v.Opening) {
				v.Opening, v.OpeningPct = w, n
			}
		}
		if a.posts > 0 {
			v.OpeningPct = v.OpeningPct * 100 / a.posts
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Posts != out[j].Posts {
			return out[i].Posts > out[j].Posts
		}
		return out[i].Actor < out[j].Actor
	})
	return out
}
