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
// Measured says the ok carried a shortcut signature the Stop hook found in
// the session's diff (Signals). It is the strongest pairing the wall has,
// and it is still not proof that THIS skip was the gap: signals attach to
// every post of a session, so the line the diff showed may sit in a file
// this post never touched. What is measured is that the session behind this
// ok carried a shortcut and the ok did not hold — two records, neither
// asked of anyone, and no claim about which caused which.
type Haunting struct {
	OK       Event    `json:"ok"`
	Fix      Event    `json:"fix"`
	Shared   []string `json:"shared"`
	Measured bool     `json:"measured,omitempty"`
	// Cost is what each side of the pair cost, present only when both were
	// measured: the ok that did not hold, and the fix that had to follow.
	Cost *PairCost `json:"unit_cost,omitempty"`
}

// PairCost is the two unit readings of a haunted pair.
type PairCost struct {
	OK  Unit `json:"ok"`
	Fix Unit `json:"fix"`
}

// AuditSummary is what the window's oks add up to once Hauntings has paired
// them. Measured is the honeypot reading — haunted oks whose session carried
// a measured shortcut. NamedHeld is the other direction: oks whose poster
// named the cheap path (Grader) and that no fix came back for, the evidence
// that naming it costs nothing and leaving it out costs later. It counts
// only oks old enough to have survived hauntProximity — a post from this
// morning has not held anything yet. Both are counts over the window and
// never per actor: a leaderboard on either is won by not posting the ok at
// all.
type AuditSummary struct {
	OKs       int `json:"oks"`
	Haunted   int `json:"haunted"`
	Measured  int `json:"measured"`
	NamedHeld int `json:"named_held"`
	// The cost side, over the measured units only: what the haunted oks
	// cost, what their fixes cost on top, and what every measured unit in
	// the window cost — the denominator, printed beside the sums and never
	// turned into a percentage.
	CostHaunted  float64 `json:"cost_haunted,omitempty"`
	CostHauntedN int     `json:"cost_haunted_n,omitempty"` // haunted oks with a reading
	CostFixes    float64 `json:"cost_fixes,omitempty"`
	CostFixesN   int     `json:"cost_fixes_n,omitempty"` // their fixes with a reading
	CostWindow   float64 `json:"cost_window,omitempty"`
}

// Summarize counts the window behind a Hauntings result. haunted must come
// from the same evs, which is why both are passed rather than recomputed:
// the audit prints the pairs and the sums from one pass over one record.
func Summarize(evs []Event, haunted []Haunting, now time.Time) AuditSummary {
	var s AuditSummary
	byID := map[string]struct{}{}
	units := UnitCosts(evs)
	for _, h := range haunted {
		byID[h.OK.ID()] = struct{}{}
		if h.Measured {
			s.Measured++
		}
		if u, ok := units[h.OK.ID()]; ok {
			s.CostHaunted += u.CostUSD
			s.CostHauntedN++
		}
		if u, ok := units[h.Fix.ID()]; ok {
			s.CostFixes += u.CostUSD
			s.CostFixesN++
		}
	}
	for _, u := range units {
		s.CostWindow += u.CostUSD
	}
	s.Haunted = len(haunted)
	for _, e := range evs {
		if e.Kind != "" || e.Outcome != OutcomeOK {
			continue
		}
		s.OKs++
		// "held" is a claim about a window that has run out. An ok posted
		// an hour ago has not held anything yet — its 7 days have barely
		// started, and counting it says the fix will not come. Only oks
		// past hauntProximity can be counted as having survived it.
		if now.Sub(e.TS) < hauntProximity {
			continue
		}
		if _, was := byID[e.ID()]; !was && strings.TrimSpace(e.Grader) != "" {
			s.NamedHeld++
		}
	}
	return s
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
	units := UnitCosts(evs)
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
				h := Haunting{OK: p, Fix: q, Shared: shared, Measured: len(p.Signals) > 0}
				if uo, ok := units[p.ID()]; ok {
					if uf, ok := units[q.ID()]; ok {
						h.Cost = &PairCost{OK: uo, Fix: uf}
					}
				}
				out = append(out, h)
				break
			}
		}
	}
	return out
}
