// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bmmmm/wallii/internal/wall"
)

func moodPosts(moods ...string) []wall.Event {
	ts := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
	evs := make([]wall.Event, 0, len(moods))
	for i, md := range moods {
		evs = append(evs, wall.Event{TS: ts.Add(time.Duration(i) * time.Minute),
			Repo: "alpha", Actor: "worker", Topic: "fix", Msg: "work", Mood: md, Outcome: wall.OutcomeOK})
	}
	return evs
}

// moodStateOf builds the panel state a renderer test needs: loaded, folded,
// no cursor, parked at a frame past the sweep unless told otherwise.
func moodStateOf(evs []wall.Event, frame int) moodState {
	st := moodState{cursor: moodNoCursor, frame: frame}
	st.load(evs)
	return st
}

func render(st moodState, w, h int) string { return renderMood(st, w, h, "", "") }

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
	panel := render(moodStateOf(moodPosts("great"), 99), 60, 26)
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
	evs := moodPosts(strings.Fields(strings.Repeat("ok ", 30))...)
	bars := func(frame int) int { return strings.Count(render(moodStateOf(evs, frame), 60, 26), moodBar) }
	first, mid, done := bars(0), bars(moodRevealFrames/2), bars(moodRevealFrames)
	if !(first < mid && mid < done) {
		t.Errorf("sweep = %d → %d → %d marks, want strictly growing", first, mid, done)
	}
	if bars(moodRevealFrames*4) != done {
		t.Errorf("series keeps growing after the reveal: %d, want %d", bars(moodRevealFrames*4), done)
	}
}

// Below moodRevealFrames points a floored sweep opens on an empty graph.
func TestMoodPanelRevealShowsSomethingOnTheFirstFrame(t *testing.T) {
	for n := 1; n < moodRevealFrames; n++ {
		st := moodStateOf(moodPosts(strings.Fields(strings.Repeat("ok ", n))...), 0)
		if panel := render(st, 60, 20); !strings.Contains(panel, moodBar) {
			t.Errorf("%d points: frame 0 draws nothing:\n%s", n, panel)
		}
	}
}

func TestMoodPanelFaceBlinks(t *testing.T) {
	evs := moodPosts("good", "good")
	if open := render(moodStateOf(evs, moodBlinkFrames+1), 60, 26); !strings.Contains(open, moodFaces[1].open) {
		t.Errorf("open frame shows no open face:\n%s", open)
	}
	if blink := render(moodStateOf(evs, 0), 60, 26); !strings.Contains(blink, moodFaces[1].blink) {
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
	st := moodStateOf([]wall.Event{{TS: time.Now(), Repo: "alpha", Msg: "no grade"}}, 3)
	panel := render(st, 60, 26)
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
		got := moodNote(wall.MoodTrail(moodPosts(c.moods...)))
		if !strings.Contains(got, c.want) || (c.want == "" && got != "") {
			t.Errorf("%s: note = %q, want %q", c.name, got, c.want)
		}
	}
}

// Coverage is a finding too: a curve drawn from a fifth of the wall says so.
func TestMoodNoteReportsThinCoverage(t *testing.T) {
	evs := append(moodPosts("great", "ok", "stuck"),
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "a"},
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "b"},
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "c"},
		wall.Event{TS: time.Now(), Repo: "alpha", Msg: "d"})
	if got := moodNote(wall.MoodTrail(evs)); !strings.Contains(got, "3 of 7 posts carry a mood") {
		t.Errorf("note = %q, want the coverage gap named", got)
	}
}

func TestMoodPanelFitsTheWindow(t *testing.T) {
	st := moodStateOf(moodPosts(strings.Fields(strings.Repeat("ok ", 40))...), 20)
	st.moveCursor(-1) // inspector open: the tallest the panel ever gets
	for _, h := range []int{8, 11, 14, 18, 20, 24, 26, 40} {
		panel := render(st, 80, h)
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
	st := moodStateOf(moodPosts("good", "good", "good"), 20)
	st.moveCursor(-1)
	for _, h := range []int{18, 20, 22, 24, 26, 30, 40} {
		if panel := render(st, 80, h); !strings.Contains(panel, "flat line") {
			t.Errorf("h=%d drops the calibration note:\n%s", h, panel)
		}
	}
}

// A lit column must still be a column. Reverse video paints the glyph in the
// background color, and a full block covers its whole cell, so a reversed
// block is an invisible one — the lit cell has to be a reversed space, which
// keeps the width but drops one block from the row.
func TestMoodFlashLightsTheNewestColumn(t *testing.T) {
	const h = 20 // one row per level, so the flash costs exactly one block
	calm := moodStateOf(moodPosts("ok", "ok", "ok"), 99)
	lit := calm
	lit.flash = moodFlashFrames
	a, b := render(calm, 60, h), render(lit, 60, h)
	if got, want := strings.Count(b, moodBar), strings.Count(a, moodBar)-1; got != want {
		t.Errorf("lit panel has %d blocks, want %d — the newest column is not lit", got, want)
	}
	if x, y := len([]rune(line(t, a, "ok"))), len([]rune(line(t, b, "ok"))); x != y {
		t.Errorf("lighting a column changed the row width: %d vs %d", x, y)
	}
}

// ---- outcome band, contradictions, inspector, actors, window ----

// mood is the height, outcome is the row below it, under one axis. A great
// mood over a failed outcome is the most interesting column on the wall, and
// neither half shows it alone.
func TestMoodPanelBandCarriesOutcome(t *testing.T) {
	evs := moodPosts("great", "good")
	evs[1].Outcome = wall.OutcomeFailed
	panel := render(moodStateOf(evs, 99), 60, 26)
	var band string
	for _, l := range strings.Split(panel, "\n") {
		if strings.HasPrefix(l, "  out") {
			band = l
		}
	}
	if band == "" {
		t.Fatalf("no outcome band:\n%s", panel)
	}
	ok, failed := outcomeGlyphOf(wall.OutcomeOK), outcomeGlyphOf(wall.OutcomeFailed)
	if !strings.Contains(band, ok) || !strings.Contains(band, failed) {
		t.Errorf("band = %q, want both %q and %q", band, ok, failed)
	}
}

func outcomeGlyphOf(o string) string { g, _ := outcomeGlyph(o); return g }

// A post whose message disagrees with its own grade is marked at the height
// the grade claims — that is the claim being doubted.
func TestMoodPanelMarksContradictions(t *testing.T) {
	evs := moodPosts("great")
	evs[0].Msg = "der native Pfad war eine Sackgasse, der Shim tut es"
	panel := render(moodStateOf(evs, 99), 60, 26)
	row := line(t, panel, "great")
	if !strings.Contains(row, moodContra) {
		t.Errorf("contradicting post is not marked:\n%s", panel)
	}
	if strings.Contains(row, moodBar) {
		t.Errorf("marked post also draws a plain block: %q", row)
	}
	if !strings.Contains(panel, "1 "+moodContra) {
		t.Errorf("legend does not count the contradiction:\n%s", panel)
	}
}

func TestMoodInspectorNamesThePost(t *testing.T) {
	evs := moodPosts("good", "rough")
	evs[1].Msg = "the one under the cursor"
	evs[1].Repo, evs[1].Topic = "beta", "deps"
	st := moodStateOf(evs, 99)
	if moodInspect(st, 100) != "" {
		t.Error("inspector speaks without a cursor")
	}
	st.moveCursor(-1) // first step lands on the newest
	got := moodInspect(st, 200)
	for _, want := range []string{"the one under the cursor", "beta", "deps", "rough"} {
		if !strings.Contains(got, want) {
			t.Errorf("inspector = %q, want it to name %q", got, want)
		}
	}
}

// A day column folds many posts, so the inspector describes the day.
func TestMoodInspectorDescribesAFoldedDay(t *testing.T) {
	d := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
	evs := []wall.Event{
		{TS: d, Repo: "alpha", Msg: "a", Mood: "great", Outcome: wall.OutcomeOK},
		{TS: d.Add(time.Hour), Repo: "alpha", Msg: "b", Mood: "ok", Outcome: wall.OutcomeFailed},
	}
	st := moodState{cursor: moodNoCursor, frame: 99, daily: true}
	st.load(evs)
	if len(st.drawn) != 1 {
		t.Fatalf("daily fold produced %d columns, want 1", len(st.drawn))
	}
	st.moveCursor(-1)
	got := moodInspect(st, 200)
	for _, want := range []string{"2 posts", "worst " + outcomeGlyphOf(wall.OutcomeFailed)} {
		if !strings.Contains(got, want) {
			t.Errorf("day inspector = %q, want it to say %q", got, want)
		}
	}
}

func TestMoodDailyToggleRefolds(t *testing.T) {
	d := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
	var evs []wall.Event
	for i := 0; i < 6; i++ { // three posts a day across two days
		evs = append(evs, wall.Event{TS: d.Add(time.Duration(i/3)*24*time.Hour + time.Duration(i)*time.Hour),
			Repo: "alpha", Msg: "x", Mood: "good"})
	}
	st := moodStateOf(evs, 99)
	if len(st.drawn) != 6 {
		t.Fatalf("post resolution = %d columns, want 6", len(st.drawn))
	}
	st.daily = true
	st.refold()
	if len(st.drawn) != 2 {
		t.Fatalf("day resolution = %d columns, want 2", len(st.drawn))
	}
	if got := render(st, 80, 26); !strings.Contains(got, "· 2 days") {
		t.Errorf("header does not say the columns are days:\n%s", got)
	}
}

func TestMoodActorLinesOnePerActor(t *testing.T) {
	evs := moodPosts("good", "great", "ok")
	evs[2].Actor = "other"
	st := moodStateOf(evs, 99)
	st.byActor = true
	panel := render(st, 80, 26)
	for _, want := range []string{"worker", "other", "by actor"} {
		if !strings.Contains(panel, want) {
			t.Errorf("actor view does not name %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "┤") {
		t.Errorf("actor view still draws the shared curve:\n%s", panel)
	}
	if !strings.ContainsAny(panel, string(moodSpark)) {
		t.Errorf("actor view draws no sparkline:\n%s", panel)
	}
}

func TestSparkRuneSpansTheScale(t *testing.T) {
	lo, hi := sparkRune(1), sparkRune(len(wall.Moods))
	if lo != moodSpark[0] || hi != moodSpark[len(moodSpark)-1] {
		t.Errorf("scale ends map to %q…%q, want %q…%q", lo, hi, moodSpark[0], moodSpark[len(moodSpark)-1])
	}
	for s := 2; s <= len(wall.Moods); s++ {
		if sparkRune(s) <= sparkRune(s-1) {
			t.Errorf("spark does not rise from score %d to %d", s-1, s)
		}
	}
}

func TestWindowStart(t *testing.T) {
	now := time.Date(2026, 3, 4, 15, 30, 0, 0, time.Local)
	if got := windowStart("", now); !got.IsZero() {
		t.Errorf("no window = %v, want the zero time (everything)", got)
	}
	if got := windowStart("today", now); got.Hour() != 0 || got.Day() != 4 {
		t.Errorf("today = %v, want midnight of the 4th", got)
	}
	if got := windowStart("7d", now); got != now.AddDate(0, 0, -7) {
		t.Errorf("7d = %v, want %v", got, now.AddDate(0, 0, -7))
	}
	if got := windowStart("30d", now); got != now.AddDate(0, 0, -30) {
		t.Errorf("30d = %v, want %v", got, now.AddDate(0, 0, -30))
	}
}

// One window bounds both views: what you are looking at and what the curve
// measures cannot drift apart.
func TestWindowBoundsListAndPanelTogether(t *testing.T) {
	now := time.Now()
	evs := []wall.Event{
		{TS: now.AddDate(0, 0, -20), Repo: "alpha", Msg: "old", Mood: "stuck"},
		{TS: now.Add(-time.Hour), Repo: "alpha", Msg: "fresh", Mood: "great"},
	}
	m := newTUI(t.TempDir(), evs)
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	if m.mood.trail.Count != 2 {
		t.Fatalf("panel starts with %d points, want both", m.mood.trail.Count)
	}
	m.handleMoodKey(key("2")) // 7d
	if m.window != "7d" {
		t.Fatalf("window = %q, want 7d", m.window)
	}
	if len(m.view) != 1 {
		t.Errorf("list shows %d posts, want 1 inside the window", len(m.view))
	}
	if m.mood.trail.Count != 1 || m.mood.trail.Avg != 5 {
		t.Errorf("panel = %d points avg %.1f, want the one fresh post at 5.0", m.mood.trail.Count, m.mood.trail.Avg)
	}
	m.handleMoodKey(key("0")) // back to everything
	if m.mood.trail.Count != 2 {
		t.Errorf("clearing the window did not restore the series: %d points", m.mood.trail.Count)
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// The panel's clock runs only while the panel is open: leaving it must not
// leave a 10fps tick running behind the list.
func TestMoodClockStopsWhenThePanelCloses(t *testing.T) {
	m := newTUI(t.TempDir(), moodPosts("good", "ok"))
	m.width, m.height = 80, 26
	if _, cmd := m.handleKey(key("m")); cmd == nil {
		t.Fatal("m does not start the panel clock")
	}
	if m.mode != modeMood {
		t.Fatalf("mode = %q after m, want %q", m.mode, modeMood)
	}
	if m.mood.flash != 0 {
		t.Errorf("opening the panel flashes a column (%d) — that marks an arrival, not a visit", m.mood.flash)
	}
	_, cmd := m.Update(moodTickMsg{epoch: m.mood.epoch})
	if cmd == nil || m.mood.frame != 1 {
		t.Fatalf("tick did not advance the animation: frame %d, cmd %v", m.mood.frame, cmd)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList {
		t.Fatalf("esc left mode %q", m.mode)
	}
	if _, cmd := m.Update(moodTickMsg{epoch: m.mood.epoch}); cmd != nil {
		t.Error("the clock keeps ticking after the panel closed")
	}
}

// Reopening must not leave two clocks running — the older tick is orphaned by
// its epoch rather than doubling the frame rate.
func TestMoodClockOrphansStaleTicks(t *testing.T) {
	m := newTUI(t.TempDir(), moodPosts("good"))
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	stale := m.mood.epoch
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m.handleKey(key("m"))
	if _, cmd := m.Update(moodTickMsg{epoch: stale}); cmd != nil {
		t.Error("a tick from the previous visit still schedules frames")
	}
	if m.mood.frame != 0 {
		t.Errorf("stale tick advanced the animation to frame %d", m.mood.frame)
	}
}

// esc peels one layer at a time: the inspector before the panel.
func TestMoodEscDropsCursorBeforePanel(t *testing.T) {
	m := newTUI(t.TempDir(), moodPosts("good", "ok"))
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	m.handleMoodKey(key("h"))
	if m.mood.cursor == moodNoCursor {
		t.Fatal("h did not open the inspector")
	}
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mood.cursor != moodNoCursor {
		t.Error("esc did not drop the inspector")
	}
	if m.mode != modeMood {
		t.Error("esc left the panel while the inspector was still open")
	}
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList {
		t.Error("a second esc did not leave the panel")
	}
}

// The panel reads the wall the reader filtered down to, not the whole store.
func TestMoodPanelFollowsTheListFilter(t *testing.T) {
	evs := moodPosts("stuck", "stuck")
	evs = append(evs, wall.Event{TS: time.Now(), Repo: "beta", Actor: "worker", Msg: "other", Mood: "great"})
	m := newTUI(t.TempDir(), evs)
	m.width, m.height = 80, 26
	m.repoF = "beta"
	m.refilter()
	m.handleKey(key("m"))
	if m.mood.trail.Count != 1 || m.mood.trail.Avg != 5 {
		t.Errorf("trail = %d posts, avg %.1f, want the one beta post at 5.0", m.mood.trail.Count, m.mood.trail.Avg)
	}
}

// Walking left past the window's left edge scrolls the series instead of
// losing the cursor off-screen.
func TestMoodCursorScrollsTheSeries(t *testing.T) {
	st := moodStateOf(moodPosts(strings.Fields(strings.Repeat("ok ", 60))...), 99)
	const width = 40 // fits far fewer columns than there are points
	if _, start, _ := moodVisible(st, width); start == 0 {
		t.Fatal("test needs a series wider than the window")
	}
	st.cursor = 0 // the oldest point, far left of the default window
	pts, start, _ := moodVisible(st, width)
	if start != 0 {
		t.Errorf("window start = %d, want 0 so the cursor stays visible", start)
	}
	if len(pts) == 0 {
		t.Error("scrolled window is empty")
	}
}

// No row may reach past the window: an actor line with a long name and a
// four-digit post count once did, and a line that wraps breaks every row
// below it.
func TestMoodPanelNeverExceedsTheWidth(t *testing.T) {
	evs := moodPosts(strings.Fields(strings.Repeat("good ", 200))...)
	for i := range evs {
		if i%3 == 0 {
			evs[i].Actor = "worker/issue-pickup/very-long"
		}
		if i%7 == 0 {
			evs[i].Mood, evs[i].Outcome = "stuck", wall.OutcomeFailed
			evs[i].Msg = "war eine Sackgasse, dritter Anlauf"
		}
	}
	base := moodStateOf(evs, 99)
	cursored := base
	cursored.moveCursor(-1)
	daily := moodState{cursor: moodNoCursor, frame: 99, daily: true}
	daily.load(evs)
	actors := base
	actors.byActor = true
	views := map[string]moodState{"posts": base, "cursor": cursored, "daily": daily, "actors": actors}

	for name, st := range views {
		for _, w := range []int{40, 64, 80, 100, 200} {
			for i, l := range strings.Split(renderMood(st, w, 30, "30d", ""), "\n") {
				if got := lipgloss.Width(l); got > w {
					t.Errorf("%s at width %d: line %d is %d wide: %q", name, w, i, got, l)
				}
			}
		}
	}
}

// The axis spans the data, not the window: a rule running past the last
// column puts the right-hand date where nothing was ever posted.
func TestMoodAxisSpansItsDataNotTheWindow(t *testing.T) {
	st := moodStateOf(moodPosts("ok", "ok", "ok", "ok", "ok"), 99)
	var axis string
	for _, l := range strings.Split(render(st, 100, 26), "\n") {
		if strings.Contains(l, "└") {
			axis = l
		}
	}
	if n := strings.Count(axis, "─"); n != 5 {
		t.Errorf("axis is %d columns wide over 5 points: %q", n, axis)
	}
}

// ---- jumping out of the curve into the posts behind it ----

func TestMoodJumpLandsOnThePost(t *testing.T) {
	evs := moodPosts("good", "rough", "great")
	for i := range evs {
		evs[i].Msg = []string{"first", "second", "third"}[i]
	}
	m := newTUI(t.TempDir(), evs)
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	m.handleMoodKey(key("h")) // newest
	m.handleMoodKey(key("h")) // one back: "second"
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEnter})

	if m.mode != modeList {
		t.Fatalf("mode = %q after jumping, want the list", m.mode)
	}
	sel, ok := m.selected()
	if !ok {
		t.Fatal("nothing selected after the jump")
	}
	if sel.Msg != "second" {
		t.Errorf("landed on %q, want the post under the cursor (second)", sel.Msg)
	}
}

func TestMoodJumpPinsTheListToADay(t *testing.T) {
	d := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
	var evs []wall.Event
	for i := 0; i < 5; i++ { // 3 on day one, 2 on day two
		day := 0
		if i >= 3 {
			day = 1
		}
		evs = append(evs, wall.Event{TS: d.AddDate(0, 0, day).Add(time.Duration(i) * time.Hour),
			Repo: "alpha", Msg: fmt.Sprintf("post %d", i), Mood: "good"})
	}
	m := newTUI(t.TempDir(), evs)
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	m.handleMoodKey(key("d")) // day resolution
	if len(m.mood.drawn) != 2 {
		t.Fatalf("folded into %d columns, want 2", len(m.mood.drawn))
	}
	m.handleMoodKey(key("h")) // newest day
	m.handleMoodKey(key("h")) // the older one
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEnter})

	if m.mode != modeList {
		t.Fatalf("mode = %q, want the list", m.mode)
	}
	if m.dayF.IsZero() {
		t.Fatal("day column jumped without pinning the list to its day")
	}
	if len(m.view) != 3 {
		t.Errorf("list shows %d posts, want the 3 of that day", len(m.view))
	}
	for _, ei := range m.view {
		if !sameDay(m.events[ei].TS.Local(), m.dayF) {
			t.Errorf("post from %v survived the day pin %v", m.events[ei].TS, m.dayF)
		}
	}
	// and the way back out
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dayF.IsZero() || len(m.view) != 5 {
		t.Errorf("esc did not clear the day pin: dayF %v, %d posts", m.dayF, len(m.view))
	}
}

// Without a cursor there is no column to jump into, so enter keeps its old
// meaning and simply closes the panel.
func TestMoodEnterWithoutCursorJustCloses(t *testing.T) {
	m := newTUI(t.TempDir(), moodPosts("good", "ok"))
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeList {
		t.Errorf("mode = %q, want the list", m.mode)
	}
	if !m.dayF.IsZero() || m.note != "" {
		t.Errorf("enter without a cursor changed the list: dayF %v, note %q", m.dayF, m.note)
	}
}
