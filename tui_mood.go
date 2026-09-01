// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bmmmm/wallii/internal/wall"
)

// Animation timing, in frames of moodFrameDur. The mood panel is the only
// thing in wallii that redraws on its own, so its clock runs only while the
// panel is open — moodTickCmd carries an epoch and a stale tick dies.
const (
	moodFrameDur     = 100 * time.Millisecond
	moodRevealFrames = 9  // the series sweeps in left to right, ~0.9s
	moodBlinkEvery   = 34 // the face blinks roughly every 3.4s …
	moodBlinkFrames  = 2  // … for two frames
	moodFlashFrames  = 8  // a post landing while the panel is open lights its column
	moodNoCursor     = -1
)

const (
	moodBar = "█"
	// moodContra replaces the mark on a post whose message disagrees with its
	// own grade. The height IS the grade, so the doubt belongs at the height
	// rather than in a footnote under the picture.
	moodContra = "!"
	// moodStem draws the cursor's column where it has no mark of its own, so
	// the inspected column is findable without painting over the curve.
	moodStem = "│"
)

// moodSpark renders a score as one of eight block heights, for the per-actor
// lines where a whole series has to fit on one row.
var moodSpark = []rune("▁▂▃▄▅▆▇█")

// moodState is everything the panel remembers between frames. It is folded on
// entry, on ingest and on a toggle — never per frame.
type moodState struct {
	trail   wall.MoodSummary
	drawn   []wall.MoodPoint // trail.Points, folded by day when daily is set
	frame   int
	epoch   int
	flash   int
	daily   bool // one column per day instead of one per post
	byActor bool // a line per actor instead of one shared curve
	cursor  int  // index into drawn, or moodNoCursor
}

func (st *moodState) load(evs []wall.Event) {
	st.trail = wall.MoodTrail(evs)
	st.refold()
}

// refold rebuilds what the panel draws from what it holds. Day folding lives
// here and not in the renderer: it is a property of the series, and a
// renderer that folded would redo it ten times a second.
func (st *moodState) refold() {
	st.drawn = st.trail.Points
	if st.daily {
		st.drawn = wall.MoodDays(st.drawn)
	}
	if st.cursor >= len(st.drawn) {
		st.cursor = len(st.drawn) - 1
	}
	if len(st.drawn) == 0 {
		st.cursor = moodNoCursor
	}
}

// moveCursor walks the curve. The first step from nowhere lands on the newest
// point — the one you are most likely asking about.
func (st *moodState) moveCursor(d int) {
	if len(st.drawn) == 0 {
		return
	}
	if st.cursor == moodNoCursor {
		st.cursor = len(st.drawn) - 1
		return
	}
	st.cursor = min(max(st.cursor+d, 0), len(st.drawn)-1)
}

func (st *moodState) at() (wall.MoodPoint, bool) {
	if st.cursor < 0 || st.cursor >= len(st.drawn) {
		return wall.MoodPoint{}, false
	}
	return st.drawn[st.cursor], true
}

type moodTickMsg struct{ epoch int }

func moodTickCmd(epoch int) tea.Cmd {
	return tea.Tick(moodFrameDur, func(time.Time) tea.Msg { return moodTickMsg{epoch} })
}

// moodFace is one row of the scale's face: the mouth carries the grade, the
// eyes carry the animation. Closed eyes don't blink — great is already
// laughing and stuck is already out — so those two hold still and the blink
// only falls where there is an eye to close.
type moodFace struct{ open, blink string }

// indexed like wall.Moods: great … stuck
var moodFaces = []moodFace{
	{"( ^‿^ )", ""},
	{"( o‿o )", "( -‿- )"},
	{"( o_o )", "( -_- )"},
	{"( ò_ó )", "( -_- )"},
	{"( x_x )", ""},
}

// moodFaceNone is the face of an ungraded wall. It is not a sad face and not
// a happy one: nobody said, and the panel must not invent a mood the posts
// never carried.
const moodFaceNone = "( ?_? )"

func faceFor(avg float64, blinking bool) string {
	f := moodFaces[moodIndex(avg)]
	if blinking && f.blink != "" {
		return f.blink
	}
	return f.open
}

// moodColor maps a score onto the same palette the outcome glyphs use —
// 2 green, 3 yellow, 1 red — and returns 0 for ok, which stays the terminal's
// own foreground. Color marks friction; the unremarkable middle gets none.
func moodColor(score int) int {
	switch score {
	case 5, 4:
		return 2
	case 2:
		return 3
	case 1:
		return 1
	}
	return 0
}

// enterMood opens the panel, keeping the cursor where it was. Jumping into a
// column and coming back is the loop the panel exists for, and a cursor reset
// on every return would make you find the column again each time. refold
// clamps it if the series shrank meanwhile.
func (m *tuiModel) enterMood() tea.Cmd {
	m.mode = modeMood
	m.mood.epoch++
	m.mood.frame, m.mood.flash = 0, 0
	m.refreshMood()
	return moodTickCmd(m.mood.epoch)
}

// refreshMood re-folds the trail from the events the panel should see, so
// window, repo, topic and search all carry into it: what you filtered the
// wall down to is the wall whose mood you are reading.
func (m *tuiModel) refreshMood() {
	m.mood.load(m.panelEvents())
}

// panelEvents is the list's filter minus the day pin, oldest first.
//
// The pin is the one filter the curve must not inherit. Every other filter is
// something you asked the wall for; the pin is the result of navigating out
// of the curve itself. Let it feed back in and the curve becomes the single
// day you just opened — so the way back is a curve with one column on it, and
// the second jump has nowhere to go. Skipping it keeps the loop open: jump
// into a day, read it in the list, come back to the whole curve, jump into
// the next. The header says the list is pinned so the two views cannot be
// confused for each other.
func (m *tuiModel) panelEvents() []wall.Event {
	q := strings.ToLower(m.search)
	since := windowStart(m.window, time.Now())
	evs := make([]wall.Event, 0, len(m.view))
	for _, e := range m.events { // events are oldest first, and so is the trail
		if m.passes(e, since, q, false) {
			evs = append(evs, e)
		}
	}
	return evs
}

func (m *tuiModel) handleMoodKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "m":
		m.mode = modeList
	case "enter":
		// enter goes one level deeper everywhere in the TUI: in the list it
		// opens the post, and on a column it opens what the column is made of
		if !m.jumpToPoint() {
			m.mode = modeList
		}
	case "esc":
		// esc peels one layer: it drops the inspector before it drops the
		// panel, the way esc in the list clears filters before anything else
		if m.mood.cursor != moodNoCursor {
			m.mood.cursor = moodNoCursor
		} else {
			m.mode = modeList
		}
	case "h", "left":
		m.mood.moveCursor(-1)
	case "l", "right":
		m.mood.moveCursor(1)
	case "g":
		m.mood.cursor = 0
	case "G":
		m.mood.cursor = len(m.mood.drawn) - 1
	case "d":
		m.mood.daily = !m.mood.daily
		m.mood.cursor = moodNoCursor // the columns mean something else now
		m.mood.refold()
		m.mood.frame = 0
	case "a":
		m.mood.byActor = !m.mood.byActor
		m.mood.frame = 0
	case "1", "2", "3", "0":
		m.setWindow(windowKeys[k.String()])
	}
	if len(m.mood.drawn) == 0 {
		m.mood.cursor = moodNoCursor
	}
	return m, nil
}

// jumpToPoint leaves the panel for the posts behind the column under the
// cursor: a post column lands the list cursor on that post, a day column pins
// the list to that day. Reports false when there is no cursor to follow, so
// enter still just closes the panel.
//
// A post is matched by timestamp and message rather than by its derived ID:
// the ID is a sha256 per event, and hashing the whole wall on every refold to
// serve one keystroke is a poor trade — two posts sharing both fields to the
// nanosecond are the same post anyway.
func (m *tuiModel) jumpToPoint() bool {
	p, ok := m.mood.at()
	if !ok {
		return false
	}
	m.mode = modeList
	if p.N > 1 {
		m.dayF = p.TS
		m.cursor, m.scroll = 0, 0
		m.refilter()
		m.note = fmt.Sprintf("%s · %s", p.TS.Format("2006-01-02"), plural(p.N, "post"))
		return true
	}
	for vi, ei := range m.view {
		if e := m.events[ei]; e.TS.Equal(p.TS) && e.Msg == p.Msg {
			m.cursor = vi
			m.note = "from the mood curve"
			return true
		}
	}
	m.note = "that post is no longer in view"
	return true
}

func (m *tuiModel) viewMood() string {
	pin := ""
	if !m.dayF.IsZero() {
		pin = m.dayF.Format("01-02")
	}
	return renderMood(m.mood, m.width, m.height, m.window, pin, m.note)
}

func renderMood(st moodState, width, height int, window, pin, note string) string {
	blink := st.frame%moodBlinkEvery < moodBlinkFrames
	// a short window spends its lines on the curve, not on air around it
	gap := ""
	if height >= 18 {
		gap = "\n"
	}
	var b strings.Builder
	b.WriteString(moodHeader(st, width, window, pin) + "\n" + gap)

	if st.trail.Count == 0 {
		b.WriteString(center(styleHeader.Render(moodFaceNone), width) + "\n" + gap)
		b.WriteString(center(styleDim.Render(fmt.Sprintf("no mood on any of these %d posts", st.trail.Total)), width) + "\n")
		b.WriteString(center(styleDim.Render("post with --mood great|good|ok|rough|stuck to fill this in"), width) + "\n")
		return fit(b.String(), height) + moodFooter(note, width, false)
	}

	face := fmt.Sprintf("%s   %s · %.1f", faceFor(st.trail.Avg, blink), moodWord(st.trail.Avg), st.trail.Avg)
	b.WriteString(center(styleHeader.Render(face), width) + "\n" + gap)
	if st.byActor {
		b.WriteString(moodActorLines(st, width))
	} else {
		b.WriteString(moodGraph(st, width, height))
		if l := moodInspect(st, width); l != "" {
			b.WriteString(gap + l + "\n")
		}
	}
	b.WriteString(gap + moodLegend(st.trail, width) + "\n")
	if n := moodNote(st.trail); n != "" {
		b.WriteString(" " + styleDim.Render(n) + "\n")
	}
	return fit(b.String(), height) + moodFooter(note, width, true)
}

// fit caps the body one line short of the window so the footer always lands
// on screen — a terminal too small for the whole panel loses the tail of it,
// never the way out of it.
func fit(body string, height int) string {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if n := height - 1; n > 0 && len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n") + "\n"
}

func moodHeader(st moodState, width int, window, pin string) string {
	left := fmt.Sprintf(" wallii · mood · %d of %d posts graded", st.trail.Count, st.trail.Total)
	if st.daily {
		left += " · " + plural(len(st.drawn), "day")
	}
	if window != "" {
		left += " · " + window
	}
	if pin != "" {
		left += " · list pinned to " + pin
	}
	if st.byActor {
		left += " · by actor"
	}
	return styleHeader.MaxWidth(width).Render(left)
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func moodFooter(note string, width int, graph bool) string {
	hint := " m/esc back"
	if graph {
		hint += " · h/l inspect · enter jump in · d day/post · a actors · 1/2/3/0 window"
	}
	if note != "" {
		hint = " " + note + " ·" + hint
	}
	return styleDim.MaxWidth(width).Render(hint)
}

// moodCell is one column of one row: what to draw, in which color, and
// whether it is lit — the cursor's column, or a post that just landed.
type moodCell struct {
	ch    string
	color int  // 0 = the terminal's own foreground
	lit   bool // reverse video
}

// renderCells writes a row, batching runs of equal style into one render — a
// style per cell would be a few thousand renders a second at 10fps. A lit
// block is drawn as a space: reverse paints the glyph in the background
// color, and a full block covers its whole cell, so a lit block would be an
// invisible one.
func renderCells(cs []moodCell) string {
	var b strings.Builder
	for i := 0; i < len(cs); {
		j := i
		for j < len(cs) && cs[j].color == cs[i].color && cs[j].lit == cs[i].lit {
			j++
		}
		var seg strings.Builder
		for _, c := range cs[i:j] {
			if c.lit && c.ch == moodBar {
				seg.WriteString(" ")
			} else {
				seg.WriteString(c.ch)
			}
		}
		style := lipgloss.NewStyle()
		if cs[i].color > 0 {
			style = style.Foreground(lipgloss.Color(strconv.Itoa(cs[i].color)))
		}
		if cs[i].lit {
			style = style.Reverse(true)
		}
		b.WriteString(style.Render(seg.String()))
		i = j
	}
	return b.String()
}

const (
	moodLabelW = 5
	moodLeft   = 2 + moodLabelW + 2 // "  great ┤"
)

// moodVisible picks the stretch of the series the window can show: the newest
// columns, unless the cursor sits left of them, in which case it anchors the
// left edge — walking left with h scrolls the series back.
func moodVisible(st moodState, width int) (pts []wall.MoodPoint, start, cols int) {
	cols = max(width-moodLeft-1, 4)
	start = max(len(st.drawn)-cols, 0)
	if st.cursor >= 0 && st.cursor < start {
		start = st.cursor
	}
	end := min(start+cols, len(st.drawn))
	return st.drawn[start:end], start, cols
}

// moodRows is how many screen rows one level of the scale gets. 12 lines go
// to header, face, axis, band, labels, inspector, legend, note, footer and
// the spacers; the rest is the curve's to spend. Overshooting costs the
// calibration note, which fit() drops first.
func moodRows(height int) int {
	if extra := (height - 18) / len(wall.Moods); extra > 0 {
		return min(1+extra, 3) // a taller band fills a tall window; past 3 it only gets fatter
	}
	return 1
}

// moodGraph draws the series as a curve, one column per graded post (or per
// day), oldest on the left: each column is a mark at its own level, not a bar
// filled from the floor. Filled bars were the first draft and they lose the
// plot — a real wall sits at good/ok almost all the time, so the bottom rows
// come out one solid block and only the top edge says anything.
func moodGraph(st moodState, width, height int) string {
	pts, start, _ := moodVisible(st, width)
	shown := revealed(len(pts), st.frame)
	cur := st.cursor - start
	flash := st.flash > 0

	var b strings.Builder
	rows := moodRows(height)
	for lvl := len(wall.Moods); lvl >= 1; lvl-- { // 5 (great) … 1 (stuck)
		cells := make([]moodCell, 0, shown)
		for i, p := range pts[:shown] {
			c := moodCell{ch: " ", lit: i == cur}
			switch {
			case p.Score == lvl && p.Contradicts():
				c.ch, c.color = moodContra, moodColor(p.Score)
			case p.Score == lvl:
				c.ch, c.color = moodBar, moodColor(p.Score)
				c.lit = c.lit || (flash && i == len(pts)-1)
			case i == cur:
				c.ch, c.lit = moodStem, false // findable off its own mark
			}
			cells = append(cells, c)
		}
		row := renderCells(cells)
		for r := 0; r < rows; r++ {
			label := ""
			if r == rows/2 {
				label = wall.Moods[len(wall.Moods)-lvl]
			}
			fmt.Fprintf(&b, "  %s ┤%s\n", pad(label, moodLabelW), row)
		}
	}
	// the axis spans the data, not the window: a rule running past the last
	// column puts the right-hand date where nothing was ever posted
	fmt.Fprintf(&b, "  %s └%s\n", strings.Repeat(" ", moodLabelW), strings.Repeat("─", max(len(pts), 1)))
	b.WriteString(moodOutcomeBand(pts[:shown], cur) + "\n")
	b.WriteString(moodAxis(pts) + "\n")
	return b.String()
}

// revealed is the sweep-in: at frame 0 one column is up, by moodRevealFrames
// all are. Rounding up matters below nine points, where a floor would open on
// an empty graph.
func revealed(n, frame int) int {
	if frame+1 >= moodRevealFrames {
		return n
	}
	return min((n*(frame+1)+moodRevealFrames-1)/moodRevealFrames, n)
}

// moodOutcomeBand runs under the same axis as the curve: mood is the height,
// outcome is the row below it. The pair is the point — a great mood over a
// failed outcome is the most interesting column on the wall, and neither half
// shows it on its own.
func moodOutcomeBand(pts []wall.MoodPoint, cur int) string {
	cells := make([]moodCell, 0, len(pts))
	for i, p := range pts {
		glyph, color := outcomeGlyph(p.Outcome)
		cells = append(cells, moodCell{ch: glyph, color: color, lit: i == cur})
	}
	return fmt.Sprintf("  %s  %s", styleDim.Render(pad("out", moodLabelW)), renderCells(cells))
}

// moodAxis labels the span the drawn columns actually cover — not the whole
// wall, which is usually wider than the window.
func moodAxis(pts []wall.MoodPoint) string {
	if len(pts) == 0 {
		return ""
	}
	f := func(p wall.MoodPoint) string {
		if p.N > 1 {
			return p.TS.Format("01-02")
		}
		return p.TS.Local().Format("01-02 15:04")
	}
	from, to := f(pts[0]), f(pts[len(pts)-1])
	if from == to {
		to = "" // a span of one minute is one label, not the same one twice
	}
	gap := len(pts) - len(from) - len(to)
	if gap < 1 || to == "" {
		return styleDim.Render(strings.Repeat(" ", moodLeft) + from)
	}
	return styleDim.Render(strings.Repeat(" ", moodLeft) + from + strings.Repeat(" ", gap) + to)
}

// moodInspect describes the column under the cursor: the post it is, or the
// day it folds. Without a cursor it says nothing rather than guessing at
// which column you meant.
func moodInspect(st moodState, width int) string {
	p, ok := st.at()
	if !ok {
		return ""
	}
	glyph, _ := outcomeGlyph(p.Outcome)
	mark := ""
	if p.ContraN > 0 {
		mark = " " + moodContra
		if p.N > 1 {
			mark = fmt.Sprintf(" %s %d of %d", moodContra, p.ContraN, p.N)
		}
	}
	var line string
	if p.N > 1 {
		line = fmt.Sprintf(" › %s · %s · %s %.1f · worst %s · %s%s", p.TS.Format("01-02"),
			plural(p.N, "post"), moodWord(p.Avg), p.Avg, orDash(strings.TrimSpace(glyph)), orDash(p.Repo), mark)
	} else {
		line = fmt.Sprintf(" › %s · %s · %s · %s %s%s — %s", p.TS.Local().Format("01-02 15:04"),
			orDash(p.Repo), orDash(p.Topic), orDash(strings.TrimSpace(glyph)), p.Mood, mark, p.Msg)
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// moodActorLines draws one sparkline per actor — the population's mirror
// against monoculture that stats writes in words, drawn as a shape instead.
func moodActorLines(st moodState, width int) string {
	const nameW, tailW = 16, 18 // tail holds "5.0  1234 posts" with room to spare
	sparkW := max(width-nameW-tailW-3, 8)
	var b strings.Builder
	for _, a := range wall.MoodActors(st.drawn) {
		pts := a.Points
		if len(pts) > sparkW {
			pts = pts[len(pts)-sparkW:]
		}
		n := 0
		for _, p := range a.Points {
			n += p.N
		}
		cells := make([]moodCell, 0, len(pts))
		for _, p := range pts[:revealed(len(pts), st.frame)] {
			cells = append(cells, moodCell{ch: string(sparkRune(p.Score)), color: moodColor(p.Score)})
		}
		line := fmt.Sprintf("  %s %s  %s", pad(orDash(a.Actor), nameW), renderCells(cells),
			styleDim.Render(fmt.Sprintf("%.1f  %s", a.Avg, plural(n, "post"))))
		b.WriteString(lipgloss.NewStyle().MaxWidth(width).Render(line) + "\n")
	}
	return b.String()
}

// sparkRune spreads the five scores across the eight block heights.
func sparkRune(score int) rune {
	return moodSpark[(score-1)*(len(moodSpark)-1)/(len(wall.Moods)-1)]
}

// moodLegend prints every value on the scale, zeros included: which part of
// the range never gets used is the point of the breakdown. The contradiction
// count rides along — those posts are the honest ones, and the curve marks
// each of them with a !.
func moodLegend(s wall.MoodSummary, width int) string {
	parts := make([]string, 0, len(wall.Moods)+1)
	for i, name := range wall.Moods {
		p := fmt.Sprintf("%s %d", name, s.Counts[i])
		if s.Counts[i] == 0 {
			p = styleDim.Render(p)
		}
		parts = append(parts, p)
	}
	if s.Contradicting > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Contradicting, moodContra))
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(" " + strings.Join(parts, " · "))
}

// moodNote says what the shape of the series cannot: a scale used at one
// value is a habit, not a reading. Same finding the stats calibration line
// reports, at the moment you are looking at the curve.
func moodNote(s wall.MoodSummary) string {
	switch {
	case s.Used() == 1:
		return fmt.Sprintf("1 of %d values used — a flat line is not a measurement", len(wall.Moods))
	case !s.Low():
		return "nothing below ok — the bottom half of the scale is unused"
	case s.Count*2 < s.Total:
		return fmt.Sprintf("%d of %d posts carry a mood — the rest are not in this curve", s.Count, s.Total)
	}
	return ""
}

func center(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", (width-w)/2) + s
}
