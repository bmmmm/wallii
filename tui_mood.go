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
	// moodKeep is wider than any terminal, so resizing the window never
	// needs the trail recomputed — the renderer just draws fewer columns.
	moodKeep = 512
)

const moodBar = "█"

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

// enterMood opens the panel: the trail is folded once here (and again on
// ingest), never per frame, and the epoch bump orphans any tick left over
// from a previous visit.
func (m *tuiModel) enterMood() tea.Cmd {
	m.mode = modeMood
	m.moodEpoch++
	m.refreshMood()
	m.moodFrame, m.moodFlash = 0, 0 // opening the panel is not an arrival
	return moodTickCmd(m.moodEpoch)
}

// refreshMood re-folds the trail from the events the list currently shows,
// so repo/topic/search filters carry into the panel: what you filtered the
// wall down to is the wall whose mood you are reading.
func (m *tuiModel) refreshMood() {
	evs := make([]wall.Event, 0, len(m.view))
	for i := len(m.view) - 1; i >= 0; i-- { // view is newest first, the trail runs oldest first
		evs = append(evs, m.events[m.view[i]])
	}
	before := m.moodTrail.Count
	m.moodTrail = wall.MoodTrail(evs, moodKeep)
	if m.moodTrail.Count > before {
		m.moodFlash = moodFlashFrames
	}
}

func (m *tuiModel) viewMood() string {
	return renderMood(m.moodTrail, m.width, m.height, m.moodFrame, m.moodFlash, m.note)
}

func renderMood(s wall.MoodSummary, width, height, frame, flash int, note string) string {
	blink := frame%moodBlinkEvery < moodBlinkFrames
	// a short window spends its lines on the curve, not on air around it
	gap := ""
	if height >= 16 {
		gap = "\n"
	}
	var b strings.Builder
	b.WriteString(moodHeader(s, width) + "\n" + gap)

	if s.Count == 0 {
		b.WriteString(center(styleHeader.Render(moodFaceNone), width) + "\n" + gap)
		b.WriteString(center(styleDim.Render(fmt.Sprintf("no mood on any of these %d posts", s.Total)), width) + "\n")
		b.WriteString(center(styleDim.Render("post with --mood great|good|ok|rough|stuck to fill this in"), width) + "\n")
		return fit(b.String(), height) + moodFooter(note, width, false)
	}

	face := fmt.Sprintf("%s   %s · %.1f", faceFor(s.Avg, blink), moodWord(s.Avg), s.Avg)
	b.WriteString(center(styleHeader.Render(face), width) + "\n" + gap)
	b.WriteString(moodGraph(s.Points, width, height, frame, flash))
	b.WriteString(gap + moodLegend(s, width) + "\n")
	if n := moodNote(s); n != "" {
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

func moodHeader(s wall.MoodSummary, width int) string {
	left := fmt.Sprintf(" wallii · mood · %d of %d posts graded", s.Count, s.Total)
	return styleHeader.MaxWidth(width).Render(left)
}

func moodFooter(note string, width int, graph bool) string {
	hint := " m/esc back"
	if graph {
		hint += " · one column = one graded post, oldest left"
	}
	if note != "" {
		hint = " " + note + " ·" + hint
	}
	return styleDim.MaxWidth(width).Render(hint)
}

// moodGraph draws the series as a curve, one column per graded post, oldest
// on the left: each column is a mark at its own level, not a bar filled from
// the floor. Filled bars were the first draft and they lose the plot — a
// real wall sits at good/ok almost all the time, so the bottom three rows
// come out a solid block and only the top edge says anything. The mark takes
// the color of its score, so a drop reads before you find the row.
func moodGraph(pts []wall.MoodPoint, width, height, frame, flash int) string {
	const labelW = 5
	left := 2 + labelW + 2 // "  great ┤"
	cols := width - left - 1
	if cols < 4 {
		cols = 4
	}
	if len(pts) > cols {
		pts = pts[len(pts)-cols:]
	}
	// the sweep-in: at frame 0 one column is up, by moodRevealFrames all are.
	// Rounding up matters below nine points, where a floor would open on an
	// empty graph.
	shown := (len(pts)*(frame+1) + moodRevealFrames - 1) / moodRevealFrames
	if shown > len(pts) || frame+1 >= moodRevealFrames {
		shown = len(pts)
	}

	// 10 lines go to header, face, axis, labels, legend, note, footer and
	// the three spacers; the rest is the curve's to spend. Overshooting here
	// costs the calibration note, which fit() would drop first.
	rows := 1
	if extra := (height - 16) / len(wall.Moods); extra > 0 {
		rows = min(1+extra, 3) // a taller band fills a tall window; past 3 it only gets fatter
	}

	var b strings.Builder
	for lvl := len(wall.Moods); lvl >= 1; lvl-- { // 5 (great) … 1 (stuck)
		for r := 0; r < rows; r++ {
			label := ""
			if r == rows/2 {
				label = wall.Moods[len(wall.Moods)-lvl]
			}
			fmt.Fprintf(&b, "  %s ┤%s\n", pad(label, labelW), moodRow(pts[:shown], lvl, flash > 0))
		}
	}
	fmt.Fprintf(&b, "  %s └%s\n", strings.Repeat(" ", labelW), strings.Repeat("─", max(0, cols)))
	b.WriteString(moodAxis(pts, left, cols) + "\n")
	return b.String()
}

// moodRow renders one level's row — the columns whose score is exactly this
// level — batching runs of equal color into a single styled write, since a
// style per cell would be a few thousand renders a second at 10fps.
func moodRow(pts []wall.MoodPoint, lvl int, flashLast bool) string {
	var b strings.Builder
	run, runColor := 0, -1
	flush := func() {
		if run == 0 {
			return
		}
		s := strings.Repeat(moodBar, run)
		if runColor > 0 {
			s = lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(runColor))).Render(s)
		}
		b.WriteString(s)
		run, runColor = 0, -1
	}
	for i, p := range pts {
		if p.Score != lvl {
			flush()
			b.WriteString(" ")
			continue
		}
		// The newest column stays lit for a few frames after it lands, so a
		// post arriving while you watch is an event, not a redraw. The lit
		// cell is a reversed space, not a reversed block: reverse paints the
		// glyph in the background color, and a full block covers its whole
		// cell — so ▛ reversed is an invisible one.
		if flashLast && i == len(pts)-1 {
			flush()
			b.WriteString(styleSel.Render(" "))
			continue
		}
		if c := moodColor(p.Score); c != runColor {
			flush()
			runColor = c
		}
		run++
	}
	flush()
	return b.String()
}

// moodAxis labels the span the drawn columns actually cover — not the whole
// wall, which is usually wider than the window.
func moodAxis(pts []wall.MoodPoint, left, cols int) string {
	if len(pts) == 0 {
		return ""
	}
	f := func(t time.Time) string { return t.Local().Format("01-02 15:04") }
	from, to := f(pts[0].TS), f(pts[len(pts)-1].TS)
	if from == to {
		to = "" // a span of one minute is one label, not the same one twice
	}
	gap := cols - len(from) - len(to)
	if gap < 1 || to == "" {
		return styleDim.Render(strings.Repeat(" ", left) + from)
	}
	return styleDim.Render(strings.Repeat(" ", left) + from + strings.Repeat(" ", gap) + to)
}

// moodLegend prints every value on the scale, zeros included: which part of
// the range never gets used is the point of the breakdown.
func moodLegend(s wall.MoodSummary, width int) string {
	parts := make([]string, 0, len(wall.Moods))
	for i, name := range wall.Moods {
		p := fmt.Sprintf("%s %d", name, s.Counts[i])
		if s.Counts[i] == 0 {
			p = styleDim.Render(p)
		}
		parts = append(parts, p)
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
