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

func render(st moodState, w, h int) string { return renderMood(st, w, h, "", "", "") }

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
	// the latency line adds a second axis on the right and the band adds its
	// range beside it, which is the easiest way to push a row past the window.
	// The turns have to spread: one repeated value prints "log 45s" and never
	// reaches the widest label the gutter has to hold.
	lined := moodStateOf(turnPosts(45_000, strings.Fields(strings.Repeat("good ", 200))...), 99)
	for i := range lined.drawn {
		lined.drawn[i].PulseMS = int64(2_800 + (i%17)*1_150) // 2.8s … 21.2s
	}
	lined.pulse = wall.Pulse{At: time.Now(), OK: true, RTT: 90 * time.Second, Src: wall.PulseSession}
	views := map[string]moodState{"posts": base, "cursor": cursored, "daily": daily, "actors": actors, "lined": lined}

	for name, st := range views {
		for _, w := range []int{40, 64, 80, 100, 200} {
			for i, l := range strings.Split(renderMood(st, w, 30, "30d", "03-04", ""), "\n") {
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

// ---- the loop: jump in, read, come back, jump again ----

func twoDayWall(t *testing.T) *tuiModel {
	t.Helper()
	d := time.Date(2026, 3, 4, 9, 0, 0, 0, time.Local)
	var evs []wall.Event
	for i := 0; i < 6; i++ {
		day := 0
		if i >= 3 {
			day = 1
		}
		evs = append(evs, wall.Event{TS: d.AddDate(0, 0, day).Add(time.Duration(i) * time.Hour),
			Repo: "alpha", Msg: fmt.Sprintf("post %d", i), Mood: "good"})
	}
	m := newTUI(t.TempDir(), evs)
	m.width, m.height = 80, 26
	return m
}

// The pin is the one filter the curve must not inherit: let it feed back in
// and the way back is a curve with one column on it, so the second jump has
// nowhere to go.
func TestMoodPanelKeepsTheWholeCurveWhileTheListIsPinned(t *testing.T) {
	m := twoDayWall(t)
	m.handleKey(key("m"))
	m.handleMoodKey(key("d"))
	m.handleMoodKey(key("h")) // newest day
	m.handleMoodKey(key("h")) // the older one
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dayF.IsZero() || len(m.view) != 3 {
		t.Fatalf("jump did not pin the list: dayF %v, %d posts", m.dayF, len(m.view))
	}

	m.handleKey(key("m")) // back into the panel
	if len(m.mood.drawn) != 2 {
		t.Errorf("curve collapsed to %d column(s) — the pin fed back into the panel", len(m.mood.drawn))
	}
	if m.mood.trail.Count != 6 {
		t.Errorf("curve covers %d posts, want all 6", m.mood.trail.Count)
	}
	if pin := m.viewMood(); !strings.Contains(pin, "list pinned to") {
		t.Errorf("panel does not say the list is pinned:\n%s", pin)
	}
	// and the second jump goes somewhere else
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dayF.IsZero() {
		t.Error("second jump lost the pin entirely")
	}
}

// Jumping in and coming back is the loop the panel exists for, so the cursor
// has to survive it — otherwise you find the column again every time.
func TestMoodCursorSurvivesTheRoundTrip(t *testing.T) {
	m := twoDayWall(t)
	m.handleKey(key("m"))
	m.handleMoodKey(key("h"))
	m.handleMoodKey(key("h"))
	want := m.mood.cursor
	if want == moodNoCursor {
		t.Fatal("no cursor to carry")
	}
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEnter}) // into the list
	m.handleKey(key("m"))                           // and back
	if m.mood.cursor != want {
		t.Errorf("cursor came back at %d, want %d", m.mood.cursor, want)
	}
}

// esc peels one layer: the pin is the newest and narrowest filter, and
// dropping it together with a search costs work you may still want.
func TestListEscPeelsThePinBeforeTheRest(t *testing.T) {
	m := twoDayWall(t)
	m.search = "post"
	m.refilter()
	m.handleKey(key("m"))
	m.handleMoodKey(key("d"))
	m.handleMoodKey(key("h"))
	m.handleMoodKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dayF.IsZero() {
		t.Fatal("no pin to peel")
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dayF.IsZero() {
		t.Error("first esc did not drop the pin")
	}
	if m.search != "post" {
		t.Errorf("first esc also cleared the search (%q) — one layer at a time", m.search)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.search != "" {
		t.Errorf("second esc left the search %q", m.search)
	}
}

// The pulse is the live half of the reading: the wall says how the work went,
// the API says whether any is possible right now.

func pulsed(evs []wall.Event, p wall.Pulse) moodState {
	st := moodStateOf(evs, 99)
	st.pulse = p
	return st
}

// turnPosts grades posts and says what each one's turn cost — the window's own
// conditions, which is what the head and the api line are drawn from.
func turnPosts(ms int64, moods ...string) []wall.Event {
	evs := moodPosts(moods...)
	for i := range evs {
		evs[i].PulseMS, evs[i].PulseSrc = ms, wall.PulseSession
	}
	return evs
}

// Nothing measured, nothing claimed: a panel that never probed says nothing
// about an API, and its head is the wall's own average.
func TestMoodPanelWithoutAPulseSaysNothingAboutTheAPI(t *testing.T) {
	panel := render(moodStateOf(moodPosts("great", "great", "great"), 99), 80, 26)
	if strings.Contains(panel, "api") {
		t.Errorf("an unprobed panel mentions the api:\n%s", panel)
	}
	if !strings.Contains(panel, "great · 5.0") {
		t.Errorf("head lost the wall's own average:\n%s", panel)
	}
}

// Latency only ever subtracts, and the panel shows the subtraction: a wall of
// nothing but great, read through a twelve-second API, is not a great day.
// The window's own waiting drags its own grades: both halves of the head come
// off the same posts, so the arithmetic can be checked against them.
func TestMoodPanelDragsTheHeadWithTheWindowsWaiting(t *testing.T) {
	st := pulsed(turnPosts(12_000, "great", "great", "great"),
		wall.Pulse{At: time.Now(), OK: true, RTT: 2 * time.Second, Src: wall.PulseSession})
	panel := render(st, 110, 26)
	for _, want := range []string{"ok · 3.0", "window 5.0", "− 2.0", "api ~12s over 3 posts", "now 2s"} {
		if !strings.Contains(panel, want) {
			t.Errorf("head is missing %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "great · 5.0") {
		t.Errorf("the head still reads as the undragged wall:\n%s", panel)
	}
}

// A live reading is not the window. It shows as `now`, and it may not move
// grades earned at another time — a month of work cannot turn rough because
// one answer just took a minute.
func TestMoodPanelLiveReadingDoesNotDragTheWindow(t *testing.T) {
	st := pulsed(moodPosts("great", "great", "great"),
		wall.Pulse{At: time.Now(), OK: true, RTT: 90 * time.Second, Src: wall.PulseSession})
	panel := render(st, 110, 26)
	if !strings.Contains(panel, "great · 5.0") || strings.Contains(panel, "−") {
		t.Errorf("a live reading dragged a window that measured nothing:\n%s", panel)
	}
	if !strings.Contains(panel, "now 1m30s") {
		t.Errorf("the live reading is not shown as now:\n%s", panel)
	}
}

// A fast API is not a compliment: it leaves the grades exactly as posted.
func TestMoodPanelFastAPILeavesTheGradesAlone(t *testing.T) {
	st := pulsed(turnPosts(1_500, "good", "good", "ok"),
		wall.Pulse{At: time.Now(), OK: true, RTT: 1500 * time.Millisecond, Src: wall.PulseSession})
	panel := render(st, 110, 26)
	if !strings.Contains(panel, "good · 3.7") {
		t.Errorf("a fast api moved the wall's average:\n%s", panel)
	}
	if !strings.Contains(panel, "api ~1.5s over 3 posts") || strings.Contains(panel, "−") {
		t.Errorf("receipt = want the turn time and no drag:\n%s", panel)
	}
}

// A probe that answered in 240ms proves the API is reachable and nothing
// else. Printing that number beside turn times invites the one comparison the
// whole scale exists to prevent — so a healthy ping prints nothing at all,
// and only an outage has something to say.
func TestMoodPanelPrintsNoNumberForAPing(t *testing.T) {
	st := pulsed(moodPosts("great", "great", "great"),
		wall.Pulse{At: time.Now(), OK: true, RTT: 240 * time.Millisecond, Src: wall.PulseProbe})
	panel := render(st, 100, 26)
	for _, unwanted := range []string{"240ms", "ping", "−"} {
		if strings.Contains(panel, unwanted) {
			t.Errorf("a healthy ping put %q in the head:\n%s", unwanted, panel)
		}
	}
	if !strings.Contains(panel, "great · 5.0") {
		t.Errorf("a ping moved the wall's average:\n%s", panel)
	}
	// an outage still speaks, because that one is not a number about speed
	down := pulsed(moodPosts("great"), wall.Pulse{At: time.Now(), Err: "connection refused"})
	if got := render(down, 100, 26); !strings.Contains(got, "no api — connection refused") {
		t.Errorf("an outage went unmentioned:\n%s", got)
	}
}

// No API is not a slow day, it is a crashout: the reading drops to the floor
// whatever the wall said, and says why.
func TestMoodPanelCrashesWithoutAPI(t *testing.T) {
	st := pulsed(moodPosts("great", "great"), wall.Pulse{At: time.Now(), Err: "connection refused"})
	panel := render(st, 100, 26)
	for _, want := range []string{moodFaceCrash, moodCrashWord, "no api", "connection refused", "window great · 5.0"} {
		if !strings.Contains(panel, want) {
			t.Errorf("crashout head is missing %q:\n%s", want, panel)
		}
	}
	// the curve behind it is history and stays exactly what was posted
	if got := line(t, panel, "great"); !strings.Contains(got, moodBar) {
		t.Errorf("the crashout ate the curve: %q", got)
	}
}

// An ungraded wall has no mood to drag — but an API that is not answering is
// measured, not invented, so the crashout still shows.
func TestMoodPanelCrashoutOnAnUngradedWall(t *testing.T) {
	evs := []wall.Event{{TS: time.Now(), Repo: "alpha", Msg: "no grade"}}
	panel := render(pulsed(evs, wall.Pulse{At: time.Now(), Err: "no route to host"}), 100, 26)
	if !strings.Contains(panel, moodCrashWord) || !strings.Contains(panel, "no grades") {
		t.Errorf("want a crashout over a wall with no grades:\n%s", panel)
	}
	if strings.Contains(panel, moodFaceNone) {
		t.Errorf("the ungraded face outranked a measured outage:\n%s", panel)
	}
	// a healthy API on the same wall stays quiet: latency subtracts, it never invents
	quiet := render(pulsed(evs, wall.Pulse{At: time.Now(), OK: true, RTT: 3 * time.Second}), 100, 26)
	if !strings.Contains(quiet, moodFaceNone) {
		t.Errorf("a slow api invented a mood on an ungraded wall:\n%s", quiet)
	}
}

// While the first probe is out the panel says so, rather than looking like an
// offline one for as long as the API takes to answer.
func TestMoodPanelSaysItIsMeasuring(t *testing.T) {
	st := moodStateOf(moodPosts("good"), 99)
	st.pulsing = true
	if panel := render(st, 80, 26); !strings.Contains(panel, "api …") {
		t.Errorf("a probe in flight is invisible:\n%s", panel)
	}
}

// A transport error can be a paragraph; the panel has one line.
func TestMoodPanelClipsALongPulseError(t *testing.T) {
	p := wall.Pulse{At: time.Now(), Err: strings.Repeat("dial tcp 10.0.0.1:443: i/o timeout, ", 12)}
	for _, w := range []int{40, 60, 80} {
		for i, l := range strings.Split(render(pulsed(moodPosts("good", "ok"), p), w, 26), "\n") {
			if lipgloss.Width(l) > w {
				t.Errorf("width %d: line %d is %d wide: %q", w, i, lipgloss.Width(l), l)
			}
		}
	}
}

// The pulse runs on the panel's clock: closing it ends the measuring, and a
// probe fired on an earlier visit does not schedule the next one.
func TestMoodPulseClockStopsWithThePanel(t *testing.T) {
	t.Setenv("WALLII_PULSE", "off") // no request leaves the test; only the clock is under test
	m := newTUI(t.TempDir(), moodPosts("good", "ok"))
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	reading := wall.Pulse{At: time.Now(), OK: true, RTT: 2 * time.Second}
	if _, cmd := m.Update(moodPulseMsg{epoch: m.mood.epoch, pulse: reading}); cmd == nil {
		t.Fatal("a reading did not schedule the next probe")
	}
	if m.mood.pulse.RTT != reading.RTT || m.mood.pulsing {
		t.Errorf("state after a reading = %s, pulsing %v, want the reading and no probe in flight", m.mood.pulse.RTT, m.mood.pulsing)
	}
	stale := m.mood.epoch
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if _, cmd := m.Update(moodPulseDueMsg{epoch: stale}); cmd != nil {
		t.Error("the panel is closed and it is still probing")
	}
	m.handleKey(key("m"))
	if _, cmd := m.Update(moodPulseDueMsg{epoch: stale}); cmd != nil {
		t.Error("a due tick from the previous visit still fires a probe")
	}
}

// wallii reads local files; the one thing in it that touches the network can
// be switched off, and then the panel neither probes nor claims.
func TestMoodPulseOffSwitchKeepsThePanelOffline(t *testing.T) {
	t.Setenv("WALLII_PULSE", "off")
	m := newTUI(t.TempDir(), moodPosts("good", "ok"))
	m.width, m.height = 80, 26
	m.handleKey(key("m"))
	if m.mood.pulsing {
		t.Error("WALLII_PULSE=off still fired a probe")
	}
	if panel := m.viewMood(); strings.Contains(panel, "api") {
		t.Errorf("an offline panel mentions the api:\n%s", panel)
	}
}

// The line's height IS the drag: the top of the scale minus what the waiting
// takes off a grade. Reading the picture and doing the arithmetic must give
// the same answer, or the line is decoration.
func TestPulseLineSitsAtTheDrag(t *testing.T) {
	cases := []struct {
		ms   int64
		want string // the row label the line belongs on
	}{
		{1_500, "great"},  // drag 0 → the top of the scale
		{5_000, "good"},   // drag 1
		{12_000, "ok"},    // drag 2
		{30_000, "rough"}, // drag 3
	}
	for _, c := range cases {
		panel := render(moodStateOf(turnPosts(c.ms, "ok", "ok", "ok"), 99), 100, 30)
		row := line(t, panel, c.want)
		if !strings.Contains(row, moodLine) && !strings.Contains(row, moodBar) {
			t.Errorf("%dms: no api line on the %q row:\n%s", c.ms, c.want, panel)
		}
	}
}

// A post nobody timed gets no point, and the line does not bridge the gap:
// an interpolated stretch would draw a measurement that was never taken.
func TestPulseLineLeavesGapsWhereNothingWasMeasured(t *testing.T) {
	evs := moodPosts("ok", "ok", "ok")
	evs[0].PulseMS, evs[0].PulseSrc = 12_000, wall.PulseSession // drag 2 → the "ok" row
	// evs[1] and evs[2] carry nothing
	panel := render(moodStateOf(evs, 99), 100, 30)
	row := line(t, panel, "ok")
	marks := strings.Count(row, moodLine)
	if marks != 0 {
		// the measured column already holds a mood block on this row, so the
		// line recolors it instead of drawing its own glyph — what matters is
		// that the two unmeasured columns stayed empty
		t.Errorf("the line drew %d glyphs where only one column was measured:\n%s", marks, panel)
	}
	if strings.Contains(panel, "api time") == false {
		t.Errorf("the line is drawn but not named in the legend:\n%s", panel)
	}
}

// Without a single measured turn there is no line at all — and no legend
// entry claiming there is one.
func TestPulseLineAbsentWithoutMeasurements(t *testing.T) {
	panel := render(moodStateOf(moodPosts("good", "ok", "great"), 99), 100, 30)
	if strings.Contains(panel, "api time") {
		t.Errorf("a legend entry for a line nobody could draw:\n%s", panel)
	}
	for _, lvl := range []string{"great", "good", "ok", "rough", "stuck"} {
		if strings.Contains(line(t, panel, lvl), moodLine) {
			t.Errorf("an api line on the %q row of an unmeasured wall:\n%s", lvl, panel)
		}
	}
}

// Thin coverage has to say so: four measured posts out of four hundred draw a
// line that looks like a reading of the whole window.
func TestPulseLineNamesItsCoverage(t *testing.T) {
	evs := moodPosts(strings.Fields(strings.Repeat("ok ", 20))...)
	evs[0].PulseMS, evs[0].PulseSrc = 20_000, wall.PulseSession
	panel := render(moodStateOf(evs, 99), 100, 30)
	if !strings.Contains(panel, "1 of 20 posts measured a turn") {
		t.Errorf("the note does not report the line's coverage:\n%s", panel)
	}
}

// A day column folds many turns; the line follows the day's mean.
func TestPulseLineFollowsTheDayFold(t *testing.T) {
	evs := moodPosts("ok", "ok")
	evs[0].PulseMS, evs[0].PulseSrc = 8_000, wall.PulseSession
	evs[1].PulseMS, evs[1].PulseSrc = 16_000, wall.PulseSession // mean 12s → drag 2
	st := moodStateOf(evs, 99)
	st.daily = true
	st.refold()
	y, ok := pulseY(st.drawn[0])
	if !ok || y != 3 {
		t.Errorf("day column's line sits at %.2f (ok %v), want 3 — 5 minus a drag of 2", y, ok)
	}
}

// The line's height is a continuous quantity laid over discrete rows, so the
// glyph carries the third of a row the position falls in. Snapping to row
// centers would put a 2.5s window and a 4.9s one in the same place.
func TestPulseLinePositionIsProportional(t *testing.T) {
	at := func(ms int64) (int, string) {
		p := wall.MoodPoint{PulseMS: ms, PulseN: 1}
		for sr := 0; sr < len(wall.Moods); sr++ {
			if g, ok := pulseGlyph(p, sr, 1); ok {
				return sr, g
			}
		}
		t.Fatalf("%dms lands on no row", ms)
		return 0, ""
	}
	// 2s is drag 0 (the top row), 5s is drag 1 (one row down): everything
	// between has to appear between them, and in order
	var last int
	for i, ms := range []int64{2_000, 3_000, 4_000, 5_000} {
		sr, g := at(ms)
		pos := sr*3 + map[string]int{moodLineTop: 0, moodLine: 1, moodLineBot: 2}[g]
		if i > 0 && pos <= last {
			t.Errorf("%dms sits at %d, not below the previous %d — the line is snapping", ms, pos, last)
		}
		last = pos
	}
	// and the anchors land on their own rows: 2s at great, 5s at good,
	// 12s at ok, 30s at rough
	for _, c := range []struct {
		ms  int64
		row int
	}{{2_000, 0}, {5_000, 1}, {12_000, 2}, {30_000, 3}} {
		if sr, g := at(c.ms); sr != c.row || g != moodLine {
			t.Errorf("%dms sits on row %d as %q, want row %d dead center", c.ms, sr, g, c.row)
		}
	}
}

// The second axis names the line's unit. A height in mood steps is not a
// number anyone can read back as seconds.
func TestPulseAxisLabelsTheSeconds(t *testing.T) {
	want := map[int]string{5: "≤2s", 4: "5s", 3: "12s", 2: "30s", 1: ""}
	for lvl, w := range want {
		if got := pulseAxisLabel(lvl); got != w {
			t.Errorf("axis label at level %d = %q, want %q", lvl, got, w)
		}
	}
	panel := render(moodStateOf(turnPosts(5_000, "ok", "ok"), 99), 100, 30)
	for _, w := range []string{"├ ≤2s", "├ 5s", "├ 12s", "├ 30s"} {
		if !strings.Contains(panel, w) {
			t.Errorf("panel is missing the axis mark %q:\n%s", w, panel)
		}
	}
	// no line, no second axis: an axis for nothing is a claim
	if bare := render(moodStateOf(moodPosts("ok", "ok"), 99), 100, 30); strings.Contains(bare, "├") {
		t.Errorf("an unmeasured wall drew a latency axis:\n%s", bare)
	}
}

// bandRow returns the api band's row, without its label or its range.
func bandRow(t *testing.T, panel string) string {
	t.Helper()
	for _, l := range strings.Split(panel, "\n") {
		if strings.HasPrefix(l, "  api ") {
			return l
		}
	}
	t.Fatalf("no api band in panel:\n%s", panel)
	return ""
}

// varied builds a window whose turns swing, all of them below the first
// anchor: the line cannot show this, which is why the band exists.
func varied(ms ...int64) []wall.Event {
	evs := moodPosts(strings.Fields(strings.Repeat("good ", len(ms)))...)
	for i := range evs {
		evs[i].PulseMS, evs[i].PulseSrc = ms[i], wall.PulseSession
	}
	return evs
}

// The drag saturates under the first anchor on purpose — none of that waiting
// reaches a grade — so a window that swings six-fold below it draws a flat
// line and needs a second mark to show the swing at all.
func TestPulseBandShowsTheSwingTheLineCannot(t *testing.T) {
	panel := render(moodStateOf(varied(300, 1900, 500, 1200, 400, 1800), 99), 100, 30)

	heights := map[rune]bool{}
	for _, r := range bandRow(t, panel) {
		if strings.ContainsRune(string(moodSpark), r) {
			heights[r] = true
		}
	}
	if len(heights) < 3 {
		t.Errorf("a six-fold swing drew %d distinct heights:\n%s", len(heights), panel)
	}
	// the line meanwhile is flat at the top, and says why
	if !strings.Contains(panel, "all under 2s") {
		t.Errorf("the flat line does not explain itself:\n%s", panel)
	}
	for _, lvl := range []string{"good", "ok", "rough", "stuck"} {
		row := line(t, panel, lvl)
		if strings.Contains(row, moodLine) || strings.Contains(row, moodLineTop) || strings.Contains(row, moodLineBot) {
			t.Errorf("the api line left the top row for %q, but nothing cost a grade:\n%s", lvl, panel)
		}
	}
}

// Log-spaced: one slow outlier must not flatten everything else into the
// floor, which is exactly what a linear scale does to latency.
func TestPulseBandIsLogScaled(t *testing.T) {
	row := bandRow(t, render(moodStateOf(varied(2000, 4000, 8000, 90000), 99), 100, 30))
	marks := []rune{}
	for _, r := range row {
		if strings.ContainsRune(string(moodSpark), r) {
			marks = append(marks, r)
		}
	}
	if len(marks) != 4 {
		t.Fatalf("band drew %d marks, want 4: %q", len(marks), row)
	}
	if !(marks[0] < marks[1] && marks[1] < marks[2] && marks[2] < marks[3]) {
		t.Errorf("2s/4s/8s/90s drew %q — a doubling has to be visible next to an outlier", string(marks))
	}
}

func TestPulseBandLabelsItsRange(t *testing.T) {
	panel := render(moodStateOf(varied(2000, 90000, 4000), 99), 100, 30)
	if !strings.Contains(panel, "log 2s–1m30s") {
		t.Errorf("band does not name its range:\n%s", panel)
	}
	// no spread, no range: one number, and a flat row rather than a shape
	flat := render(moodStateOf(varied(3000, 3000, 3000), 99), 100, 30)
	if !strings.Contains(flat, "log 3s") || strings.Contains(flat, "3s–") {
		t.Errorf("a window with no spread invented one:\n%s", flat)
	}
	if got := strings.Count(bandRow(t, flat), string(moodSpark[len(moodSpark)/2])); got != 3 {
		t.Errorf("flat window drew %d middle marks, want 3:\n%s", got, flat)
	}
}

// The gutter holds two right-hand labels — the line's seconds axis and the
// band's range — and it has to be sized for the wider. Sized for the axis
// alone, as it was for as long as the band existed, a full window cut the
// range down to "log 2." and left the shape with no numbers at all.
func TestPulseBandRangeSurvivesAFullWindow(t *testing.T) {
	ms := make([]int64, 200)
	for i := range ms {
		ms[i] = int64(2_800 + (i%17)*1_150) // 2.8s … 21.2s, cycling
	}
	for _, w := range []int{60, 80, 100, 140} {
		panel := render(moodStateOf(varied(ms...), 99), w, 30)
		if !strings.Contains(panel, "log 2.8s–21.2s") {
			t.Errorf("width %d: the band's range was clipped:\n%s", w, panel)
		}
	}
}

// Posts nobody timed leave the band empty there, like the line above it.
func TestPulseBandLeavesGaps(t *testing.T) {
	evs := varied(2000, 4000, 8000)
	evs[1].PulseMS, evs[1].PulseSrc = 0, ""
	row := bandRow(t, render(moodStateOf(evs, 99), 100, 30))
	marks := 0
	for _, r := range row {
		if strings.ContainsRune(string(moodSpark), r) {
			marks++
		}
	}
	if marks != 2 {
		t.Errorf("band drew %d marks for 2 measured posts: %q", marks, row)
	}
}

// The scale comes off the whole visible series, so it cannot rescale while
// the sweep reveals it — three different pictures in one second.
func TestPulseBandScaleHoldsThroughTheSweep(t *testing.T) {
	evs := varied(2000, 4000, 8000, 16000, 32000, 64000)
	for _, frame := range []int{0, 1, 3, 5, 99} {
		if panel := render(moodStateOf(evs, frame), 100, 30); !strings.Contains(panel, "log 2s–1m4s") {
			t.Errorf("frame %d: the band's range moved:\n%s", frame, panel)
		}
	}
}

// A drag that rounds to zero is not a term. "− 0.0" reads as a bug.
func TestMoodHeadDropsAZeroDrag(t *testing.T) {
	// 2.02s: past the first anchor by a whisker, a drag of 0.007
	panel := render(moodStateOf(varied(2020, 2020), 99), 100, 30)
	if strings.Contains(panel, "− 0.0") {
		t.Errorf("head prints a zero drag:\n%s", panel)
	}
	// but a real one still shows
	if big := render(moodStateOf(varied(8500, 8500), 99), 100, 30); !strings.Contains(big, "− 1.5") {
		t.Errorf("head dropped a drag that matters:\n%s", big)
	}
}

// A lifetime average cannot be contradicted: with hundreds of posts behind it
// a rough afternoon moves it by a thousandth, and the panel then shows a
// smiling face over work that just went wrong. The recent stretch is the term
// that can disagree.
func TestMoodHeadShowsTheRecentStretch(t *testing.T) {
	moods := append(strings.Fields(strings.Repeat("great ", 40)), strings.Fields(strings.Repeat("rough ", 10))...)
	st := moodStateOf(moodPosts(moods...), 99)
	panel := render(st, 110, 30)

	// the headline is the part that can still move, and the face moves with it
	if !strings.Contains(panel, "last 10: rough · 2.0 ↓") {
		t.Errorf("the headline is not the recent stretch:\n%s", panel)
	}
	if !strings.Contains(panel, moodFaces[3].open) && !strings.Contains(panel, moodFaces[3].blink) {
		t.Errorf("the face kept smiling through ten rough posts:\n%s", panel)
	}
	// and the window it sits in is named beside it, never replaced by it
	if !strings.Contains(panel, "window good · 4.4") {
		t.Errorf("head dropped the window it is a tail of:\n%s", panel)
	}
}

// The headline's grade and its waiting must come off the same posts: a recent
// stretch dragged by the whole window's latency would be an average of one
// thing wearing the arithmetic of another.
func TestMoodHeadDragsTheTailWithItsOwnWaiting(t *testing.T) {
	// a hundred and ninety quick posts, then ten slow ones — same grades throughout
	evs := turnPosts(1_000, strings.Fields(strings.Repeat("great ", 200))...)
	for i := 190; i < 200; i++ {
		evs[i].PulseMS = 12_000 // twelve seconds a turn: two steps off
	}
	panel := render(moodStateOf(evs, 99), 120, 30)
	if !strings.Contains(panel, "last 10: ok · 3.0 ↓") {
		t.Errorf("the tail was not dragged by its own waiting:\n%s", panel)
	}
	// and the waiting it names is the tail's own: the window's mean is 1.6s,
	// which drags nothing and would leave the 3.0 unreconstructable
	if !strings.Contains(panel, "api ~12s over 10") || strings.Contains(panel, "1.6s") {
		t.Errorf("the head names a different window's waiting than the one it subtracted:\n%s", panel)
	}
	// meanwhile the window barely moved — 190 quick turns against 10 slow ones
	// average 1.6s, under the first anchor — which is exactly why the tail is
	// the headline: the whole window cannot report a bad afternoon
	if !strings.Contains(panel, "window great · 5.0") || strings.Contains(panel, "window 5.0 −") {
		t.Errorf("the window claimed a drag it does not have:\n%s", panel)
	}
}

// The arrow is a claim about direction, so it only appears where there is one.
func TestMoodRecentArrowOnlyOnRealMovement(t *testing.T) {
	flat := moodStateOf(moodPosts(strings.Fields(strings.Repeat("good ", 30))...), 99)
	if got := render(flat, 110, 30); strings.Contains(got, "↓") || strings.Contains(got, "↑") {
		t.Errorf("an unchanging wall drew a trend arrow:\n%s", got)
	}
	up := append(strings.Fields(strings.Repeat("ok ", 30)), strings.Fields(strings.Repeat("great ", 10))...)
	if got := render(moodStateOf(moodPosts(up...), 99), 110, 30); !strings.Contains(got, "↑") {
		t.Errorf("a recovering wall drew no upward arrow:\n%s", got)
	}
}

// Below the threshold the recent stretch IS the window: printing the same
// number twice under two names says nothing.
func TestMoodRecentSilentOnAShortWall(t *testing.T) {
	for _, n := range []int{1, 5, moodRecentN} {
		st := moodStateOf(moodPosts(strings.Fields(strings.Repeat("good ", n))...), 99)
		if got := render(st, 110, 30); strings.Contains(got, "last 10") {
			t.Errorf("%d posts: head claims a recent stretch it does not have:\n%s", n, got)
		}
	}
	st := moodStateOf(moodPosts(strings.Fields(strings.Repeat("good ", moodRecentN+1))...), 99)
	if got := render(st, 110, 30); !strings.Contains(got, "last 10") {
		t.Errorf("%d posts: head drops the recent stretch:\n%s", moodRecentN+1, got)
	}
}

// The window term carries arithmetic only where there is some. Its job is to
// say what the headline is a tail of, not to repeat it in longer words.
func TestMoodWindowTermCarriesItsArithmetic(t *testing.T) {
	plain := render(moodStateOf(moodPosts("good", "good", "ok"), 99), 110, 30)
	if !strings.Contains(plain, "window good · 3.7") {
		t.Errorf("head does not name the window:\n%s", plain)
	}
	if strings.Contains(plain, "−") {
		t.Errorf("a window nobody kept waiting claimed a drag:\n%s", plain)
	}
	dragged := render(pulsed(turnPosts(12_000, "great", "great", "great"),
		wall.Pulse{At: time.Now(), OK: true, RTT: 12 * time.Second, Src: wall.PulseSession}), 110, 30)
	if !strings.Contains(dragged, "window 5.0 − 2.0") {
		t.Errorf("head hides the arithmetic behind a drag:\n%s", dragged)
	}
	crashed := render(pulsed(moodPosts("great", "great"),
		wall.Pulse{At: time.Now(), Err: "connection refused"}), 110, 30)
	if !strings.Contains(crashed, "window great · 5.0") {
		t.Errorf("a crashout hid what the wall was before it:\n%s", crashed)
	}
}
