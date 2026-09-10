// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"fmt"
	"sort"
)

// What a unit of work cost is a reading, not a field. A post stores the
// session's cumulative spend (CostCum, TokCum, keyed by Sess); the cost of
// the work between two posts is the difference between them, taken here,
// whenever anybody asks. The first post of a session counts from the
// session's start — FromStart says so, because that unit also paid for
// whatever the session did before it first posted.
//
// Reported, never applied. Nothing here reaches a Mood or an Outcome, and
// nothing in post.go refuses a post for what it cost: a grade a machine
// pushed down for its price would be read later as the price of honesty.
type Unit struct {
	CostUSD   float64
	Tok       int64
	FromStart bool
}

// UnitCosts reads the unit cost of every measured post in evs, keyed by
// Event.ID(). Posts are grouped by Sess and walked in time order; the
// delta is taken against the previous post of the same session whoever
// posted it — a session is one agent's, and an actor switch inside it is
// still the same spend. A negative delta (a cache that started over, two
// sessions sharing a key) yields no entry, never a zero: absent means
// unreadable, zero would mean free.
func UnitCosts(evs []Event) map[string]Unit {
	bySess := map[string][]Event{}
	for _, e := range evs {
		if e.Kind != "" || e.CostSrc == "" || e.Sess == "" {
			continue
		}
		bySess[e.Sess] = append(bySess[e.Sess], e)
	}
	out := map[string]Unit{}
	for _, run := range bySess {
		sort.SliceStable(run, func(i, j int) bool { return run[i].TS.Before(run[j].TS) })
		for i, e := range run {
			if i == 0 {
				out[e.ID()] = Unit{CostUSD: e.CostCum, Tok: e.TokCum, FromStart: true}
				continue
			}
			prev := run[i-1]
			dc, dt := e.CostCum-prev.CostCum, e.TokCum-prev.TokCum
			if dc < 0 || dt < 0 {
				continue
			}
			out[e.ID()] = Unit{CostUSD: dc, Tok: dt}
		}
	}
	return out
}

// UnitNote is the line `wallii post` prints after the append: what the
// unit just posted cost, read against the actor's own earlier posts. Empty
// when the post carries no reading — no file, a stale one, half a reading —
// so a session without the statusline hears nothing rather than a zero.
func UnitNote(prior []Event, e Event) string {
	if e.CostSrc == "" {
		return ""
	}
	u, ok := UnitCosts(append(append([]Event(nil), prior...), e))[e.ID()]
	if !ok {
		return ""
	}
	since := "since your last post in this session"
	if u.FromStart {
		since = "since session start"
	}
	return fmt.Sprintf("this unit ≈ %s · %s tok %s (cost_cum %.2f)", FmtUSD(u.CostUSD), FmtTok(u.Tok), since, e.CostCum)
}

// FmtUSD renders a spend the way it is read: cents under a dollar, two
// decimals under a hundred, whole dollars above.
func FmtUSD(usd float64) string {
	switch {
	case usd >= 100:
		return fmt.Sprintf("$%.0f", usd)
	default:
		return fmt.Sprintf("$%.2f", usd)
	}
}

// FmtTok renders a token count: 812, 9.8k, 1.3M.
func FmtTok(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}
