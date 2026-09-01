// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

// The wall's mood is history: what actors graded, after the fact. The pulse is
// the other half of the same question, measured instead of reported — how fast
// the API answers right now. Nobody has a good day while every turn takes eight
// seconds, and with no API at all there is no day to grade: the mood is a
// crashout, whatever the posts behind it said.
//
// The pulse never enters the curve. The series is posts, and a synthetic column
// for "now" would be a mood nobody posted — the one thing the panel promises
// not to draw. It moves the live reading at the top, and nothing else.

// PulseTimeout caps one probe. Past it there is no answer left to time, and the
// reading is the one a dead network gives: no api.
const PulseTimeout = 10 * time.Second

// DefaultPulseURL is timed, not used: a GET without a key answers 401, which is
// all the probe needs. No credentials are ever sent — the number being measured
// is how long the wire took, not whether we may write through it.
const DefaultPulseURL = "https://api.anthropic.com/v1/models"

// Pulse is one reading of the API. A zero Pulse means nobody has looked yet,
// which is not the same as a look that failed: Known separates the two, so a
// panel that never probed stays quiet instead of reporting an outage it never
// measured.
type Pulse struct {
	At  time.Time
	RTT time.Duration
	OK  bool   // an answer arrived, whatever its status code
	Err string // why none did, when none did
}

// Known reports whether this Pulse is a reading at all.
func (p Pulse) Known() bool { return !p.At.IsZero() }

// Fresh reports whether the reading is younger than d — a probe per keystroke
// would ping the API for opening a panel, and a reading from two seconds ago
// still stands.
func (p Pulse) Fresh(now time.Time, d time.Duration) bool {
	return p.Known() && now.Sub(p.At) < d
}

// PulseEnabled reports whether probing should happen at all. wallii is
// otherwise an offline tool that reads local files, so the one thing in it
// that touches the network has an off switch that needs no config file.
func PulseEnabled() bool { return !strings.EqualFold(os.Getenv("WALLII_PULSE"), "off") }

// PulseURL is what gets timed. Overridable because the thing worth measuring
// is whichever API this machine actually works against — a gateway, a local
// model server, a proxy — and a hardcoded host would measure someone else's.
func PulseURL() string {
	if u := strings.TrimSpace(os.Getenv("WALLII_PULSE_URL")); u != "" {
		return u
	}
	return DefaultPulseURL
}

// one client for the life of the process, so a probe reuses its connection the
// way a session does: a fresh TLS handshake per reading would time the
// handshake, not the API
var pulseClient = &http.Client{Timeout: PulseTimeout}

// ProbePulse times one round trip to url. Any answer counts, 401 included: the
// probe asks how long the API takes to speak, not what it is willing to say.
// The URL is passed in rather than read here so the thing being timed is
// always visible at the call site.
func ProbePulse(ctx context.Context, url string) Pulse {
	return probe(ctx, pulseClient, url)
}

// probe is the seam the tests drive: a stub transport times a round trip that
// never leaves the process, so the suite can measure latency and outages
// without a socket — and stays green where a sandbox refuses to bind one.
func probe(ctx context.Context, c *http.Client, url string) Pulse {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Pulse{At: time.Now(), Err: pulseErr(err)}
	}
	start := time.Now()
	resp, err := c.Do(req)
	rtt := time.Since(start)
	if err != nil {
		return Pulse{At: time.Now(), RTT: rtt, Err: pulseErr(err)}
	}
	resp.Body.Close()
	return Pulse{At: time.Now(), RTT: rtt, OK: true}
}

// PulseErrRunes caps the reason at what one panel line can hold beside the
// verdict it explains.
const PulseErrRunes = 56

// pulseErr keeps the one thing a reader needs: why no answer came. Go stacks
// its transport errors front to back — `Get "<url>": dial tcp <addr>: connect:
// connection refused` — and everything in front of the cause is either already
// on screen or an address nobody typed.
func pulseErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "no answer in " + PulseTimeout.String()
	}
	s := err.Error()
	if i := strings.Index(s, `": `); i >= 0 { // Get "<url>": …
		s = s[i+3:]
	}
	if strings.HasPrefix(s, "dial tcp ") { // dial tcp <addr>: …
		if i := strings.Index(s, ": "); i >= 0 {
			s = s[i+2:]
		}
	}
	if r := []rune(s); len(r) > PulseErrRunes {
		s = strings.TrimSpace(string(r[:PulseErrRunes-1])) + "…"
	}
	return s
}

// pulseAnchors say how many steps of the five-value scale the current round
// trip takes off the wall's own grade. Anchored on what a turn feels like, not
// on what looks tidy on a network graph: under half a second the tool is out of
// the way, a second is noticeable, past two and a half the loop you were in
// breaks, and past five you are waiting rather than working. The last anchor is
// open-ended — pulseMaxDrag is the floor, because a scale that keeps falling
// would put a fast wall below a slow one on latency alone.
var pulseAnchors = []struct {
	upTo time.Duration
	drag float64
}{
	{400 * time.Millisecond, 0},
	{time.Second, 0.5},
	{2500 * time.Millisecond, 1},
	{5 * time.Second, 2},
}

const pulseMaxDrag = 3

// PulseDrag is the penalty for one round trip, in steps of the mood scale.
func PulseDrag(rtt time.Duration) float64 {
	for _, a := range pulseAnchors {
		if rtt <= a.upTo {
			return a.drag
		}
	}
	return pulseMaxDrag
}

// MoodNow is the mood as of this second. Avg is what the face and the word are
// drawn from; Drag and Crash keep the two terms behind it separable, so the
// reading can show its own arithmetic instead of asking to be believed.
type MoodNow struct {
	Avg   float64 // 1 … 5, or 0 when nothing measured says anything
	Drag  float64 // steps the latency took off the wall's average
	Crash bool    // no API — the bottom of the scale, whatever the wall said
	Known bool    // false when the wall carries no grades and the API is fine
}

// Now folds the live pulse into the wall's average.
//
// A fast API says nothing: it leaves the grades exactly as they were posted,
// which is why an unprobed or healthy pulse can never invent a mood on an
// ungraded wall. Latency only ever subtracts. And no API at all is not a
// subtraction but a verdict — nothing is getting done at any grade, so the
// reading drops to the floor of the scale and says why.
func (s MoodSummary) Now(p Pulse) MoodNow {
	if p.Known() && !p.OK {
		return MoodNow{Avg: 1, Drag: pulseMaxDrag, Crash: true, Known: true}
	}
	if s.Count == 0 {
		return MoodNow{}
	}
	n := MoodNow{Avg: s.Avg, Known: true}
	if p.Known() {
		n.Drag = PulseDrag(p.RTT)
		n.Avg = clampMood(s.Avg - n.Drag)
	}
	return n
}

// clampMood keeps a value on the scale the words exist for.
func clampMood(v float64) float64 {
	if v < 1 {
		return 1
	}
	if v > float64(len(Moods)) {
		return float64(len(Moods))
	}
	return v
}
