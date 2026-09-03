// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The pulse measures what one turn costs. The squeeze measures what there is
// left to spend on turns at all: the statusline that already caches the API
// time per session caches the rate limits beside it, so the same file answers
// both halves of "what were the conditions" — how long the answer took, and
// how much of the five-hour and seven-day budget was gone when it came.
//
// The same law as the pulse's, and one more reason for it. The squeeze never
// enters the curve and never rewrites a posted mood. A grade a machine pushed
// down would be read later as the bottom of the scale finally being reached —
// and whether `rough` ever gets posted is the one question this wall still
// owes an answer to. Answering it with wallii's own arithmetic would not be
// an answer. So the pressure is stored beside the grade and shown beside the
// reading, and there is no code path from it to a Mood.

// SqueezeSession is the only source there is: the session's own statusline
// cache. There is no probe to fall back on — nothing outside this machine
// knows what the account has spent — so unlike the pulse there is no `none`
// either. A file that is missing, stale or unparseable leaves the source
// empty, which says nobody measured, and must never read as an empty budget.
const SqueezeSession = "session"

const (
	// SqueezeEnv switches the reading off, the way WALLII_PULSE does for the
	// probe: a post carries no budget reading at all then, indistinguishable
	// from one written before the field existed.
	SqueezeEnv = "WALLII_SQUEEZE"
	// SqueezeFileEnv points at the file to read instead of the session's own
	// statusline cache — a different statusline, or a fixture.
	SqueezeFileEnv = "WALLII_SQUEEZE_FILE"
	// SqueezeMaxAge keeps a stale file out of a reading. It shares the
	// pulse's number, not its identity: the statusline rewrites both on every
	// turn, so an old file means an idle session either way — but the two can
	// drift apart, because a budget ages far more slowly than a round trip.
	SqueezeMaxAge = 15 * time.Minute
)

// The keys claudii's statusline writes, and where they come from: rate_5h and
// rate_7d are `.rate_limits.{five_hour,seven_day}.used_percentage` from Claude
// Code's own statusline JSON — percentages of the limit already spent, and
// they do run past 100 (the flight recorder's maximum over 424,509 readings is
// 107). reset_5h and reset_7d are the matching `resets_at`, unix seconds.
const (
	squeezeKey5h      = "rate_5h"
	squeezeKey7d      = "rate_7d"
	squeezeKeyReset5h = "reset_5h"
	squeezeKeyReset7d = "reset_7d"
)

// The two windows the limits are drawn on. 5h is the one that is felt — a
// cooldown really acts inside it — and 7d is the slow background nothing you
// do this afternoon repairs.
const (
	SqueezeWindow5h = 5 * time.Hour
	SqueezeWindow7d = 7 * 24 * time.Hour
)

// MaxSqueezePct bounds a stored percentage. Past twice the limit it is a
// parse gone wrong or a stopped counter, not a budget anybody spent.
const MaxSqueezePct = 200

// SqueezeEnabled reports whether the budget gets read at all. The same off
// switch shape as the pulse's, and for the same reason: everything wallii
// reads outside its own store needs one.
func SqueezeEnabled() bool { return !strings.EqualFold(os.Getenv(SqueezeEnv), "off") }

// Budget is one reading of how much of the rate limits is already spent. A
// zero Budget means nobody looked — Src is what separates that from a look
// that found an untouched budget, which is why the source is stored at all.
type Budget struct {
	At      time.Time // when the reading was taken: the file's own mtime
	Pct5h   float64   // percent of the five-hour limit already spent
	Pct7d   float64   // percent of the seven-day limit already spent
	Reset5h time.Time // when the five-hour window refills; zero when unknown
	Reset7d time.Time
	Src     string
}

// Known reports whether this Budget is a reading at all.
func (b Budget) Known() bool { return b.Src != "" }

// Fields are what a post stores. The order matches the event's: the seven-day
// share first, because it is the one that outlives the afternoon.
func (b Budget) Fields() (p7, p5 float64, src string) {
	if !b.Known() {
		return 0, 0, ""
	}
	return b.Pct7d, b.Pct5h, b.Src
}

// Elapsed5h says how far the five-hour window has already run: 0 at its
// start, 1 at the reset. Unknown reset — no file, an unparseable value —
// answers 0, so a reading never discounts itself by something it could not
// measure.
func (b Budget) Elapsed5h(now time.Time) float64 {
	return squeezeElapsed(now, b.Reset5h, SqueezeWindow5h)
}

// Elapsed7d is the same for the week.
func (b Budget) Elapsed7d(now time.Time) float64 {
	return squeezeElapsed(now, b.Reset7d, SqueezeWindow7d)
}

// squeezeElapsed turns a reset time into a position inside its window.
//
// This is the idea behind claudii's `pace=ahead|behind` — spending is only
// fast or slow against how much of the window is gone — but taken off the
// window's own clock instead of the session's. claudii compares the rate to
// how long THIS session has been running, which is the right question for a
// statusline watching one session and the wrong one here: the budget is the
// account's, a dozen sessions spend it at once, and a session started five
// minutes ago would read every limit as an emergency.
func squeezeElapsed(now, reset time.Time, window time.Duration) float64 {
	if reset.IsZero() {
		return 0
	}
	left := reset.Sub(now)
	if left < 0 {
		left = 0
	}
	if left > window {
		left = window
	}
	return 1 - float64(left)/float64(window)
}

// squeezeAnchors say how many steps of the five-value scale the budget
// presses at, for a given share of it already spent — pulseAnchors' shape,
// and calibrated the same way: on whether the real distribution reaches the
// steps at all. "A scale whose first step the data cannot reach is not
// measuring anything" is the mistake that scale made once, and this table was
// laid over the flight recorder before it was written down.
//
// Measured 2026-09-03 over the 33,768 recorded turns that carry both limits
// (every history-*.tsv on this machine, 150 days; the five-hour column alone
// reaches back over 424,509 of them, maximum 107 %): the share of turns above
// each step is
//
//	(>25 %)   5h 60.1 %   7d 70.4 %
//	(>50 %)   5h 33.5 %   7d 62.3 %
//	(>75 %)   5h 19.2 %   7d 38.1 %
//	(>90 %)   5h  7.4 %   7d 15.0 %
//
// so every step is reached, in both windows, by a real part of real turns —
// which is the whole test. The spacing tightens towards the top (25 points
// per step, then 25, then 15) because that is how a budget is felt: the
// stretch from 25 % to 50 % changes nothing anybody does, and the one from
// 75 % to 90 % changes what gets started. The floor lands on 90 %, past which
// the exact number has stopped mattering — what is left is rationing.
var squeezeAnchors = []struct{ upTo, press float64 }{
	{25, 0},
	{50, 1},
	{75, 2},
	{90, 3},
}

// SqueezeMaxPress is the most the budget can press, in steps of the mood
// scale — the same three the pulse's drag tops out at, so the two terms of
// the live reading are in one unit and can be read against each other.
const SqueezeMaxPress = 3

// SqueezeDensityUnknown is what a turn density nobody counted looks like. It
// is not zero: zero is a measured pause, and a pause is the thing that eases
// the pressure. Passed to Squeeze it applies no easing at all.
const SqueezeDensityUnknown = -1

// Squeeze is how hard a limit presses, in steps of the mood scale — pure, so
// the whole model is one expression a reader can check.
//
// Three inputs, each carrying one of the things the pressure actually depends
// on. pct is the level: how much of the budget is gone. elapsedFrac is where
// the window stands, because the same 90 % is one thing six hours before the
// reset and another six days before it. turnsPerHour is how hard the budget
// is being spent right now, and it is what makes the reading fall again after
// a pause without wallii storing a single byte of state: stop working, the
// density drops out of the last hour on its own, and the pressure eases.
//
// The three stay separate factors rather than one fitted number. Nobody has
// measured what a budget does to a mood — 203 posts with a limit reading
// beside them showed no visible relation, over two days and one reset
// boundary, which is not enough to decide anything. The point of this release
// is to store the readings so that question can be asked later; until then a
// shape whose parts can be read one at a time is worth more than a curve.
func Squeeze(pct, elapsedFrac, turnsPerHour float64) float64 {
	return squeezeLevel(pct) * squeezeEase(elapsedFrac) * squeezeHeat(turnsPerHour)
}

// squeezeLevel interpolates the anchors above. Continuous between them, like
// the pulse's drag and for the same reason: a stepped reading takes four
// positions and has no shape, and the difference between 62 % and 74 % spent
// is exactly the difference that would disappear.
func squeezeLevel(pct float64) float64 {
	prev := squeezeAnchors[0]
	if !(pct > prev.upTo) { // NaN lands here too, at no pressure
		return prev.press
	}
	for _, a := range squeezeAnchors[1:] {
		if pct <= a.upTo {
			return prev.press + (a.press-prev.press)*(pct-prev.upTo)/(a.upTo-prev.upTo)
		}
		prev = a
	}
	return SqueezeMaxPress
}

// squeezeEaseFloor is what is left of the pressure at the moment of the
// reset. Not zero: a window that is 100 % spent is spent, and the fact that
// it refills in four minutes does not give back the four minutes.
const squeezeEaseFloor = 0.25

// squeezeEase discounts the level by how much of the window has already run.
// Full weight at the start, the floor at the reset — the cooldown that
// actually helps is the one the clock is running towards.
func squeezeEase(elapsedFrac float64) float64 {
	if !(elapsedFrac > 0) { // NaN too: no measured position, no discount
		return 1
	}
	if elapsedFrac > 1 {
		elapsedFrac = 1
	}
	return 1 - (1-squeezeEaseFloor)*elapsedFrac
}

const (
	// squeezeHeatFull is the turn density at which the budget is being spent
	// about as hard as this machine ever spends it. Calibrated on the flight
	// recorder the same way the anchors are: over its 426,365 rows, the
	// number of rows in the hour before a row runs 289 at the lower quartile,
	// 588 at the median and 1023 at the upper — so a busy hour saturates and
	// an ordinary one sits around three quarters of the way up.
	squeezeHeatFull = 1000
	// squeezeHeatFloor is what survives a full hour of silence. Not zero:
	// 95 % spent is still 95 % spent after a walk around the block, and a
	// reading that went quiet with the keyboard would be measuring the
	// keyboard.
	squeezeHeatFloor = 0.4
)

// squeezeHeat scales the level by how fast turns are coming.
func squeezeHeat(turnsPerHour float64) float64 {
	switch {
	case turnsPerHour < 0: // SqueezeDensityUnknown — nothing counted, nothing discounted
		return 1
	case !(turnsPerHour > 0): // an hour with nothing in it, NaN included
		return squeezeHeatFloor
	case turnsPerHour >= squeezeHeatFull:
		return 1
	}
	return squeezeHeatFloor + (1-squeezeHeatFloor)*turnsPerHour/squeezeHeatFull
}

// SessionBudget reads how much of the limits is already spent. Missing,
// stale, switched off and unparseable are one answer — no reading — because a
// number wallii cannot stand behind is worse than none, and worse here than
// anywhere else: this one would be read back later as evidence.
func SessionBudget(now time.Time) Budget {
	if !SqueezeEnabled() {
		return Budget{}
	}
	path := strings.TrimSpace(os.Getenv(SqueezeFileEnv))
	if path == "" {
		path = statuslineCache()
	}
	if path == "" {
		return Budget{}
	}
	fi, err := os.Stat(path)
	if err != nil || now.Sub(fi.ModTime()) > SqueezeMaxAge {
		return Budget{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Budget{}
	}
	bud, ok := parseBudget(string(b))
	if !ok {
		return Budget{}
	}
	// the file's own mtime is the reading's time, as with the pulse: the
	// limits were what they were when the statusline last rendered
	bud.At, bud.Src = fi.ModTime(), SqueezeSession
	return bud
}

// parseBudget reads the key=value lines the statusline writes. Both
// percentages have to be there — one window is half a reading, and the two
// are always written together — while the resets are optional: without them
// the level still stands, it just carries no position inside its window.
func parseBudget(s string) (Budget, bool) {
	var b Budget
	var have5h, have7d bool
	for _, line := range strings.Split(s, "\n") {
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(k) {
		case squeezeKey5h:
			b.Pct5h, have5h = squeezePct(v)
		case squeezeKey7d:
			b.Pct7d, have7d = squeezePct(v)
		case squeezeKeyReset5h:
			b.Reset5h = squeezeStamp(v)
		case squeezeKeyReset7d:
			b.Reset7d = squeezeStamp(v)
		}
	}
	return b, have5h && have7d
}

// squeezePct parses one percentage. Non-finite values are rejected rather
// than clamped: strconv reads "NaN" happily, and a NaN in a stored field
// would poison every average taken over it afterwards.
func squeezePct(v string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > MaxSqueezePct {
		return 0, false
	}
	return f, true
}

// squeezeStamp parses a reset time, unix seconds. Zero means unknown.
func squeezeStamp(v string) time.Time {
	sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

const (
	// squeezeTailBytes bounds what the flight recorder costs to read. The
	// file is one row per statusline render and runs to megabytes a month;
	// this reading happens where a person is waiting, so it reads the end of
	// it and never the whole thing. A row is around 115 bytes, so this covers
	// some two thousand of them — more than a busy hour holds.
	squeezeTailBytes = 256 << 10
	// squeezeDensityWindow is how far back the density looks. An hour, so a
	// break long enough to be a break shows up and a walk to the kettle does
	// not.
	squeezeDensityWindow = time.Hour
	// squeezeDensityMinSpan floors the divisor when the tail did not reach
	// back a whole window. Without it a truncated read of a burst would
	// divide a few dozen rows by a few seconds and report a density nothing
	// on this machine has ever produced.
	squeezeDensityMinSpan = 5 * time.Minute
)

// TurnDensity reports how fast turns are coming, per hour, from claudii's
// flight recorder — the account's, not this session's, because the budget it
// is spending is the account's too.
//
// Reports SqueezeDensityUnknown when there is no recorder to read: a machine
// without claudii still gets a level and a position, just no easing.
func TurnDensity(now time.Time) float64 {
	dir := claudiiCacheDir()
	if dir == "" {
		return SqueezeDensityUnknown
	}
	path := filepath.Join(dir, "history-"+now.Format("2006-01")+".tsv")
	stamps, fromStart, err := recorderTail(path, squeezeTailBytes)
	if err != nil || len(stamps) == 0 {
		return SqueezeDensityUnknown
	}
	return turnsPerHour(now, stamps, fromStart)
}

// recorderTail reads at most maxBytes from the end of the flight recorder and
// returns the unix timestamp of every whole row in it, oldest first. The
// second return says the read reached the start of the file, which is what
// tells a quiet hour from a tail too short to have seen the whole hour.
func recorderTail(path string, maxBytes int64) ([]int64, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	off := int64(0)
	if fi.Size() > maxBytes {
		off = fi.Size() - maxBytes
	}
	buf := make([]byte, fi.Size()-off)
	n, err := f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	buf = buf[:n]
	if off > 0 {
		// the first row in the buffer is a fragment of one — dropping it is
		// why the caller is told whether the read started at the file's head
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}
	var out []int64
	for _, line := range strings.Split(string(buf), "\n") {
		field, _, _ := strings.Cut(line, "\t")
		sec, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64)
		if err != nil || sec <= 0 {
			continue // a half-written row is skipped and never fatal
		}
		out = append(out, sec)
	}
	return out, off == 0, nil
}

// turnsPerHour counts the rows of the last window and divides. Where the tail
// was too short to cover the whole window, it divides by what it did cover
// instead of pretending the missing part was quiet — a truncated read only
// ever happens under a load high enough to fill a quarter megabyte in under
// an hour, which is exactly when reporting a low density would be worst.
func turnsPerHour(now time.Time, stamps []int64, fromStart bool) float64 {
	cutoff := now.Add(-squeezeDensityWindow).Unix()
	n := 0
	for _, s := range stamps {
		if s >= cutoff {
			n++
		}
	}
	span := squeezeDensityWindow
	if !fromStart && len(stamps) > 0 && stamps[0] > cutoff {
		span = now.Sub(time.Unix(stamps[0], 0))
		if span < squeezeDensityMinSpan {
			span = squeezeDensityMinSpan
		}
	}
	return float64(n) / span.Hours()
}
