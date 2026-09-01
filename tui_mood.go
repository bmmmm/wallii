// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"fmt"
	"math"
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
	// moodPulseEvery is how often the API gets timed while the panel is open.
	// Slow enough that reading the curve is not a stream of requests, fast
	// enough that an outage shows up while you are still looking at it.
	moodPulseEvery = 20 * time.Second
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
	// The latency line. Its height is the mood the waiting still allows — top
	// of the scale when turns are quick, one row down for every step the
	// waiting takes off a grade — so the gap between it and the curve is the
	// drag, read straight off the picture instead of a number.
	//
	// Three glyphs, not one: seconds are a continuous axis laid over five
	// discrete rows, and a line that snapped to row centers would put 16s and
	// 29s in the same place. Top, middle and bottom of the cell give the
	// position three times the resolution the rows have.
	moodLineTop = "▔"
	moodLine    = "─"
	moodLineBot = "▁"
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
	// pulse is the live half of the reading: how fast the API answered last
	// time it was asked. pulsing says a probe is out, so the panel can show
	// that it is measuring instead of showing nothing and looking broken.
	pulse   wall.Pulse
	pulsing bool
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

// The pulse runs on its own two-step clock, next to the animation's: probe,
// then wait moodPulseEvery for the next one. Both carry the panel's epoch, so
// a probe fired on an earlier visit dies on arrival like a stale frame — and
// the waiting one never becomes a request nobody asked for.
type (
	moodPulseMsg struct {
		epoch int
		pulse wall.Pulse
	}
	moodPulseDueMsg struct{ epoch int }
)

// moodPulseCmd takes the reading off the render path: bubbletea runs a Cmd in
// its own goroutine, so even a probe that has to time out costs the panel no
// frames. It asks the same way a post does — the session's own number first —
// so the head and the stored posts can never disagree about what a turn costs.
func moodPulseCmd(epoch int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), wall.PulseTimeout)
		defer cancel()
		p, _ := wall.SessionPulse(ctx)
		return moodPulseMsg{epoch, p}
	}
}

func moodPulseDueCmd(epoch int) tea.Cmd {
	return tea.Tick(moodPulseEvery, func(time.Time) tea.Msg { return moodPulseDueMsg{epoch} })
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

// The crashout: no API answering, so no work getting done at any grade. It
// gets a face and a word of its own rather than borrowing stuck's, because
// stuck is something an actor reports about their day, and this is something
// the machine reports about itself.
const (
	moodFaceCrash = "( ✖_✖ )"
	moodCrashWord = "crashout"
)

func faceFor(avg float64, blinking bool) string {
	f := moodFaces[moodIndex(avg)]
	if blinking && f.blink != "" {
		return f.blink
	}
	return f.open
}

// moodColor maps a score onto the same palette the outcome glyphs use —
// 2 green, 3 yellow, 1 red — and returns "" for ok, which stays the terminal's
// own foreground. Color marks friction; the unremarkable middle gets none.
func moodColor(score int) string {
	switch score {
	case 5, 4:
		return "2"
	case 2:
		return "3"
	case 1:
		return "1"
	}
	return ""
}

// colorPulse is the latency line: a 256-color pink, deliberately outside the
// red/yellow/green the grades and outcomes use. It is not a worse or better
// value on the same scale — it is the other measurement, and it must not read
// as one more shade of "bad".
const colorPulse = "213"

// colorOf turns the outcome palette's ANSI number into a style color, mapping
// its 0 ("no color") onto the empty string moodCell uses for the same thing.
func colorOf(ansi int) string {
	if ansi == 0 {
		return ""
	}
	return strconv.Itoa(ansi)
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
	return tea.Batch(moodTickCmd(m.mood.epoch), m.moodPulse())
}

// moodPulse starts the panel's latency clock. A reading younger than the
// cadence still stands — toggling m twice in a second is a keystroke, not a
// reason to ask the API again — and with probing switched off the panel keeps
// its offline shape: no line, no request, no claim about an API nobody timed.
func (m *tuiModel) moodPulse() tea.Cmd {
	if !wall.PulseEnabled() {
		return nil
	}
	if m.mood.pulse.Fresh(time.Now(), moodPulseEvery) {
		return moodPulseDueCmd(m.mood.epoch)
	}
	m.mood.pulsing = true
	return moodPulseCmd(m.mood.epoch)
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
	now := st.trail.Now(st.pulse)
	b.WriteString(moodHeader(st, width, window, pin) + "\n" + gap)

	if st.trail.Count == 0 {
		b.WriteString(moodVerdict(st, now, blink, width) + "\n" + gap)
		b.WriteString(center(styleDim.Render(fmt.Sprintf("no mood on any of these %d posts", st.trail.Total)), width) + "\n")
		b.WriteString(center(styleDim.Render("post with --mood great|good|ok|rough|stuck to fill this in"), width) + "\n")
		return fit(b.String(), height) + moodFooter(note, width, false)
	}

	b.WriteString(moodVerdict(st, now, blink, width) + "\n" + gap)
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
		// clipped like every other row: the note is the longest line in the
		// panel and a narrow window would wrap it into the footer
		b.WriteString(" " + styleDim.MaxWidth(max(width-1, 1)).Render(n) + "\n")
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

// moodVerdict is the line under the header: the face and the word for how it
// is right now, followed by the terms that produced them. The receipt rides
// on the same line on purpose — the panel spends its rows on the curve, and a
// number nobody can check is worth less than the row it would cost.
func moodVerdict(st moodState, now wall.MoodNow, blink bool, width int) string {
	line := styleHeader.Render(moodHead(now, blink))
	if r := moodReceipt(st, now); r != "" {
		line += styleDim.Render("   " + r)
	}
	return center(lipgloss.NewStyle().MaxWidth(width).Render(line), width)
}

// moodHead names the state in a face and a word: the crashout and the ungraded
// wall are the two the average cannot carry, because one is measured off the
// API and the other was never posted at all.
func moodHead(now wall.MoodNow, blink bool) string {
	switch {
	case now.Crash:
		return fmt.Sprintf("%s   %s · no api", moodFaceCrash, moodCrashWord)
	case !now.Known:
		return moodFaceNone
	}
	return fmt.Sprintf("%s   %s · %.1f", faceFor(now.Avg, blink), moodWord(now.Avg), now.Avg)
}

// moodReceipt shows the arithmetic behind the head: what the window graded,
// what its own waiting took off that, and what it waited — with the count, so
// a mean over three posts can never pass for the whole window. The live
// reading rides along at the end as `now`, kept apart from the rest because it
// is the only term that is not about these posts.
func moodReceipt(st moodState, now wall.MoodNow) string {
	var parts []string
	if st.trail.Count > 0 {
		// the wall's own average only earns a term when the waiting moved it:
		// with no drag it is the number already printed in the head, and
		// printing it twice fills the line without adding a fact
		switch {
		case now.Crash:
			// the crashout replaces the head's number, so the wall's own is
			// the only place left to see what the day was before the verdict
			parts = append(parts, fmt.Sprintf("wall %.1f", st.trail.Avg))
		case now.Drag >= 0.05:
			parts = append(parts, fmt.Sprintf("wall %.1f − %.1f", st.trail.Avg, now.Drag))
		}
		if r, ok := st.trail.Recent(moodRecentN); ok {
			parts = append(parts, fmt.Sprintf("last %d · %.1f%s", moodRecentN, r, trendMark(r-st.trail.Avg)))
		}
	} else if st.pulse.Known() || st.pulsing {
		parts = append(parts, "no grades")
	}
	if st.trail.PulseTurns > 0 {
		parts = append(parts, fmt.Sprintf("api ~%s over %s",
			pulseDur(time.Duration(st.trail.PulseMS)*time.Millisecond), plural(st.trail.PulseTurns, "post")))
	}
	if st.trail.PulseDown > 0 {
		parts = append(parts, fmt.Sprintf("%d with none", st.trail.PulseDown))
	}
	if st.pulse.Known() || st.pulsing {
		parts = append(parts, moodNowTerm(st.pulse))
	}
	return strings.Join(parts, " · ")
}

// moodRecentN is how many posts count as "lately". Enough that one post
// cannot swing it, few enough that it moves inside a session — a number, not
// a duration, because a quiet week would leave a duration empty exactly when
// the wall has something to say.
const moodRecentN = 10

// moodTrendGap is when the divergence stops being noise and becomes a finding
// worth the note line: a third of a step is three of ten posts having moved.
const moodTrendGap = 0.3

// trendMark says which way the recent stretch sits against the whole. Nothing
// when they agree: an arrow on a difference of 0.02 is a claim about noise.
func trendMark(diff float64) string {
	switch {
	case diff <= -0.05:
		return " ↓"
	case diff >= 0.05:
		return " ↑"
	}
	return ""
}

// moodNowTerm is the live half in one word-pair. It says `now` for a measured
// turn because that is what separates it from the window's mean beside it; a
// probe stays a ping, and an outage keeps its reason.
func moodNowTerm(p wall.Pulse) string {
	switch {
	case !p.Known():
		return "api …"
	case !p.OK:
		return "no api — " + p.Err
	case !p.Turn():
		return "ping " + pulseDur(p.RTT)
	}
	return "now " + pulseDur(p.RTT)
}

// eventPulseTerm names the conditions one post was written under. Empty when
// the post carries no reading — most of the wall predates the field, and a
// silent row is the honest rendering of "nobody measured".
func eventPulseTerm(e wall.Event) string {
	switch e.PulseSrc {
	case "":
		return ""
	case wall.PulseNone:
		return "no api"
	case wall.PulseProbe:
		return "ping " + pulseDur(time.Duration(e.PulseMS)*time.Millisecond)
	}
	return "api " + pulseDur(time.Duration(e.PulseMS)*time.Millisecond)
}

// pointPulseTerm is the same for a curve column, which may fold a whole day:
// a mean over the posts that carry a reading, and the count of the ones
// written while nothing answered.
func pointPulseTerm(p wall.MoodPoint) string {
	switch {
	case p.PulseN == 0 && p.PulseDown == 0:
		return ""
	case p.PulseN == 0:
		return "no api"
	}
	term := "api " + pulseDur(time.Duration(p.PulseMS)*time.Millisecond)
	if p.N > 1 {
		term = "api ~" + pulseDur(time.Duration(p.PulseMS)*time.Millisecond)
	}
	if p.PulseDown > 0 {
		term += fmt.Sprintf(" · %d with none", p.PulseDown)
	}
	return term
}

// pulseDur rounds a round trip to what the eye can tell apart: milliseconds
// below a second, a tenth of one above it. The extra digits are real and
// meaningless — nobody feels the difference between 241ms and 247ms.
func pulseDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	}
	// Go writes a round minute as "1m0s"; the zero is noise in a line that is
	// already dense. "1m30s" keeps its seconds, because those are not noise.
	s := d.Round(time.Second).String()
	s = strings.TrimSuffix(s, "0m0s")
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	return s
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
	color string // "" = the terminal's own foreground
	lit   bool   // reverse video
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
		if cs[i].color != "" {
			style = style.Foreground(lipgloss.Color(cs[i].color))
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
	// The second axis on the right: the latency line's own unit, because a
	// height in mood steps is not a number anybody can read back as seconds.
	moodAxisW  = 4                 // "≤15s"
	moodRightW = 1 + 2 + moodAxisW // " ├ 15s"
)

// moodVisible picks the stretch of the series the window can show: the newest
// columns, unless the cursor sits left of them, in which case it anchors the
// left edge — walking left with h scrolls the series back.
func moodVisible(st moodState, width int) (pts []wall.MoodPoint, start, cols int) {
	right := 0
	if st.trail.PulseTurns > 0 {
		right = moodRightW
	}
	cols = max(width-moodLeft-right-1, 4)
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
	axis := st.trail.PulseTurns > 0 // the second axis only where there is a line
	// one pass per screen row rather than per level: the latency line lives
	// between the rows as often as on them, so the row is the unit
	for sr := 0; sr < len(wall.Moods)*rows; sr++ {
		lvl := len(wall.Moods) - sr/rows // 5 (great) … 1 (stuck)
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
			// the latency line runs through the same band. Where it meets a
			// mark it recolors it instead of replacing it: the grade is what
			// the panel is about, and a column whose block turns pink is
			// exactly the column where the waiting caught up with it.
			if g, ok := pulseGlyph(p, sr, rows); ok {
				c.color = colorPulse
				if c.ch == " " || c.ch == moodStem {
					c.ch = g
				}
			}
			cells = append(cells, c)
		}
		label, right := "", ""
		if sr%rows == rows/2 { // the level's own row carries both labels
			label = wall.Moods[len(wall.Moods)-lvl]
			if axis {
				right = pulseAxisLabel(lvl)
			}
		}
		fmt.Fprintf(&b, "  %s ┤%s", pad(label, moodLabelW), renderCells(cells))
		if axis {
			// the gutter is measured against the whole series, not the part
			// the sweep has revealed, so the axis does not slide in with it
			fmt.Fprintf(&b, "%s├ %s", strings.Repeat(" ", max(len(pts)-shown, 0)+1),
				styleDim.Render(pad(right, moodAxisW)))
		}
		b.WriteString("\n")
	}
	// the axis spans the data, not the window: a rule running past the last
	// column puts the right-hand date where nothing was ever posted
	fmt.Fprintf(&b, "  %s └%s\n", strings.Repeat(" ", moodLabelW), strings.Repeat("─", max(len(pts), 1)))
	b.WriteString(moodOutcomeBand(pts[:shown], cur) + "\n")
	if band := moodPulseBand(pts, shown, cur, width); band != "" {
		b.WriteString(band + "\n")
	}
	b.WriteString(moodAxis(pts) + "\n")
	return b.String()
}

// pulseY is where the latency line belongs for one column, in mood units: the
// top of the scale minus what that column's turns take off a grade. Not
// rounded to a row — the drag is continuous and the rounding would be the
// picture's largest error. False when the column measured no turn: a gap in
// the line is the honest drawing of a post nobody timed, and a line
// interpolated across it would invent the stretch it draws through.
func pulseY(p wall.MoodPoint) (float64, bool) {
	if p.PulseN == 0 {
		return 0, false
	}
	return float64(len(wall.Moods)) - wall.PulseDrag(time.Duration(p.PulseMS)*time.Millisecond), true
}

// pulseGlyph places that height on the screen: which row it falls in, and
// where inside the row. rows is how many screen rows one mood level gets, so
// a tall window buys the line proportionally more resolution — the band is
// one continuous axis, not five buckets.
func pulseGlyph(p wall.MoodPoint, screenRow, rows int) (string, bool) {
	y, ok := pulseY(p)
	if !ok {
		return "", false
	}
	// row 0 is the top of the band, which sits half a level above the top
	// level's center: a mark at level 5 belongs in the middle of the first row
	pos := (float64(len(wall.Moods)) + 0.5 - y) * float64(rows)
	if int(pos) != screenRow {
		return "", false
	}
	switch f := pos - float64(int(pos)); {
	case f < 1.0/3:
		return moodLineTop, true
	case f < 2.0/3:
		return moodLine, true
	}
	return moodLineBot, true
}

// pulseAxisLabel is the second axis: what a turn has to cost for the line to
// reach this level. Empty below the floor the drag caps at — an axis must not
// print a number no reading can produce.
func pulseAxisLabel(lvl int) string {
	drag := float64(len(wall.Moods) - lvl)
	if drag > pulseMaxDragSteps {
		return ""
	}
	d := wall.PulseAtDrag(drag)
	if drag == 0 {
		return "≤" + pulseDur(d)
	}
	return pulseDur(d)
}

// pulseMaxDragSteps mirrors the cap in wall: the line can never fall past it,
// so neither can its axis.
const pulseMaxDragSteps = 3

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
		cells = append(cells, moodCell{ch: glyph, color: colorOf(color), lit: i == cur})
	}
	return fmt.Sprintf("  %s  %s", styleDim.Render(pad("out", moodLabelW)), renderCells(cells))
}

// moodPulseBand runs under the outcome band: the same columns, the api time's
// own scale. The line above answers what the waiting cost a grade, and
// saturates on purpose — under the first anchor nothing is lost, so a window
// that swung from 2s to 6s draws a flat line at the top, which is true and
// says nothing about the swing. This row is the swing: the window's own
// min…max spread over eight heights, so the shape shows at whatever scale the
// window actually lived at.
//
// Log-spaced, because latency is: a doubling from 2s to 4s and one from 30s to
// 60s are the same event to whoever waited, and one slow outlier on a linear
// scale flattens everything else into the floor. The range is printed beside
// it — a relative shape with no numbers is a picture of nothing.
func moodPulseBand(pts []wall.MoodPoint, shown, cur, width int) string {
	// the scale comes off the whole visible series, not the part the sweep has
	// revealed: a range that rescales while the graph draws itself would show
	// three different pictures in one second
	lo, hi := int64(0), int64(0)
	for _, p := range pts {
		if p.PulseN == 0 {
			continue
		}
		if lo == 0 || p.PulseMS < lo {
			lo = p.PulseMS
		}
		if p.PulseMS > hi {
			hi = p.PulseMS
		}
	}
	if hi == 0 {
		return "" // nothing in view was timed
	}
	cells := make([]moodCell, 0, shown)
	for i, p := range pts[:shown] {
		c := moodCell{ch: " ", lit: i == cur}
		if p.PulseN > 0 {
			c.ch, c.color = string(moodSpark[sparkAt(p.PulseMS, lo, hi)]), colorPulse
		}
		cells = append(cells, c)
	}
	span := pulseDur(time.Duration(lo) * time.Millisecond)
	if hi != lo {
		span += "–" + pulseDur(time.Duration(hi)*time.Millisecond)
	}
	line := fmt.Sprintf("  %s  %s  %s", styleDim.Render(pad("api", moodLabelW)),
		renderCells(cells), styleDim.Render("log "+span))
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// sparkAt places one reading on the eight block heights, log-spaced between
// the window's own ends. A window with no spread draws a flat middle row:
// that there was nothing to see is itself the finding, and stretching it to
// full height would manufacture a shape out of rounding.
func sparkAt(ms, lo, hi int64) int {
	if hi <= lo {
		return len(moodSpark) / 2
	}
	f := (math.Log(float64(max(ms, 1))) - math.Log(float64(max(lo, 1)))) /
		(math.Log(float64(hi)) - math.Log(float64(max(lo, 1))))
	return min(max(int(f*float64(len(moodSpark)-1)+0.5), 0), len(moodSpark)-1)
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
	api := ""
	if t := pointPulseTerm(p); t != "" {
		api = " · " + t
	}
	var line string
	if p.N > 1 {
		line = fmt.Sprintf(" › %s · %s · %s %.1f · worst %s · %s%s%s", p.TS.Format("01-02"),
			plural(p.N, "post"), moodWord(p.Avg), p.Avg, orDash(strings.TrimSpace(glyph)), orDash(p.Repo), api, mark)
	} else {
		line = fmt.Sprintf(" › %s · %s · %s · %s %s%s%s — %s", p.TS.Local().Format("01-02 15:04"),
			orDash(p.Repo), orDash(p.Topic), orDash(strings.TrimSpace(glyph)), p.Mood, api, mark, p.Msg)
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
	if s.PulseTurns > 0 {
		// the line has to name itself: a pink row through a mood graph is a
		// second measurement, and an unlabelled one would read as a verdict
		key := moodLine + " api time"
		// and it has to say when it is flat on purpose. A line pinned to the
		// top of a window whose turns tripled looks broken; it is not, it is
		// reporting that none of that waiting reached a grade.
		if wall.PulseDrag(time.Duration(s.PulseMaxMS)*time.Millisecond) == 0 {
			key += " · all under " + pulseDur(wall.PulseAtDrag(0))
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color(colorPulse)).Render(key))
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(" " + strings.Join(parts, " · "))
}

// moodNote says what the shape of the series cannot: a scale used at one
// value is a habit, not a reading. Same finding the stats calibration line
// reports, at the moment you are looking at the curve.
func moodNote(s wall.MoodSummary) string {
	// The one note that is about now rather than about the scale: a headline
	// average over hundreds of posts cannot be moved by a bad afternoon, so
	// when the end of the curve has left the middle of it, the panel says so
	// instead of letting the big number speak for the small one.
	if r, ok := s.Recent(moodRecentN); ok && math.Abs(r-s.Avg) >= moodTrendGap {
		return fmt.Sprintf("the last %d average %.1f, not the %.1f above — that one spans all %d graded posts",
			moodRecentN, r, s.Avg, s.Count)
	}
	switch {
	// the line's own coverage comes first while it is thin: a pink line drawn
	// from four posts out of four hundred is a sample, and it sits in the
	// picture looking like a measurement of the whole window
	case s.PulseTurns > 0 && s.PulseTurns*4 < s.Total:
		return fmt.Sprintf("%d of %d posts measured a turn — the api line is drawn from those", s.PulseTurns, s.Total)
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
