// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Squeeze is one expression, so its table can be read as the model itself:
// where each anchor sits, what the window's position does to it, and what a
// pause does. Every number here is derivable from squeezeAnchors,
// squeezeEaseFloor and squeezeHeatFull by hand — that is the point of keeping
// the three factors apart.
func TestSqueezeIsALevelAPositionAndAPace(t *testing.T) {
	const idle, busy = 0, squeezeHeatFull
	cases := []struct {
		name              string
		pct, elapsed, tph float64
		want              float64
	}{
		{"an untouched budget presses nothing", 0, 0, SqueezeDensityUnknown, 0},
		{"the first anchor is where it starts", 25, 0, SqueezeDensityUnknown, 0},
		{"the second anchor is one step", 50, 0, SqueezeDensityUnknown, 1},
		{"between anchors it runs continuously", 62.5, 0, SqueezeDensityUnknown, 1.5},
		{"the floor is three steps", 90, 0, SqueezeDensityUnknown, 3},
		{"past the limit is still three", 107, 0, SqueezeDensityUnknown, 3},
		{"at the reset a quarter is left", 90, 1, SqueezeDensityUnknown, 3 * squeezeEaseFloor},
		{"half a window in, the position discounts it", 90, 0.5, SqueezeDensityUnknown, 1.875},
		{"a counted hour of silence eases it", 90, 0, idle, 3 * squeezeHeatFloor},
		{"a full hour of work does not", 90, 0, busy, 3},
		{"an uncounted density eases nothing", 90, 0, SqueezeDensityUnknown, 3},
	}
	for _, c := range cases {
		if got := Squeeze(c.pct, c.elapsed, c.tph); !near(got, c.want) {
			t.Errorf("%s: Squeeze(%g, %g, %g) = %g, want %g", c.name, c.pct, c.elapsed, c.tph, got, c.want)
		}
	}

	// The two properties the whole shape exists for, stated as comparisons so
	// they survive any recalibration of the constants above.
	if late, early := Squeeze(80, 0.8, 500), Squeeze(80, 0.2, 500); !(late < early) {
		t.Errorf("the same 80%% presses %g near the reset and %g far from it — the longer cooldown must weigh less", late, early)
	}
	if quiet, loud := Squeeze(80, 0.3, 100), Squeeze(80, 0.3, 900); !(quiet < loud) {
		t.Errorf("the same 80%% presses %g at 100 turns/h and %g at 900 — the pause must ease it", quiet, loud)
	}
	// Nothing measured is not the same finding as an hour of nothing, and the
	// two must not produce the same reading.
	if unknown, paused := Squeeze(80, 0.3, SqueezeDensityUnknown), Squeeze(80, 0.3, 0); near(unknown, paused) {
		t.Errorf("an uncounted density reads %g, the same as a counted pause — those are different facts", unknown)
	}
}

// The real distribution, measured read-only on 2026-09-03 over every
// ~/.cache/claudii/history-*.tsv on this machine: the share of recorded turns
// falling in each ten-percent band, for both limit windows, over the 33,768
// turns that carry both (the five-hour column alone reaches back over
// 424,509, maximum 107 %). Aggregates only — no session id, no row and no
// single reading from those files is in this repository.
//
// Index i is the band [10i, 10i+10); index 10 is everything from 100 up.
var (
	squeezeBands5h = [11]float64{16.2, 16.2, 14.1, 11.6, 7.4, 7.0, 4.5, 6.1, 8.7, 7.9, 0.3}
	squeezeBands7d = [11]float64{15.2, 9.5, 8.3, 2.0, 2.2, 6.7, 12.0, 6.5, 20.7, 16.8, 0.0}
)

// reachedFrom is the share of turns landing in the band that holds pct or in
// any band above it. Band granularity is why it is "from" and not "above":
// the recorder was counted in tens, and pretending to know how 90.0 and 90.9
// split inside one band would be a model, not a measurement.
func reachedFrom(bands [11]float64, pct float64) float64 {
	share := 0.0
	for i, b := range bands {
		if float64(i*10+10) > pct {
			share += b
		}
	}
	return share
}

// The mistake pulseAnchors made before it was recalibrated: a scale whose
// first step the data never reach measures nothing, and reports no pressure
// through a week of it. So every step of this one is checked against the
// band table above — a frozen record of what the flight recorder held on the
// day the anchors were chosen, not a live read: CI has no recorder, and a
// test that read this machine's would prove only this machine's week. The
// table is what goes red when an anchor climbs past the data (re-measured by
// review on 2026-09-03 over 427k rows, within two points of every band).
func TestSqueezeAnchorsAreReachedByRealTurns(t *testing.T) {
	windows := map[string][11]float64{"5h": squeezeBands5h, "7d": squeezeBands7d}
	for _, a := range squeezeAnchors {
		for name, bands := range windows {
			if got := reachedFrom(bands, a.upTo); got <= 0 {
				t.Errorf("the %g%% step is reached by %.1f%% of turns in the %s window — a step the data never reach measures nothing", a.upTo, got, name)
			}
		}
	}
	// the first step is the one that went wrong last time, so it is named
	// on its own and held to more than a rounding artefact
	first := squeezeAnchors[0].upTo
	for name, bands := range windows {
		if got := reachedFrom(bands, first); got < 10 {
			t.Errorf("the first step (%g%%) is reached by %.1f%% of turns in the %s window — that is a scale nobody is on", first, got, name)
		}
	}
	// and the table itself has to stay a scale: rising bounds, rising
	// pressure, no pressure at the bottom, the documented maximum at the top
	if squeezeAnchors[0].press != 0 {
		t.Errorf("the first anchor presses %g — the bottom of a scale is where nothing happens", squeezeAnchors[0].press)
	}
	if last := squeezeAnchors[len(squeezeAnchors)-1]; last.press != SqueezeMaxPress {
		t.Errorf("the last anchor presses %g, SqueezeMaxPress is %d", last.press, SqueezeMaxPress)
	}
	for i, a := range squeezeAnchors[1:] {
		if prev := squeezeAnchors[i]; a.upTo <= prev.upTo || a.press <= prev.press {
			t.Errorf("anchor %d (%g%%, %g) does not rise over %d (%g%%, %g)", i+1, a.upTo, a.press, i, prev.upTo, prev.press)
		}
	}
	// the sanity check on the bands themselves: a mistyped table would make
	// every assertion above meaningless
	for name, bands := range windows {
		sum := 0.0
		for _, b := range bands {
			sum += b
		}
		if math.Abs(sum-100) > 0.5 {
			t.Errorf("the %s bands add up to %.1f%%, not 100", name, sum)
		}
	}
}

// The law, at the level where it is cheapest to break: a live reading carries
// the pressure beside the grade and cannot reach the grade.
func TestSqueezedNeverMovesTheAverage(t *testing.T) {
	s := MoodSummary{Count: 4, Avg: 4.0}
	base := s.Now(Pulse{})
	b := Budget{Pct5h: 99, Pct7d: 97, Src: SqueezeSession}
	got := base.Squeezed(b, time.Now(), SqueezeDensityUnknown)
	if got.Avg != base.Avg {
		t.Errorf("the average moved from %g to %g under a 99%% budget", base.Avg, got.Avg)
	}
	if got.Drag != base.Drag || got.Crash != base.Crash || got.Known != base.Known {
		t.Errorf("Squeezed touched a term that is not its own: %+v vs %+v", got, base)
	}
	if got.Squeeze <= 0 {
		t.Errorf("a 99%% budget reported %g pressure", got.Squeeze)
	}
	// and a reading nobody took reports nothing rather than nothing measured
	if quiet := base.Squeezed(Budget{}, time.Now(), SqueezeDensityUnknown); quiet.Squeeze != 0 {
		t.Errorf("an unread budget reported %g pressure", quiet.Squeeze)
	}
	// the tighter window is the one that presses; they are not added
	only5h := base.Squeezed(Budget{Pct5h: 99, Pct7d: 0, Src: SqueezeSession}, time.Now(), SqueezeDensityUnknown)
	if !near(only5h.Squeeze, got.Squeeze) {
		t.Errorf("a 99/0 budget presses %g and a 99/97 one %g — the windows are being summed", only5h.Squeeze, got.Squeeze)
	}
}

// A window's position is read off its own reset clock, not off a session's
// age: the budget belongs to the account and a dozen sessions spend it.
func TestBudgetElapsedIsReadOffTheResetClock(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	b := Budget{Reset5h: now.Add(SqueezeWindow5h / 4), Reset7d: now.Add(SqueezeWindow7d)}
	if got := b.Elapsed5h(now); !near(got, 0.75) {
		t.Errorf("a five-hour window resetting in 75 minutes is %g through, want 0.75", got)
	}
	if got := b.Elapsed7d(now); !near(got, 0) {
		t.Errorf("a week resetting in a week is %g through, want 0", got)
	}
	// a reset already past, and one nobody reported
	if got := (Budget{Reset5h: now.Add(-time.Hour)}).Elapsed5h(now); !near(got, 1) {
		t.Errorf("a reset an hour ago reads %g, want 1", got)
	}
	if got := (Budget{}).Elapsed5h(now); got != 0 {
		t.Errorf("an unknown reset reads %g — a reading must not discount itself by what it could not measure", got)
	}
}

func TestSessionBudgetReadsTheStatuslineCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-abcd1234")
	// invented, in the shape claudii writes: key=value lines, percentages of
	// the limit spent and the unix second each window refills at
	body := "model=Made Up 1.0\nrate_5h=82\nrate_7d=41.5\nreset_5h=1800000000\nreset_7d=1800600000\napi_mean_ms=4321\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(SqueezeFileEnv, path)
	now := time.Now()

	b := SessionBudget(now)
	if b.Src != SqueezeSession || b.Pct5h != 82 || b.Pct7d != 41.5 {
		t.Fatalf("read %+v, want 82/41.5 from %q", b, SqueezeSession)
	}
	if b.Reset5h.Unix() != 1800000000 || b.Reset7d.Unix() != 1800600000 {
		t.Errorf("resets read %v / %v", b.Reset5h, b.Reset7d)
	}
	p7, p5, src := b.Fields()
	if p7 != 41.5 || p5 != 82 || src != SqueezeSession {
		t.Errorf("Fields() = %g, %g, %q — the seven-day share comes first", p7, p5, src)
	}

	// A file the statusline stopped rewriting is an idle session, and an idle
	// session's budget is not what things are like now.
	old := now.Add(-SqueezeMaxAge - time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if stale := SessionBudget(now); stale.Known() {
		t.Errorf("a stale file still read as %+v", stale)
	}
}

// Every way of having nothing to say has to end in the same silence — and
// never in a zero, which is a budget nobody has touched.
func TestSessionBudgetIsSilentWhereItCannotMeasure(t *testing.T) {
	dir := t.TempDir()
	fresh := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]string{
		"a file that is not there":        filepath.Join(dir, "nope"),
		"a file with no limits in it":     fresh("bare", "model=Made Up 1.0\napi_mean_ms=100\n"),
		"only half the reading":           fresh("half", "rate_5h=80\n"),
		"a percentage that is not one":    fresh("junk", "rate_5h=lots\nrate_7d=40\n"),
		"a percentage past every limit":   fresh("huge", "rate_5h=9001\nrate_7d=40\n"),
		"a percentage that is not finite": fresh("nan", "rate_5h=NaN\nrate_7d=40\n"),
	}
	for name, path := range cases {
		t.Setenv(SqueezeFileEnv, path)
		if b := SessionBudget(time.Now()); b.Known() || b.Pct5h != 0 || b.Pct7d != 0 {
			t.Errorf("%s read as %+v, want no reading at all", name, b)
		}
	}
	// and the off switch reaches the reading, not just the file
	t.Setenv(SqueezeFileEnv, fresh("good", "rate_5h=80\nrate_7d=40\n"))
	t.Setenv(SqueezeEnv, "off")
	if b := SessionBudget(time.Now()); b.Known() {
		t.Errorf("WALLII_SQUEEZE=off still read %+v", b)
	}
}

// The store's own guard: a percentage nobody claims cannot be told from a
// guess, and a source nobody knows is not a source.
func TestValidateGuardsTheSqueezeFields(t *testing.T) {
	ok := Event{Repo: "r", Msg: "m"}
	cases := map[string]Event{
		"a value with no source":     {Repo: "r", Msg: "m", SqueezeP: 40},
		"an unknown source":          {Repo: "r", Msg: "m", SqueezeSrc: "guessed"},
		"a negative percentage":      {Repo: "r", Msg: "m", SqueezeSrc: SqueezeSession, Squeeze5h: -1},
		"a percentage past the cap":  {Repo: "r", Msg: "m", SqueezeSrc: SqueezeSession, Squeeze5h: MaxSqueezePct + 1},
		"a percentage that is a NaN": {Repo: "r", Msg: "m", SqueezeSrc: SqueezeSession, Squeeze5h: math.NaN()},
		"a squeeze on a reply":       {Repo: "r", Msg: "m", Kind: KindReact, Parent: "abcd", SqueezeSrc: SqueezeSession},
	}
	for name, e := range cases {
		if err := e.Validate(); err == nil {
			t.Errorf("%s validated", name)
		}
	}
	if err := ok.Validate(); err != nil {
		t.Errorf("a post with no squeeze at all failed: %v", err)
	}
	// A source with no value is the reading this field exists for: somebody
	// looked, and the budget was untouched.
	if err := (Event{Repo: "r", Msg: "m", SqueezeSrc: SqueezeSession}).Validate(); err != nil {
		t.Errorf("a measured but empty budget failed: %v", err)
	}
	// past 100 is not a typo — the recorder's own maximum is 107
	if err := (Event{Repo: "r", Msg: "m", SqueezeSrc: SqueezeSession, Squeeze5h: 107}).Validate(); err != nil {
		t.Errorf("107 %% rejected, but that is the highest reading on record: %v", err)
	}
}

func TestTurnDensityCountsTheLastHourAndBoundsWhatItReads(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	at := func(min int) int64 { return now.Add(-time.Duration(min) * time.Minute).Unix() }

	// a full read that reaches back past the window: rows older than the hour
	// are outside it, and the divisor is the hour
	stamps := []int64{at(200), at(150), at(50), at(30), at(10), at(1)}
	if got := turnsPerHour(now, stamps, true); !near(got, 4) {
		t.Errorf("four rows in the last hour read as %g per hour", got)
	}
	// an hour with nothing in it is zero, and zero is a measurement
	if got := turnsPerHour(now, []int64{at(200), at(150)}, true); got != 0 {
		t.Errorf("an idle hour read as %g per hour, want 0", got)
	}
	// a tail too short to have seen the whole hour divides by what it saw,
	// rather than reporting the part it could not read as quiet
	short := []int64{at(20), at(15), at(10), at(5)}
	if got := turnsPerHour(now, short, false); !near(got, 12) {
		t.Errorf("four rows over the last twenty minutes read as %g per hour, want 12", got)
	}
	// the same rows, read from the head of the file, describe an hour that
	// simply started twenty minutes ago
	if got := turnsPerHour(now, short, true); !near(got, 4) {
		t.Errorf("the same four rows from the file's head read as %g per hour, want 4", got)
	}
	// and a burst inside a few seconds is floored, not extrapolated into a
	// density nothing has ever produced
	burst := []int64{at(1), at(1), at(1), at(1), at(1)}
	if got := turnsPerHour(now, burst, false); !near(got, 5/squeezeDensityMinSpan.Hours()) {
		t.Errorf("five rows in one second read as %g per hour", got)
	}
}

func TestRecorderTailReadsOnlyTheEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history-2026-09.tsv")
	// invented rows in the recorder's shape: unix second, then columns this
	// reader never looks at
	var b strings.Builder
	for i := range 4000 {
		b.WriteString("17000000")
		b.WriteString(string(rune('0' + i%10)))
		b.WriteString("\tMade Up 1.0\t1.5\t20\t30\tsid\t1\t2\t3\t40\t1800000000\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	stamps, fromStart, err := recorderTail(path, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if fromStart {
		t.Error("a file far past the cap reported that the read reached its head")
	}
	if len(stamps) == 0 || len(stamps) > 4096/40 {
		t.Errorf("%d rows out of a 4096-byte tail — the read is not bounded by what it was given", len(stamps))
	}
	// the whole file, read whole, says so
	if _, all, err := recorderTail(path, 1<<24); err != nil || !all {
		t.Errorf("reading the whole file reported fromStart=%v (err %v)", all, err)
	}
}

// The month rolls over inside the hour like any other minute. Ten minutes
// into a month the current file holds ten minutes of rows and the previous
// month's holds the other fifty — read alone, the new file reported a pause
// nobody took and eased the pressure for it (found in review, 2026-09-03).
func TestTurnDensityReadsAcrossTheMonthBoundary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDII_CACHE_DIR", dir)
	now := time.Date(2026, time.October, 1, 0, 10, 0, 0, time.Local)
	write := func(name string, from, to time.Time) {
		t.Helper()
		var b strings.Builder
		for ts := from; ts.Before(to); ts = ts.Add(time.Minute) {
			b.WriteString(strconv.FormatInt(ts.Unix(), 10))
			b.WriteString("\tMade Up 1.0\t1.5\t20\t30\tsid\t1\t2\t3\t40\t1800000000\n")
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// one invented row a minute: two hours of September, ten minutes of
	// October — sixty of them inside the last hour, fifty across the boundary
	write("history-2026-09.tsv", now.Add(-120*time.Minute), now.Add(-10*time.Minute))
	write("history-2026-10.tsv", now.Add(-10*time.Minute), now)
	if got := TurnDensity(now); !near(got, 60) {
		t.Errorf("sixty rows across the month boundary read as %g per hour", got)
	}
	// a month whose file does not exist yet is read out of the previous one
	if err := os.Remove(filepath.Join(dir, "history-2026-10.tsv")); err != nil {
		t.Fatal(err)
	}
	if got := TurnDensity(now); !near(got, 50) {
		t.Errorf("fifty rows, all in last month's file, read as %g per hour", got)
	}
}

// A machine with no flight recorder gets a level and a position and no
// easing — never an easing invented out of a file that is not there.
func TestTurnDensityIsUnknownWithoutARecorder(t *testing.T) {
	t.Setenv("CLAUDII_CACHE_DIR", t.TempDir())
	if got := TurnDensity(time.Now()); got != SqueezeDensityUnknown {
		t.Errorf("an empty cache dir read %g turns per hour, want %d", got, SqueezeDensityUnknown)
	}

	dir := t.TempDir()
	t.Setenv("CLAUDII_CACHE_DIR", dir)
	now := time.Now()
	path := filepath.Join(dir, "history-"+now.Format("2006-01")+".tsv")
	var b strings.Builder
	for i := range 30 {
		// invented rows every five minutes back to two and a half hours ago,
		// so exactly twelve of them fall inside the last one
		ts := now.Add(-time.Duration((30-i)*5) * time.Minute).Unix()
		b.WriteString(strconv.FormatInt(ts, 10))
		b.WriteString("\tMade Up 1.0\t1.5\t20\t30\tsid\t1\t2\t3\t40\t1800000000\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := TurnDensity(now); !near(got, 12) {
		t.Errorf("twelve rows in the last hour read as %g per hour", got)
	}
}
