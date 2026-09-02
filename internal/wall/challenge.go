// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"os"
	"strings"
	"time"
)

// The lint talks back. A contradiction note on stderr lands in the middle of
// a tool's output and is gone with the scrollback: 27 noted contradictions in
// 14 days, 0 challenges, 0 failed. A challenge is the same observation written
// to the wall, where it stays open until the actor it doubts reacts — the
// mechanism react.go already has, driven by the lint instead of a colleague.
// A post never closes a challenge, only a react does (OpenChallenges), so the
// doubt survives the moment it was raised in.
//
// Is that still a note, or already a gate? A note — but the last one that can
// be called that, and only while four properties hold:
//
//  1. The post lands unchanged, byte for byte. The challenge is written after
//     Append and never in its way; a failure here costs the challenge and
//     nothing else (post.go treats it as non-fatal).
//  2. The challenge names the regrade as the cheapest answer. Doubts fires on
//     (leftover word AND outcome=ok) or (friction word AND mood≥good), never
//     on the word alone — so the cheapest way out is not to drop "Sackgasse"
//     but to set --mood ok. The gradient points at the grade, where it belongs.
//  3. Nowhere is it counted how a challenge turns out. No conceded/defended
//     tally, ever — that count would be the gate. Permanent non-goal.
//  4. Dedup by (actor, class): at most one open lint challenge per class per
//     actor, so the volume of honesty never makes honesty feel expensive.
//
// Erode any one of them and this is a gate. No recursion guard is needed:
// Validate forbids outcome and mood on dialogue, so Doubts is empty on every
// challenge the lint writes.
//
// There is no --no-challenge per post. An agent that can switch the doubt off
// on exactly the post that raised it has a strictly cheaper move than
// answering. Only the environment switch, set by a human.

// LintActor is who the challenge is from. The wallii/ prefix says the machine
// is speaking, not a colleague.
const LintActor = "wallii/lint"

// AutoChallengeEnabled mirrors PulseEnabled: WALLII_AUTO_CHALLENGE=off keeps
// the lint on stderr only. Trimmed like every other switch here — a value
// read out of a file arrives as "off\n", and an off switch that quietly
// stays on is worse than one that was never offered.
func AutoChallengeEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("WALLII_AUTO_CHALLENGE")), "off")
}

// LintChallenge is what RaiseLintChallenge found and did. Doubt is zero when
// the post gave no reason; otherwise exactly one of Raised and Open is set,
// and Challenge is the event written (Raised) or the older one still waiting
// on the same actor for the same class (Open). Both are handles: the stderr
// note names the ID either way, because silence about a suppressed challenge
// would leave the agent believing nothing happened.
type LintChallenge struct {
	Doubt     Doubt
	Raised    bool
	Open      bool
	Challenge Event
}

// RaiseLintChallenge writes one challenge against target for the first doubt
// it draws — leftover before friction; two challenges on one sentence would
// be the machine arguing with itself twice — unless one of the same class is
// already open against the actor anywhere on the wall. The key is the actor,
// not the repo: the finding is about how this actor grades, and 22 distinct
// markers over 27 posts showed that a key on the marker collapses nothing.
// No cooldown after an answer: whoever answers and does it again straight
// away deserves a fresh one.
//
// The dedup reads the current month only (ParseFile, never ReadLast): every
// post runs this, and the post path must not open the gzipped archives. The
// class of an open challenge is not stored but derived again from its target,
// so a challenge whose target is not in this month's file cannot be
// classified and does not count. Consequence at a month boundary: "at most
// two open per actor" holds per month, not forever — a third one right after
// the boundary is the price of not decompressing history on every post.
func RaiseLintChallenge(dir string, target Event, now time.Time) (LintChallenge, error) {
	ds := Doubts(target)
	if len(ds) == 0 {
		return LintChallenge{}, nil
	}
	d := ds[0]
	evs, _, err := ParseFile(CurrentFile(dir, now))
	if err != nil && !os.IsNotExist(err) {
		return LintChallenge{Doubt: d}, err
	}
	for _, c := range OpenChallenges(evs) {
		if c.Challenge.Actor != LintActor || !c.HasTarget || c.Target.Actor != target.Actor {
			continue
		}
		if prior := Doubts(c.Target); len(prior) > 0 && prior[0].Class == d.Class {
			return LintChallenge{Doubt: d, Open: true, Challenge: c.Challenge}, nil
		}
	}
	ch := Event{TS: now.UTC(), Repo: target.Repo, Actor: LintActor, Kind: KindChallenge, Parent: target.ID(), Msg: d.Ask}
	if err := Append(dir, ch); err != nil {
		return LintChallenge{Doubt: d}, err
	}
	return LintChallenge{Doubt: d, Raised: true, Challenge: ch}, nil
}
