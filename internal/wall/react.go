// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"fmt"
	"strings"
)

// FindByID resolves an ID prefix to exactly one event. Ambiguity is an error
// rather than a pick: replying to the wrong post would be worse than asking
// for one more character.
func FindByID(evs []Event, idPrefix string) (Event, error) {
	idPrefix = strings.ToLower(strings.TrimSpace(idPrefix))
	if len(idPrefix) < 4 {
		return Event{}, fmt.Errorf("event ID %q is too short — at least 4 hex chars (see `wallii tail --ids`)", idPrefix)
	}
	var found []Event
	for _, e := range evs {
		if strings.HasPrefix(e.ID(), idPrefix) {
			found = append(found, e)
		}
	}
	switch len(found) {
	case 0:
		return Event{}, fmt.Errorf("no event with ID %s — list candidates with `wallii tail --ids`", idPrefix)
	case 1:
		return found[0], nil
	}
	return Event{}, fmt.Errorf("ID %s matches %d events — add more characters", idPrefix, len(found))
}

// Thread groups react/challenge events under the ID of the event they
// answer, oldest first per parent. Events whose parent is not in evs are
// still listed under that parent ID — the renderer decides how to show an
// orphan.
func Thread(evs []Event) map[string][]Event {
	out := map[string][]Event{}
	for _, e := range evs {
		if e.Kind == KindReact || e.Kind == KindChallenge {
			out[e.Parent] = append(out[e.Parent], e)
		}
	}
	return out
}

// OpenChallenge is a challenge nobody has answered yet, paired with the post
// it doubts (Target is zero-valued when the challenged post is not in evs).
type OpenChallenge struct {
	Challenge Event
	Target    Event
	HasTarget bool
}

// OpenChallenges returns challenges that still wait for the challenged actor,
// oldest first. A challenge is answered when the target's actor reacted to
// the challenge itself, or to the challenged post after the challenge was
// raised — answering the thread counts, restating is not required.
func OpenChallenges(evs []Event) []OpenChallenge {
	byID := map[string]Event{}
	for _, e := range evs {
		byID[e.ID()] = e
	}
	var out []OpenChallenge
	for _, c := range evs {
		if c.Kind != KindChallenge {
			continue
		}
		target, hasTarget := byID[c.Parent]
		answered := false
		for _, r := range evs {
			if r.Kind != KindReact || r.TS.Before(c.TS) {
				continue
			}
			if r.Parent == c.ID() {
				answered = true
				break
			}
			if hasTarget && r.Parent == c.Parent && r.Actor == target.Actor {
				answered = true
				break
			}
		}
		if !answered {
			out = append(out, OpenChallenge{Challenge: c, Target: target, HasTarget: hasTarget})
		}
	}
	return out
}
