// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// wallii post times the API now, so the suite would otherwise reach the
// network once per post — slow where it works, and a different test where it
// does not. Probing is off for the package; the tests that are about the
// pulse hand over a value of their own instead.
func TestMain(m *testing.M) {
	os.Setenv("WALLII_PULSE", "off")
	os.Exit(m.Run())
}

// readWall returns every post in dir, oldest first.
func readWall(t *testing.T, dir string) []wall.Event {
	t.Helper()
	evs, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestPostCarriesTheSessionsOwnNumber(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_PULSE", "")
	t.Setenv("WALLII_PULSE_MS", "1830")

	if err := cmdPost([]string{"-r", "x", "-t", "fix", "the session knows what its turns cost"}); err != nil {
		t.Fatal(err)
	}
	e := readWall(t, dir)[0]
	if e.PulseMS != 1830 || e.PulseSrc != wall.PulseSession {
		t.Errorf("post carries %dms from %q, want 1830ms from %q", e.PulseMS, e.PulseSrc, wall.PulseSession)
	}
}

func TestPostRecordsAnOutageAsSuch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_PULSE", "")
	t.Setenv("WALLII_PULSE_MS", "none")

	if err := cmdPost([]string{"-r", "x", "-t", "fix", "posted while the api was gone"}); err != nil {
		t.Fatal(err)
	}
	e := readWall(t, dir)[0]
	if e.PulseSrc != wall.PulseNone {
		t.Errorf("pulse_src = %q, want %q — an outage is a reading, not an absence", e.PulseSrc, wall.PulseNone)
	}
	if got := eventPulseTerm(e); got != "no api" {
		t.Errorf("the row reads %q, want %q", got, "no api")
	}
}

// The off switch has to reach the stored line, not just the socket: a post
// written with probing off must be indistinguishable from one written before
// the field existed, because in both cases nobody measured.
func TestPostWithoutProbingCarriesNoReading(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_PULSE", "off")
	t.Setenv("WALLII_PULSE_MS", "1830") // even a value on offer is not taken

	if err := cmdPost([]string{"-r", "x", "-t", "fix", "offline post"}); err != nil {
		t.Fatal(err)
	}
	e := readWall(t, dir)[0]
	if e.PulseMS != 0 || e.PulseSrc != "" {
		t.Errorf("post carries %dms from %q, want nothing at all", e.PulseMS, e.PulseSrc)
	}
	if got := eventPulseTerm(e); got != "" {
		t.Errorf("an unmeasured post renders %q, want silence", got)
	}
}

// A grade earned in a four-second API is not the same grade. The stats line
// says so, and names what the wait costs the scale.
func TestAPILineReportsTheConditions(t *testing.T) {
	got := apiLine(wall.Stats{PulseAnswered: 4, PulseTotalMS: 16_000, PulseDown: 2})
	for _, want := range []string{"4s average over 4 posts", "takes 2.0 off a mood", "2 written with no api"} {
		if !strings.Contains(got, want) {
			t.Errorf("api line %q is missing %q", got, want)
		}
	}
	if fast := apiLine(wall.Stats{PulseAnswered: 9, PulseTotalMS: 1_800}); strings.Contains(fast, "off a mood") {
		t.Errorf("a fast api claimed a drag: %q", fast)
	}
	if one := apiLine(wall.Stats{PulseAnswered: 1, PulseTotalMS: 157}); !strings.Contains(one, "over 1 post") || strings.Contains(one, "1 posts") {
		t.Errorf("api line %q counts one post as several", one)
	}
	if none := apiLine(wall.Stats{PulseDown: 3}); !strings.Contains(none, "nothing answered across 3 posts") {
		t.Errorf("all-down window reads %q", none)
	}
	// Most of the wall predates the field. Zero coverage is not a fast API.
	if quiet := apiLine(wall.Stats{Posts: 400}); quiet != "" {
		t.Errorf("a wall with no readings printed %q", quiet)
	}
}

func TestMoodInspectorNamesTheConditions(t *testing.T) {
	evs := moodPosts("good", "rough")
	evs[0].PulseMS, evs[0].PulseSrc = 180, wall.PulseProbe
	evs[1].PulseSrc = wall.PulseNone
	st := moodStateOf(evs, 99)

	st.cursor = 0
	if got := moodInspect(st, 120); !strings.Contains(got, "api 180ms") {
		t.Errorf("inspector = %q, want the round trip that post was written in", got)
	}
	st.cursor = 1
	if got := moodInspect(st, 120); !strings.Contains(got, "no api") {
		t.Errorf("inspector = %q, want the outage named", got)
	}
}

// A day column folds many posts: the mean over the ones that were measured,
// and a count of the ones written with nothing answering.
func TestMoodDayColumnFoldsTheConditions(t *testing.T) {
	evs := moodPosts("good", "good", "rough")
	evs[0].PulseMS, evs[0].PulseSrc = 200, wall.PulseProbe
	evs[1].PulseMS, evs[1].PulseSrc = 600, wall.PulseProbe
	evs[2].PulseSrc = wall.PulseNone
	st := moodStateOf(evs, 99)
	st.daily = true
	st.refold()
	st.cursor = 0

	p, _ := st.at()
	if p.PulseMS != 400 || p.PulseN != 2 || p.PulseDown != 1 {
		t.Fatalf("day folds to %dms over %d readings, %d down; want 400ms over 2, 1 down", p.PulseMS, p.PulseN, p.PulseDown)
	}
	if got := moodInspect(st, 120); !strings.Contains(got, "api ~400ms") || !strings.Contains(got, "1 with none") {
		t.Errorf("day inspector = %q, want the mean and the outage count", got)
	}
}

func TestEventPulseTermRoundsLikeTheRestOfThePanel(t *testing.T) {
	e := wall.Event{PulseMS: 2840, PulseSrc: wall.PulseProbe}
	if got := eventPulseTerm(e); got != "api 2.8s" {
		t.Errorf("eventPulseTerm = %q, want %q", got, "api 2.8s")
	}
	if got := pulseDur(1830 * time.Millisecond); got != "1.8s" {
		t.Errorf("pulseDur = %q, want %q", got, "1.8s")
	}
}
