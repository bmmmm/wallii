// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"sort"
	"time"
)

// PairState is the registration state of one (actor, repo) pair.
type PairState struct {
	Actor     string    `json:"actor"`
	Repo      string    `json:"repo"`
	Posts     int       `json:"posts"`
	FirstPost time.Time `json:"first_post,omitzero"`
	LastPost  time.Time `json:"last_post,omitzero"`
	Attached  bool      `json:"attached"`
	Explicit  bool      `json:"explicit"`          // an attach/detach event exists
	StateAt   time.Time `json:"state_at"`          // when the current state was entered
	Persona   string    `json:"persona,omitempty"` // latest voice line from an attach event
}

// Attachments folds the event stream into per (actor, repo) registration
// state — the wall itself is the registry, there is no second store to
// drift. Any post implicitly attaches its pair; attach/detach events set
// the state explicitly; a post after a detach re-attaches (whoever posts
// is back). Events must be chronological, as ReadLast returns them.
func Attachments(evs []Event) []PairState {
	type key struct{ actor, repo string }
	m := map[key]*PairState{}
	get := func(actor, repo string) *PairState {
		k := key{actor, repo}
		if p, ok := m[k]; ok {
			return p
		}
		p := &PairState{Actor: actor, Repo: repo}
		m[k] = p
		return p
	}
	for _, e := range evs {
		p := get(e.Actor, e.Repo)
		switch e.Kind {
		case KindAttach:
			p.Attached, p.Explicit, p.StateAt = true, true, e.TS
			if e.Persona != "" {
				p.Persona = e.Persona
			}
		case KindDetach:
			p.Attached, p.Explicit, p.StateAt = false, true, e.TS
		default:
			p.Posts++
			p.LastPost = e.TS
			if p.FirstPost.IsZero() {
				p.FirstPost = e.TS
			}
			if !p.Attached {
				p.Attached, p.StateAt = true, e.TS
			}
		}
	}
	out := make([]PairState, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Actor != out[j].Actor {
			return out[i].Actor < out[j].Actor
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}
