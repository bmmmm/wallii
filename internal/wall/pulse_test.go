// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPulseDragRisesWithLatency(t *testing.T) {
	// turn times, not round trips: the scale is what a turn costs, which is
	// seconds where a ping is milliseconds
	cases := []struct {
		rtt  time.Duration
		want float64
	}{
		{170 * time.Millisecond, 0},
		{5 * time.Second, 0},
		{15 * time.Second, 0},
		{17 * time.Second, 1},
		{30 * time.Second, 1},
		{45 * time.Second, 2},
		{60 * time.Second, 2},
		{90 * time.Second, 3},
		{10 * time.Minute, 3}, // the drag has a floor: it cannot fall off the scale
	}
	last := -1.0
	for _, c := range cases {
		got := PulseDrag(c.rtt)
		if got != c.want {
			t.Errorf("PulseDrag(%s) = %.1f, want %.1f", c.rtt, got, c.want)
		}
		if got < last {
			t.Errorf("PulseDrag(%s) = %.1f fell below the previous anchor %.1f — the drag must never reward waiting", c.rtt, got, last)
		}
		last = got
	}
}

func TestMoodNowDragsWithLatency(t *testing.T) {
	s := MoodTrail(moodEvents("great", "great", "good")) // 4.67
	turn := func(d time.Duration) Pulse {
		return Pulse{At: time.Now(), OK: true, RTT: d, Src: PulseSession}
	}
	fast := s.Now(turn(4 * time.Second))
	if fast.Avg != s.Avg || fast.Drag != 0 {
		t.Errorf("a 4s turn = %.2f (drag %.1f), want the wall's own %.2f untouched", fast.Avg, fast.Drag, s.Avg)
	}
	slow := s.Now(turn(45 * time.Second))
	if slow.Drag != 2 || slow.Avg != s.Avg-2 {
		t.Errorf("a 45s turn = %.2f (drag %.1f), want %.2f (drag 2)", slow.Avg, slow.Drag, s.Avg-2)
	}
	if !slow.Known || slow.Crash {
		t.Errorf("slow api = known %v, crash %v, want known and no crash — it answered", slow.Known, slow.Crash)
	}
}

// The bug this scale was born with: a probe answering in 170ms is not a fast
// day, it is an open socket. Only a measured turn may move the mood.
func TestMoodNowIgnoresAPing(t *testing.T) {
	s := MoodTrail(moodEvents("great", "great", "good"))
	ping := Pulse{At: time.Now(), OK: true, RTT: 170 * time.Millisecond, Src: PulseProbe}
	if n := s.Now(ping); n.Drag != 0 || n.Avg != s.Avg {
		t.Errorf("a ping moved the mood to %.2f (drag %.1f), want %.2f untouched", n.Avg, n.Drag, s.Avg)
	}
	// and a slow ping is still only a ping — it says the network is unhappy,
	// not that a turn took that long
	slowPing := Pulse{At: time.Now(), OK: true, RTT: 2 * time.Second, Src: PulseProbe}
	if n := s.Now(slowPing); n.Drag != 0 {
		t.Errorf("a slow ping claimed a drag of %.1f", n.Drag)
	}
}

func TestMoodNowCrashesWithoutAPI(t *testing.T) {
	s := MoodTrail(moodEvents("great", "great", "great")) // the best wall there is
	n := s.Now(Pulse{At: time.Now(), Err: "connection refused"})
	if !n.Crash || !n.Known {
		t.Fatalf("no api = crash %v, known %v, want both true", n.Crash, n.Known)
	}
	if n.Avg != 1 {
		t.Errorf("no api = %.1f, want 1 — nothing is getting done at any grade", n.Avg)
	}
	if MoodLevel(n.Avg) != 1 {
		t.Errorf("no api draws at level %d, want the floor of the scale", MoodLevel(n.Avg))
	}
}

func TestMoodNowClampsToTheScale(t *testing.T) {
	s := MoodTrail(moodEvents("rough", "rough")) // 2.0
	n := s.Now(Pulse{At: time.Now(), OK: true, RTT: 4 * time.Minute, Src: PulseSession})
	if n.Avg != 1 {
		t.Errorf("2.0 minus a 3-step drag = %.1f, want 1 — the scale has a floor", n.Avg)
	}
}

func TestMoodNowIgnoresAnUnmeasuredPulse(t *testing.T) {
	s := MoodTrail(moodEvents("good", "ok"))
	n := s.Now(Pulse{}) // nobody looked
	if n.Avg != s.Avg || n.Drag != 0 || n.Crash {
		t.Errorf("unprobed = %.2f (drag %.1f, crash %v), want the wall's own %.2f — an outage nobody measured is not one", n.Avg, n.Drag, n.Crash, s.Avg)
	}
}

func TestMoodNowUngradedWallStaysUnknownUntilItCrashes(t *testing.T) {
	s := MoodTrail([]Event{{TS: time.Now(), Repo: "alpha", Msg: "no grade"}})
	if n := s.Now(Pulse{At: time.Now(), OK: true, RTT: 40 * time.Second, Src: PulseSession}); n.Known {
		t.Errorf("ungraded wall with a slow api = %.2f, want no reading — waiting subtracts, it does not invent a mood", n.Avg)
	}
	if n := s.Now(Pulse{At: time.Now(), Err: "no route to host"}); !n.Known || n.Avg != 1 {
		t.Errorf("ungraded wall with no api = known %v, %.1f, want a measured crashout at 1", n.Known, n.Avg)
	}
}

// stubAPI answers after delay with code, or fails the way a dead network does.
func stubAPI(t *testing.T, delay time.Duration, code int, fail error) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-api-key") != "" || r.Header.Get("authorization") != "" {
			t.Error("the probe sent credentials — it times the wire, it does not authenticate")
		}
		// a real transport gives up when the caller does, and so does this one.
		// The check comes before the wait: with an already-cancelled context and
		// no delay both select cases are ready at once, and the answer would be
		// a coin toss.
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(delay):
		}
		if fail != nil {
			return nil, fail
		}
		return &http.Response{StatusCode: code, Body: http.NoBody, Request: r}, nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbePulseTimesAnyAnswer(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusTooManyRequests} {
		p := probe(context.Background(), stubAPI(t, 20*time.Millisecond, code, nil), "http://api.test/v1/models")
		if !p.OK {
			t.Errorf("status %d = no api (%s), want an answer — the probe times the round trip, not the permission", code, p.Err)
		}
		if !p.Known() || p.RTT < 20*time.Millisecond {
			t.Errorf("status %d = rtt %s, known %v, want the round trip it actually took", code, p.RTT, p.Known())
		}
	}
}

func TestProbePulseReportsNoAPI(t *testing.T) {
	c := stubAPI(t, 0, 0, errors.New("dial tcp 160.79.104.10:443: connect: connection refused"))
	p := probe(context.Background(), c, "http://api.test/v1/models")
	if p.OK {
		t.Fatal("a dead network answered — want no api")
	}
	if !p.Known() || p.Err == "" {
		t.Errorf("failed probe = known %v, err %q, want a reading that says why", p.Known(), p.Err)
	}
	if p.Err != "connect: connection refused" {
		t.Errorf("err = %q, want the cause without the URL and address Go stacks in front of it", p.Err)
	}
	if s := MoodTrail(moodEvents("good")).Now(p); !s.Crash {
		t.Error("a failed probe did not reach the mood as a crashout")
	}
}

func TestPulseErrKeepsTheCause(t *testing.T) {
	long := errors.New(`Get "https://api.anthropic.com/v1/models": dial tcp 160.79.104.10:443: ` + strings.Repeat("read: connection reset by peer, ", 4))
	got := pulseErr(long)
	if n := len([]rune(got)); n > PulseErrRunes {
		t.Errorf("err is %d runes, max %d: %q", n, PulseErrRunes, got)
	}
	if !strings.HasPrefix(got, "read: connection reset") || !strings.HasSuffix(got, "…") {
		t.Errorf("err = %q, want the cause, cut where it stops fitting", got)
	}
	if to := pulseErr(context.DeadlineExceeded); to != "no answer in 10s" {
		t.Errorf("timeout = %q, want the wait named in seconds rather than Go's sentence", to)
	}
}

func TestProbePulseHonorsTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := probe(ctx, stubAPI(t, 0, http.StatusOK, nil), "http://api.test/v1/models")
	if p.OK || p.Err == "" {
		t.Errorf("cancelled probe = ok %v, err %q, want a failed reading", p.OK, p.Err)
	}
}

func TestPulseOffSwitch(t *testing.T) {
	t.Setenv("WALLII_PULSE", "")
	if !PulseEnabled() {
		t.Error("probing is off by default, want on")
	}
	t.Setenv("WALLII_PULSE", "off")
	if PulseEnabled() {
		t.Error("WALLII_PULSE=off still probes")
	}
}

func TestPulseURLOverride(t *testing.T) {
	t.Setenv("WALLII_PULSE_URL", "")
	if PulseURL() != DefaultPulseURL {
		t.Errorf("PulseURL = %q, want the default %q", PulseURL(), DefaultPulseURL)
	}
	t.Setenv("WALLII_PULSE_URL", " http://localhost:8010/v1/models ")
	if got := PulseURL(); got != "http://localhost:8010/v1/models" {
		t.Errorf("PulseURL = %q, want the override, trimmed", got)
	}
}

func TestPulseFreshness(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if (Pulse{}).Fresh(now, time.Minute) {
		t.Error("an unmeasured pulse counted as fresh — the first probe would never run")
	}
	if !(Pulse{At: now.Add(-10 * time.Second)}).Fresh(now, time.Minute) {
		t.Error("a 10s old reading is not fresh within a minute")
	}
	if (Pulse{At: now.Add(-2 * time.Minute)}).Fresh(now, time.Minute) {
		t.Error("a two-minute-old reading counted as fresh")
	}
}

// The reading a post carries: the session's own number where there is one,
// wallii's measurement otherwise, and nothing at all when nobody looked.

func TestSessionPulseTakesTheSessionsNumber(t *testing.T) {
	t.Setenv("WALLII_PULSE", "")
	t.Setenv(PulseMSEnv, "1830")
	p, note := SessionPulse(context.Background())
	if note != "" {
		t.Errorf("note = %q, want none — the value parsed", note)
	}
	if !p.OK || p.RTT != 1830*time.Millisecond || p.Src != PulseSession {
		t.Fatalf("pulse = ok %v, %s, src %q; want a 1.83s session reading", p.OK, p.RTT, p.Src)
	}
	if ms, src := p.Fields(); ms != 1830 || src != PulseSession {
		t.Errorf("fields = %dms, %q; want 1830, %q", ms, src, PulseSession)
	}
}

func TestSessionPulseTakesTheSessionsOutage(t *testing.T) {
	t.Setenv("WALLII_PULSE", "")
	t.Setenv(PulseMSEnv, "NONE") // a shell exports what it exports
	p, _ := SessionPulse(context.Background())
	ms, src := p.Fields()
	if src != PulseNone || ms != 0 {
		t.Errorf("fields = %dms, %q; want an outage the session reported without a probe", ms, src)
	}
}

func TestSessionPulseOffRecordsNothing(t *testing.T) {
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv(PulseMSEnv, "1830")
	p, _ := SessionPulse(context.Background())
	if p.Known() {
		t.Error("probing is off and a reading came back anyway")
	}
	if ms, src := p.Fields(); ms != 0 || src != "" {
		t.Errorf("fields = %dms, %q; want nothing stored — nobody measured", ms, src)
	}
}

// A value nobody can parse is said out loud and then ignored: wallii measures
// for itself rather than writing down a number it does not understand.
func TestSessionPulseFallsBackToMeasuring(t *testing.T) {
	t.Setenv("WALLII_PULSE", "")
	t.Setenv(PulseMSEnv, "soon")
	t.Setenv("WALLII_PULSE_URL", "http://127.0.0.1:9/v1/models") // nothing listens on discard
	p, note := SessionPulse(context.Background())
	if !strings.Contains(note, PulseMSEnv) || !strings.Contains(note, "measuring instead") {
		t.Errorf("note = %q, want it to name the variable and what happened next", note)
	}
	if p.Src != PulseProbe {
		t.Errorf("src = %q, want the fallback to be wallii's own measurement", p.Src)
	}
	if _, src := p.Fields(); src != PulseNone {
		t.Errorf("fields src = %q, want %q — the probe reached nothing", src, PulseNone)
	}
}

func TestPulseFieldsNeverInventAnOutage(t *testing.T) {
	if ms, src := (Pulse{}).Fields(); ms != 0 || src != "" {
		t.Errorf("an unmeasured pulse stores %dms/%q, want nothing — absence is not an outage", ms, src)
	}
}

func TestValidatePulse(t *testing.T) {
	ok := func(ms int64, src string) Event {
		e := validEvent()
		e.PulseMS, e.PulseSrc = ms, src
		return e
	}
	for _, e := range []Event{ok(0, ""), ok(185, PulseProbe), ok(1830, PulseSession), ok(0, PulseNone), ok(2999, PulseNone)} {
		if err := e.Validate(); err != nil {
			t.Errorf("valid pulse (%dms, %q) rejected: %v", e.PulseMS, e.PulseSrc, err)
		}
	}
	bad := map[string]Event{
		"unknown source":       ok(185, "guess"),
		"negative round trip":  ok(-1, PulseProbe),
		"over an hour":         ok(MaxPulseMS+1, PulseProbe),
		"value with no source": ok(185, ""),
	}
	for name, e := range bad {
		if err := e.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// Dialogue carries no telemetry, and the pulse is telemetry.
func TestValidateRejectsPulseOnAReply(t *testing.T) {
	e := validEvent()
	e.Kind, e.Parent = KindReact, "abc1234"
	e.PulseMS, e.PulseSrc = 185, PulseProbe
	if err := e.Validate(); err == nil {
		t.Fatal("a react carrying a pulse was accepted")
	}
}

// The conditions travel with the points the curve is drawn from, so a column
// can say what the work behind it was up against.
func TestMoodTrailCarriesTheConditions(t *testing.T) {
	evs := moodEvents("good", "rough", "ok", "good")
	evs[0].PulseMS, evs[0].PulseSrc = 17_000, PulseSession
	evs[1].PulseSrc = PulseNone
	evs[2].PulseMS, evs[2].PulseSrc = 170, PulseProbe // a ping, not a turn
	// evs[3] predates the field: no reading at all

	pts := MoodTrail(evs).Points
	if pts[0].PulseMS != 17_000 || pts[0].PulseN != 1 || pts[0].PulseDown != 0 {
		t.Errorf("measured turn = %dms/%d/%d, want 17000/1/0", pts[0].PulseMS, pts[0].PulseN, pts[0].PulseDown)
	}
	if pts[1].PulseN != 0 || pts[1].PulseDown != 1 {
		t.Errorf("outage post = %d readings, %d down; want 0 and 1", pts[1].PulseN, pts[1].PulseDown)
	}
	if pts[2].PulseN != 0 || pts[2].PulseMS != 0 {
		t.Errorf("ping post = %dms over %d readings, want none — reachability is not a turn", pts[2].PulseMS, pts[2].PulseN)
	}
	if pts[3].PulseN != 0 || pts[3].PulseDown != 0 {
		t.Errorf("unmeasured post = %d readings, %d down; want neither", pts[3].PulseN, pts[3].PulseDown)
	}
}

func TestMoodDaysAveragesTheConditions(t *testing.T) {
	evs := moodEvents("good", "good", "ok", "rough", "good")
	evs[0].PulseMS, evs[0].PulseSrc = 10_000, PulseSession
	evs[1].PulseMS, evs[1].PulseSrc = 30_000, PulseSession
	evs[2].PulseSrc = PulseNone
	evs[3].PulseMS, evs[3].PulseSrc = 170, PulseProbe // must not pull the mean down
	// evs[4] carries nothing, and must not pull it toward zero either

	days := MoodDays(MoodTrail(evs).Points)
	if len(days) != 1 {
		t.Fatalf("folded into %d days, want 1", len(days))
	}
	d := days[0]
	if d.PulseMS != 20_000 || d.PulseN != 2 || d.PulseDown != 1 {
		t.Errorf("day = %dms over %d turns, %d down; want 20000ms over 2, 1 down", d.PulseMS, d.PulseN, d.PulseDown)
	}
}

func TestStatsKeepsTurnsPingsAndOutagesApart(t *testing.T) {
	evs := moodEvents("good", "good", "ok", "rough")
	evs[0].PulseMS, evs[0].PulseSrc = 10_000, PulseSession
	evs[1].PulseMS, evs[1].PulseSrc = 30_000, PulseSession
	evs[2].PulseMS, evs[2].PulseSrc = 170, PulseProbe
	evs[3].PulseSrc = PulseNone

	s := Compute(evs)
	if s.PulseTurns != 2 || s.PulseTurnTotalMS != 40_000 {
		t.Errorf("turns = %d / %dms, want 2 / 40000 — a ping is not a turn", s.PulseTurns, s.PulseTurnTotalMS)
	}
	if s.PulsePings != 1 || s.PulseDown != 1 {
		t.Errorf("pings/down = %d/%d, want 1/1", s.PulsePings, s.PulseDown)
	}
}

// The number the terminal already shows: the statusline renders every turn and
// caches what it cost, so wallii reads that rather than inventing a second
// measurement of a different thing.
func TestSessionPulseReadsTheStatuslineCache(t *testing.T) {
	dir := t.TempDir()
	sid := "24d0857f-2f6f-454b-a846-9bfcb956de82"
	if err := os.WriteFile(filepath.Join(dir, "session-"+sid[:8]),
		[]byte("model=Opus 5\nlast_api_duration_ms=1604071\nlast_api_delta=17431\ncompactions=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WALLII_PULSE", "")
	t.Setenv(PulseMSEnv, "")
	t.Setenv("CLAUDII_CACHE_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", sid)

	p, note := SessionPulse(context.Background())
	if note != "" {
		t.Errorf("note = %q, want none", note)
	}
	if !p.Turn() || p.RTT != 17431*time.Millisecond || p.Src != PulseSession {
		t.Fatalf("pulse = %s from %q (turn %v), want a 17.4s turn from the session", p.RTT, p.Src, p.Turn())
	}
	// and it drags the mood the way seventeen seconds should
	if drag := PulseDrag(p.RTT); drag != 1 {
		t.Errorf("a 17.4s turn drags %.1f, want 1", drag)
	}
}

// A session's own cache, but from an hour ago: that is what the last turn cost
// before lunch, not what things are like now.
func TestSessionPulseIgnoresAStaleCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-24d0857f")
	if err := os.WriteFile(path, []byte("last_api_delta=17431\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDII_CACHE_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "24d0857f-2f6f")
	if _, ok := filePulse(time.Now()); ok {
		t.Error("a two-hour-old cache was taken as current")
	}
	// fresh again, and it counts again — the rule is the age, not the file
	if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := filePulse(time.Now()); !ok {
		t.Error("a cache written this second was ignored")
	}
}

func TestPulseFileAcceptsBothShapes(t *testing.T) {
	if ms, ok := pulseFileValue("17431\n"); !ok || ms != 17431 {
		t.Errorf("bare milliseconds = %d/%v, want 17431", ms, ok)
	}
	if ms, ok := pulseFileValue("a=1\nlast_api_delta=17431\nb=2\n"); !ok || ms != 17431 {
		t.Errorf("key=value file = %d/%v, want 17431", ms, ok)
	}
	for _, junk := range []string{"", "last_api_delta=\n", "last_api_delta=soon\n", "other=17431\n", "last_api_delta=-5\n"} {
		if _, ok := pulseFileValue(junk); ok {
			t.Errorf("%q was read as a reading", junk)
		}
	}
}

// An explicit value outranks the cache: a harness that says what its turns
// cost has said so on purpose.
func TestSessionPulseEnvOutranksTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session-24d0857f"), []byte("last_api_delta=17431\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WALLII_PULSE", "")
	t.Setenv("CLAUDII_CACHE_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "24d0857f-2f6f")
	t.Setenv(PulseMSEnv, "4200")

	p, _ := SessionPulse(context.Background())
	if p.RTT != 4200*time.Millisecond {
		t.Errorf("pulse = %s, want the 4.2s the session was told to report", p.RTT)
	}
}

func TestPulseFileOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn-ms")
	if err := os.WriteFile(path, []byte("22000"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WALLII_PULSE", "")
	t.Setenv(PulseMSEnv, "")
	t.Setenv(PulseFileEnv, path)

	p, _ := SessionPulse(context.Background())
	if !p.Turn() || p.RTT != 22*time.Second {
		t.Errorf("pulse = %s (turn %v), want a 22s turn from the named file", p.RTT, p.Turn())
	}
}

// No session id, no cache, no file — the probe is all that is left, and it may
// only ever answer "there" or "not there".
func TestSessionPulseFallsBackToAPing(t *testing.T) {
	t.Setenv("WALLII_PULSE", "")
	t.Setenv(PulseMSEnv, "")
	t.Setenv(PulseFileEnv, "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CLAUDII_CACHE_DIR", t.TempDir())
	t.Setenv("WALLII_PULSE_URL", "http://127.0.0.1:9/v1/models")

	p, _ := SessionPulse(context.Background())
	if p.Src != PulseProbe {
		t.Errorf("src = %q, want a probe when nothing else measured", p.Src)
	}
	if p.Turn() {
		t.Error("a probe claimed to be a turn")
	}
}
