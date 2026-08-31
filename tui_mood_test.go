// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bmmmm/wallii/internal/wall"
)

func moodPosts(moods ...string) []wall.Event {
	ts := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	evs := make([]wall.Event, 0, len(moods))
	for i, md := range moods {
		evs = append(evs, wall.Event{TS: ts.Add(time.Duration(i) * time.Minute),
			Repo: "alpha", Actor: "worker", Topic: "fix", Msg: "work", Mood: md})
	}
	return evs
}

// line returns the panel row whose label column names lvl.
func line(t *testing.T, panel, lvl string) string {
	t.Helper()
	for _, l := range strings.Split(panel, "\n") {
		if strings.HasPrefix(l, "  "+lvl) && strings.Contains(l, "┤") {
			return l
		}
	}
	t.Fatalf("no %q row in panel:\n%s", lvl, panel)
	return ""
}

// The curve marks each post at its own level. Bars filled from the floor were
// the first draft: a wall that sits at good/ok turns the bottom rows into one
// solid block, and only the top edge says anything.
func TestMoodPanelDrawsCurveNotFilledBars(t *testing.T) {
	s := wall.MoodTrail(moodPosts("great"), 0)
	panel := renderMood(s, 60, 24, 99, 0, "")
	if !strings.Contains(line(t, panel, "great"), moodBar) {
		t.Errorf("great row carries no mark:\n%s", panel)
	}
	for _, lvl := range []string{"good", "ok", "rough", "stuck"} {
		if strings.Contains(line(t, panel, lvl), moodBar) {
			t.Errorf("%s row is filled under a great post — the curve must not fill downward:\n%s", lvl, panel)
		}
	}
}

func TestMoodPanelRevealSweepsIn(t *testing.T) {
	s := wall.MoodTrail(moodPosts(strings.Split(strings.Repeat("ok ", 30), " ")...), 0)
	bars := func(frame int) int {
		return strings.Count(renderMood(s, 60, 24, frame, 0, ""), moodBar)
	}
	first, mid, done := bars(0), bars(moodRevealFrames/2), bars(moodRevealFrames)
	if !(first < mid && mid < done) {
		t.Errorf("sweep = %d → %d → %d marks, want strictly growing", first, mid, done)
	}
	if bars(moodRevealFrames*4) != done {
		t.Errorf("series keeps growing after the reveal: %d, want %d", bars(moodRevealFrames*4), done)
	}
}

func TestMoodPanelFaceBlinks(t *testing.T) {
	s := wall.MoodTrail(moodPosts("good", "good"), 0)
	open := renderMood(s, 60, 24, moodBlinkFrames+1, 0, "")
	blink := renderMood(s, 60, 24, 0, 0, "")
	if !strings.Contains(open, moodFaces[1].open) {
		t.Errorf("open frame shows no open face:\n%s", open)
	}
	if !strings.Contains(blink, moodFaces[1].blink) {
		t.Errorf("blink frame shows no closed eyes:\n%s", blink)
	}
}

// The scale's ends are drawn with closed eyes already, so the blink falls
// only where there is an eye to close.
func TestMoodFaceClosedEyesHoldStill(t *testing.T) {
	for _, avg := range []float64{5, 1} {
		if faceFor(avg, true) != faceFor(avg, false) {
			t.Errorf("face at %.0f blinks: %q vs %q", avg, faceFor(avg, true), faceFor(avg, false))
		}
	}
	for _, avg := range []float64{4, 3, 2} {
		if faceFor(avg, true) == faceFor(avg, false) {
			t.Errorf("face at %.0f never blinks: %q", avg, faceFor(avg, false))
		}
	}
}

// One face per value on the scale, picked the same way moodWord picks its
// word — the panel can never show a grinning face over the word "stuck".
func TestMoodFacesCoverTheScale(t *testing.T) {
	if len(moodFaces) != len(wall.Moods) {
		t.Fatalf("%d faces for %d moods", len(moodFaces), len(wall.Moods))
	}
	for i := range wall.Moods {
		avg := float64(len(wall.Moods) - i) // great → 5 … stuck → 1
		if moodIndex(avg) != i {
			t.Errorf("avg %.0f maps to %q, want %q", avg, wall.Moods[moodIndex(avg)], wall.Moods[i])
		}
	}
}

func TestMoodPanelNamesAnUngradedWall(t *testing.T) {
	evs := []wall.Event{{TS: time.Now(), Repo: "alpha", Msg: "no grade"}}
	panel := renderMood(wall.MoodTrail(evs, 0), 60, 24, 3, 0, "")
	if !strings.Contains(panel, moodFaceNone) {
		t.Errorf("ungraded wall gets a mood face:\n%s", panel)
	}
	if strings.Contains(panel, moodBar) {
		t.Errorf("ungraded wall draws a curve out of nothing:\n%s", panel)
	}
	if !strings.Contains(panel, "no mood on any of these 1 posts") {
		t.Errorf("panel does not say the wall is ungraded:\n%s", panel)
	}
}

func TestMoodNoteReportsCalibration(t *testing.T) {
	cases := []struct {
		name  string
		moods []string
		want  string
	}{
		{"flat", []string{"good", "good", "good"}, "a flat line is not a measurement"},
		{"never low", []string{"great", "good", "ok"}, "bottom half of the scale is unused"},
		{"honest", []string{"great", "ok", "stuck"}, ""},
	}
	for _, c := range cases {
		if got := moodNote(wall.MoodTrail(moodPosts(c.moods...), 0)); !strings.Contains(got, c.want) || (c.want == "" && got != "") {
			t.Errorf("%s: note = %q, want %q", c.name, got, c.want)
		}
	}
}

// Coverage is a finding too: a curve drawn from a fifth of the wall must say so.
func TestMoodNoteReportsThinCoverage(t *testing.T) {
	evs := append(moodPosts("great", "ok", "stuck"),
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "a"},
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "b"},
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "c"},
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "d"})
	if got := moodNote(wall.MoodTrail(evs, 0)); !strings.Contains(got, "3 of 7 posts carry a mood") {
		t.Errorf("note = %q, want the coverage gap named", got)
	}
}

func TestMoodPanelFitsTheWindow(t *testing.T) {
	s := wall.MoodTrail(moodPosts(strings.Split(strings.Repeat("ok ", 40), " ")...), 0)
	for _, h := range []int{8, 11, 14, 16, 20, 24, 26, 40} {
		panel := renderMood(s, 80, h, 20, 0, "")
		if got := strings.Count(panel, "\n") + 1; got > h {
			t.Errorf("h=%d renders %d lines:\n%s", h, got, panel)
		}
		if !strings.Contains(panel, "m/esc back") {
			t.Errorf("h=%d drops the way out of the panel:\n%s", h, panel)
		}
	}
}

// The calibration note is the panel's most valuable line and the last one
// laid out, so it is what an over-generous row budget silently costs.
func TestMoodPanelKeepsTheNoteAtEveryHeight(t *testing.T) {
	s := wall.MoodTrail(moodPosts("good", "good", "good"), 0)
	for _, h := range []int{16, 20, 21, 24, 26, 30, 40} {
		if panel := renderMood(s, 80, h, 20, 0, ""); !strings.Contains(panel, "flat line") {
			t.Errorf("h=%d drops the calibration note:\n%s", h, panel)
		}
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// The panel's clock runs only while the panel is open: leaving it must not
// leave a 10fps tick running behind the list.
func TestMoodClockStopsWhenThePanelCloses(t *testing.T) {
	m := newTUI(t.TempDir(), moodPosts("good", "ok"))
	m.width, m.height = 80, 24
	if _, cmd := m.handleKey(key("m")); cmd == nil {
		t.Fatal("m does not start the panel clock")
	}
	if m.mode != modeMood {
		t.Fatalf("mode = %q after m, want %q", m.mode, modeMood)
	}
	if m.moodFlash != 0 {
		t.Errorf("opening the panel flashes a column (%d) — that marks an arrival, not a visit", m.moodFlash)
	}
	_, cmd := m.Update(moodTickMsg{epoch: m.moodEpoch})
	if cmd == nil || m.moodFrame != 1 {
		t.Fatalf("tick did not advance the animation: frame %d, cmd %v", m.moodFrame, cmd)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList {
		t.Fatalf("esc left mode %q", m.mode)
	}
	if _, cmd := m.Update(moodTickMsg{epoch: m.moodEpoch}); cmd != nil {
		t.Error("the clock keeps ticking after the panel closed")
	}
}

// Reopening must not leave two clocks running — the older tick is orphaned
// by its epoch rather than doubling the frame rate.
func TestMoodClockOrphansStaleTicks(t *testing.T) {
	m := newTUI(t.TempDir(), moodPosts("good"))
	m.width, m.height = 80, 24
	m.handleKey(key("m"))
	stale := m.moodEpoch
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m.handleKey(key("m"))
	if _, cmd := m.Update(moodTickMsg{epoch: stale}); cmd != nil {
		t.Error("a tick from the previous visit still schedules frames")
	}
	if m.moodFrame != 0 {
		t.Errorf("stale tick advanced the animation to frame %d", m.moodFrame)
	}
}

// The panel reads the wall the reader filtered down to, not the whole store.
func TestMoodPanelFollowsTheListFilter(t *testing.T) {
	evs := moodPosts("stuck", "stuck")
	evs = append(evs, wall.Event{TS: time.Now(), Repo: "beta", Actor: "worker", Msg: "other", Mood: "great"})
	m := newTUI(t.TempDir(), evs)
	m.width, m.height = 80, 24
	m.repoF = "beta"
	m.refilter()
	m.handleKey(key("m"))
	if m.moodTrail.Count != 1 || m.moodTrail.Avg != 5 {
		t.Errorf("trail = %d posts, avg %.1f, want the one beta post at 5.0", m.moodTrail.Count, m.moodTrail.Avg)
	}
}

// A lit column must still be a column. Reverse video paints the glyph in the
// background color, and a full block covers its whole cell, so a reversed
// block is an invisible one — the lit cell has to be a reversed space, which
// keeps the width but drops one block from the row.
func TestMoodFlashLightsTheNewestColumn(t *testing.T) {
	s := wall.MoodTrail(moodPosts("ok", "ok", "ok"), 0)
	const h = 20 // one row per level, so the flash costs exactly one block
	calm := renderMood(s, 60, h, 99, 0, "")
	lit := renderMood(s, 60, h, 99, moodFlashFrames, "")
	if got, want := strings.Count(lit, moodBar), strings.Count(calm, moodBar)-1; got != want {
		t.Errorf("lit panel has %d blocks, want %d — the newest column is not lit", got, want)
	}
	if a, b := len([]rune(line(t, calm, "ok"))), len([]rune(line(t, lit, "ok"))); a != b {
		t.Errorf("lighting a column changed the row width: %d vs %d", a, b)
	}
}

// Below moodRevealFrames points a floored sweep opens on an empty graph.
func TestMoodPanelRevealShowsSomethingOnTheFirstFrame(t *testing.T) {
	for n := 1; n < moodRevealFrames; n++ {
		s := wall.MoodTrail(moodPosts(strings.Fields(strings.Repeat("ok ", n))...), 0)
		if panel := renderMood(s, 60, 20, 0, 0, ""); !strings.Contains(panel, moodBar) {
			t.Errorf("%d points: frame 0 draws nothing:\n%s", n, panel)
		}
	}
}
