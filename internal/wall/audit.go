// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"sort"
	"strings"
	"time"
)

// Post-hoc audit. Landed-% is self-reported at the moment of maximum
// optimism; whether an ok *held* only shows later, when the same ground gets
// fixed again. Every fix indicts an earlier ok — mechanically, from the
// wall's own record, with no judgment call anywhere.

// hauntProximity is how long an ok stays answerable for a follow-up fix.
// Longer gaps are ordinary life: software decays, and a fix a month later
// says nothing about the grade.
const hauntProximity = 7 * 24 * time.Hour

// hauntMinShared is how many significant words a fix must share with an
// earlier ok before the two count as the same ground. One shared word is a
// coincidence in a repo's shared vocabulary; two start to be a place.
const hauntMinShared = 2

// Haunting pairs an ok-graded post with a later fix on the same ground.
type Haunting struct {
	OK     Event    `json:"ok"`
	Fix    Event    `json:"fix"`
	Shared []string `json:"shared"`
}

// hauntNoise is process vocabulary that names how work was done, not where:
// every post on this wall talks about tests, rounds and proofs, so sharing
// those words is not sharing ground. First run without this list paired
// posts on "erster, nicht" and "proven, tests".
var hauntNoise = map[string]struct{}{}

func init() {
	for _, w := range []string{
		"tests", "test", "checks", "check", "proven", "proof", "round",
		"rounds", "fixed", "fixes", "gefixt", "findings", "gruen", "green",
		"jetzt", "nicht", "erster", "never", "whole", "after", "bewiesen",
		"geprueft", "gepruft", "live", "echte", "echten", "issue", "closed",
	} {
		hauntNoise[w] = struct{}{}
	}
}

// hauntTokens keeps the words specific enough to name a place in the code:
// >=5 runes, minus the repo's own name (shared by definition) and the
// process vocabulary every post shares anyway.
func hauntTokens(e Event) map[string]struct{} {
	out := map[string]struct{}{}
	repo := strings.ToLower(e.Repo)
	for _, t := range tokenize(e.Msg) {
		if len([]rune(t)) < 5 || t == repo {
			continue
		}
		if _, noisy := hauntNoise[t]; noisy {
			continue
		}
		out[t] = struct{}{}
	}
	return out
}

// Hauntings finds ok posts that a later fix in the same repo revisited
// within hauntProximity, sharing at least hauntMinShared significant words.
// evs oldest-first; each ok is reported at most once, against its earliest
// haunting fix.
func Hauntings(evs []Event) []Haunting {
	var out []Haunting
	for i, p := range evs {
		if p.Kind != "" || p.Outcome != OutcomeOK {
			continue
		}
		pt := hauntTokens(p)
		if len(pt) == 0 {
			continue
		}
		for _, q := range evs[i+1:] {
			if q.Kind != "" || q.Topic != "fix" || q.Repo != p.Repo {
				continue
			}
			if q.TS.Sub(p.TS) > hauntProximity {
				break // evs is oldest-first: every later q is further away
			}
			var shared []string
			for t := range hauntTokens(q) {
				if _, ok := pt[t]; ok {
					shared = append(shared, t)
				}
			}
			if len(shared) >= hauntMinShared {
				sort.Strings(shared)
				out = append(out, Haunting{OK: p, Fix: q, Shared: shared})
				break
			}
		}
	}
	return out
}
