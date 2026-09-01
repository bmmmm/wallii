// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	Src string // PulseProbe or PulseSession — who took the reading
}

// Known reports whether this Pulse is a reading at all.
func (p Pulse) Known() bool { return !p.At.IsZero() }

// Turn reports whether this reading is what a turn cost, rather than what a
// ping cost. Only a turn drags the mood: a probe answers a different question —
// whether the API is there at all — and answering it in 170ms says nothing
// about a day spent waiting 17s per answer.
func (p Pulse) Turn() bool { return p.OK && p.Src == PulseSession }

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
		return Pulse{At: time.Now(), RTT: rtt, Err: pulseErr(err), Src: PulseProbe}
	}
	resp.Body.Close()
	return Pulse{At: time.Now(), RTT: rtt, OK: true, Src: PulseProbe}
}

// PostPulseTimeout is how long a post waits for the API before writing the
// reading down as a crashout. Shorter than the panel's, because the panel is
// watching and a post is in someone's way: at three seconds the drag is
// already two of three steps, so the difference between "very slow" and "not
// answering" has stopped mattering to the day being graded.
const PostPulseTimeout = 3 * time.Second

// PulseMSEnv is the session's own number — what a turn actually costs this
// harness, which is the thing an unauthenticated GET can only stand in for.
// `none` is a legal value: a session that knows the API is gone says so
// without wallii spending three seconds finding out.
const PulseMSEnv = "WALLII_PULSE_MS"

// SessionPulse takes the reading a post carries, in the order the sources
// deserve: the number this session was told to report, then the one its
// statusline already measured, and only then a probe — which can say the API is
// gone but never how long it takes. The second return is a note for stderr: a
// value nobody can parse is worth saying out loud and worth nothing else,
// because the post is written either way.
func SessionPulse(ctx context.Context) (Pulse, string) {
	if !PulseEnabled() {
		return Pulse{}, ""
	}
	switch v := strings.TrimSpace(os.Getenv(PulseMSEnv)); {
	case v == "":
	case strings.EqualFold(v, PulseNone):
		return Pulse{At: time.Now(), Src: PulseSession, Err: "the session reported no api"}, ""
	default:
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil || ms < 0 || ms > MaxPulseMS {
			return probeNow(ctx), fmt.Sprintf("%s=%q is neither milliseconds nor %q — measuring instead", PulseMSEnv, v, PulseNone)
		}
		return Pulse{At: time.Now(), RTT: time.Duration(ms) * time.Millisecond, OK: true, Src: PulseSession}, ""
	}
	if p, ok := filePulse(time.Now()); ok {
		return p, ""
	}
	return probeNow(ctx), ""
}

// PulseFileEnv names a file holding what the last turn cost, either as bare
// milliseconds or as a `key=value` line under PulseFileKey. Unset, wallii looks
// where the number already is: the statusline renders on every turn and caches
// it per session, so the value the terminal shows and the value the wall stores
// are the same measurement rather than two guesses at it.
const (
	PulseFileEnv = "WALLII_PULSE_FILE"
	PulseFileKey = "last_api_delta"
	// PulseFileMaxAge keeps a stale file out of a live reading. The statusline
	// rewrites it every turn, so an old file means an idle session — and the
	// cost of a turn from an hour ago is not what things are like now.
	PulseFileMaxAge = 15 * time.Minute
)

// filePulse reads the session's own measurement. Missing, stale or unreadable
// files are all the same answer — no reading — because a number wallii cannot
// stand behind is worse than none.
func filePulse(now time.Time) (Pulse, bool) {
	path := strings.TrimSpace(os.Getenv(PulseFileEnv))
	if path == "" {
		path = statuslineCache()
	}
	if path == "" {
		return Pulse{}, false
	}
	fi, err := os.Stat(path)
	if err != nil || now.Sub(fi.ModTime()) > PulseFileMaxAge {
		return Pulse{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Pulse{}, false
	}
	ms, ok := pulseFileValue(string(b))
	if !ok {
		return Pulse{}, false
	}
	// the file's own mtime is the reading's time: it was measured when the
	// turn ended, not when wallii got around to looking
	return Pulse{At: fi.ModTime(), RTT: time.Duration(ms) * time.Millisecond, OK: true, Src: PulseSession}, true
}

// pulseFileValue accepts a bare number of milliseconds or a key=value file
// carrying PulseFileKey — one format for a file written for wallii, one for a
// file that already existed.
func pulseFileValue(s string) (int64, bool) {
	parse := func(v string) (int64, bool) {
		ms, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || ms < 0 || ms > MaxPulseMS {
			return 0, false
		}
		return ms, true
	}
	if ms, ok := parse(s); ok {
		return ms, true
	}
	for _, line := range strings.Split(s, "\n") {
		if k, v, found := strings.Cut(line, "="); found && strings.TrimSpace(k) == PulseFileKey {
			return parse(v)
		}
	}
	return 0, false
}

// statuslineCache is where claudii's statusline keeps the current session's
// numbers — keyed by the session id Claude Code exports, so a wallii running
// under one session never reads another's. Absent env, absent file, absent
// tool: every one of them just means there is nothing to read.
func statuslineCache() string {
	sid := strings.TrimSpace(os.Getenv("CLAUDE_CODE_SESSION_ID"))
	if len(sid) < 8 {
		return ""
	}
	dir := strings.TrimSpace(os.Getenv("CLAUDII_CACHE_DIR"))
	if dir == "" {
		base := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME"))
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			base = filepath.Join(home, ".cache")
		}
		dir = filepath.Join(base, "claudii")
	}
	return filepath.Join(dir, "session-"+sid[:8])
}

func probeNow(ctx context.Context) Pulse {
	ctx, cancel := context.WithTimeout(ctx, PostPulseTimeout)
	defer cancel()
	return ProbePulse(ctx, PulseURL())
}

// Fields are what a post stores: the round trip in milliseconds, and who
// measured it. A pulse that never happened stores nothing at all — an absent
// field means nobody looked, and it must never read as an outage.
func (p Pulse) Fields() (ms int64, src string) {
	if !p.Known() {
		return 0, ""
	}
	if !p.OK {
		return p.RTT.Milliseconds(), PulseNone
	}
	return p.RTT.Milliseconds(), p.Src
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

// pulseAnchors say how many steps of the five-value scale a turn's API time
// takes off the wall's own grade.
//
// The first version of these anchors was wrong, and wrong in the way that
// matters: they were calibrated on a probe's round trip — 170ms to open a
// socket and read one HTTP answer — while the number anybody actually feels is
// what a turn costs, seventeen seconds of it. A ping says the door is open, not
// how long the room takes. So the scale is the one the statusline already
// draws in colors, because that is the scale being lived: under 15s the tool
// keeps up, to 30s you notice, to a minute you are waiting, past that you are
// doing something else.
var pulseAnchors = []struct {
	upTo time.Duration
	drag float64
}{
	{15 * time.Second, 0},
	{30 * time.Second, 1},
	{60 * time.Second, 2},
}

const pulseMaxDrag = 3

// PulseDrag is the penalty for one turn's API time, in steps of the mood scale.
func PulseDrag(d time.Duration) float64 {
	for _, a := range pulseAnchors {
		if d <= a.upTo {
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
// Only a turn drags: a probe that answered proves the API is there and nothing
// more, so it leaves the grades exactly as posted — which is also why a healthy
// pulse can never invent a mood on an ungraded wall. Waiting only ever
// subtracts. And no API at all is not a subtraction but a verdict: nothing is
// getting done at any grade, so the reading drops to the floor and says why.
func (s MoodSummary) Now(p Pulse) MoodNow {
	if p.Known() && !p.OK {
		return MoodNow{Avg: 1, Drag: pulseMaxDrag, Crash: true, Known: true}
	}
	if s.Count == 0 {
		return MoodNow{}
	}
	n := MoodNow{Avg: s.Avg, Known: true}
	if p.Turn() {
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
