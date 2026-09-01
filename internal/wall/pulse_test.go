// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPulseDragRisesWithLatency(t *testing.T) {
	cases := []struct {
		rtt  time.Duration
		want float64
	}{
		{80 * time.Millisecond, 0},
		{400 * time.Millisecond, 0},
		{700 * time.Millisecond, 0.5},
		{time.Second, 0.5},
		{2 * time.Second, 1},
		{4 * time.Second, 2},
		{9 * time.Second, 3},
		{time.Minute, 3}, // the drag has a floor: it cannot fall off the scale
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
	fast := s.Now(Pulse{At: time.Now(), OK: true, RTT: 120 * time.Millisecond})
	if fast.Avg != s.Avg || fast.Drag != 0 {
		t.Errorf("fast api = %.2f (drag %.1f), want the wall's own %.2f untouched", fast.Avg, fast.Drag, s.Avg)
	}
	slow := s.Now(Pulse{At: time.Now(), OK: true, RTT: 4 * time.Second})
	if slow.Drag != 2 || slow.Avg != s.Avg-2 {
		t.Errorf("slow api = %.2f (drag %.1f), want %.2f (drag 2)", slow.Avg, slow.Drag, s.Avg-2)
	}
	if !slow.Known || slow.Crash {
		t.Errorf("slow api = known %v, crash %v, want known and no crash — it answered", slow.Known, slow.Crash)
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
	n := s.Now(Pulse{At: time.Now(), OK: true, RTT: 30 * time.Second})
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
	if n := s.Now(Pulse{At: time.Now(), OK: true, RTT: 3 * time.Second}); n.Known {
		t.Errorf("ungraded wall with a slow api = %.2f, want no reading — latency subtracts, it does not invent a mood", n.Avg)
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
	evs := moodEvents("good", "rough", "ok")
	evs[0].PulseMS, evs[0].PulseSrc = 200, PulseProbe
	evs[1].PulseSrc = PulseNone
	// evs[2] predates the field: no reading at all

	pts := MoodTrail(evs).Points
	if pts[0].PulseMS != 200 || pts[0].PulseN != 1 || pts[0].PulseDown != 0 {
		t.Errorf("measured post = %dms/%d/%d, want 200/1/0", pts[0].PulseMS, pts[0].PulseN, pts[0].PulseDown)
	}
	if pts[1].PulseN != 0 || pts[1].PulseDown != 1 {
		t.Errorf("outage post = %d readings, %d down; want 0 and 1", pts[1].PulseN, pts[1].PulseDown)
	}
	if pts[2].PulseN != 0 || pts[2].PulseDown != 0 {
		t.Errorf("unmeasured post = %d readings, %d down; want neither", pts[2].PulseN, pts[2].PulseDown)
	}
}

func TestMoodDaysAveragesTheConditions(t *testing.T) {
	evs := moodEvents("good", "good", "ok", "rough")
	evs[0].PulseMS, evs[0].PulseSrc = 100, PulseProbe
	evs[1].PulseMS, evs[1].PulseSrc = 300, PulseProbe
	evs[2].PulseSrc = PulseNone
	// evs[3] carries nothing, and must not drag the mean toward zero

	days := MoodDays(MoodTrail(evs).Points)
	if len(days) != 1 {
		t.Fatalf("folded into %d days, want 1", len(days))
	}
	d := days[0]
	if d.PulseMS != 200 || d.PulseN != 2 || d.PulseDown != 1 {
		t.Errorf("day = %dms over %d readings, %d down; want 200ms over 2, 1 down", d.PulseMS, d.PulseN, d.PulseDown)
	}
}

func TestStatsSplitsMeasuredFromMissing(t *testing.T) {
	evs := moodEvents("good", "good", "ok", "rough")
	evs[0].PulseMS, evs[0].PulseSrc = 100, PulseProbe
	evs[1].PulseMS, evs[1].PulseSrc = 300, PulseSession
	evs[2].PulseSrc = PulseNone

	s := Compute(evs)
	if s.PulseAnswered != 2 || s.PulseTotalMS != 400 || s.PulseDown != 1 {
		t.Errorf("stats = %d answered / %dms / %d down; want 2 / 400 / 1 (the unmeasured post is neither)",
			s.PulseAnswered, s.PulseTotalMS, s.PulseDown)
	}
}
