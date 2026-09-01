// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bmmmm/wallii/internal/wall"
)

const (
	modeList   = "list"
	modeDetail = "detail"
	modeSearch = "search"
	modeMood   = "mood"
)

var (
	styleHeader = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleSel    = lipgloss.NewStyle().Reverse(true)
)

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() > 0 {
		return fmt.Errorf("tui takes no arguments (got %q) — filter interactively with / r t", fs.Arg(0))
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	// TODO: cap the initial load (e.g. -n 5000) before the wall grows into
	// years of history — this reads everything into RAM.
	events, stats, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		return err
	}
	m := newTUI(dir, events)
	if stats.BadLines > 0 || len(stats.SkippedFiles) > 0 {
		m.note = fmt.Sprintf("skipped: %d bad line(s), %d unreadable file(s)", stats.BadLines, len(stats.SkippedFiles))
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

type tuiModel struct {
	dir            string
	events         []wall.Event
	view           []int // indexes into events, newest first
	cursor, scroll int
	width, height  int
	mode           string
	search         string
	repoF, topicF  string
	tailPath       string
	tailOff        int64
	note           string
	// window bounds the whole view in time — "" (all), today, 7d, 30d. The
	// list and the mood panel read the same one: what you are looking at and
	// what the curve measures can never drift apart.
	window string
	// dayF pins the list to a single day. Only the mood panel sets it, when
	// you jump into a folded day column — the question a day column raises is
	// "what happened then", and the answer is that day's posts and no others.
	dayF time.Time

	mood moodState
}

func newTUI(dir string, events []wall.Event) *tuiModel {
	m := &tuiModel{dir: dir, events: events, mode: modeList}
	m.mood.cursor = moodNoCursor
	m.tailPath = wall.CurrentFile(dir, time.Now())
	m.tailOff = fileSize(m.tailPath)
	m.refilter()
	return m
}

func (m *tuiModel) Init() tea.Cmd { return tickCmd() }

func (m *tuiModel) refilter() {
	m.view = m.view[:0]
	q := strings.ToLower(m.search)
	since := windowStart(m.window, time.Now())
	for i := len(m.events) - 1; i >= 0; i-- {
		if m.passes(m.events[i], since, q, true) {
			m.view = append(m.view, i)
		}
	}
	if m.cursor >= len(m.view) {
		m.cursor = max(0, len(m.view)-1)
	}
}

// passes is the view's filter, in one place so the mood panel can use it
// while skipping exactly one clause of it. withDayPin is that clause: see
// panelEvents for why the panel does not honor the pin.
func (m *tuiModel) passes(e wall.Event, since time.Time, q string, withDayPin bool) bool {
	if !since.IsZero() && e.TS.Before(since) {
		return false
	}
	if withDayPin && !m.dayF.IsZero() && !sameDay(e.TS.Local(), m.dayF) {
		return false
	}
	if m.repoF != "" && !strings.EqualFold(e.Repo, m.repoF) {
		return false
	}
	if m.topicF != "" && !strings.EqualFold(e.Topic, m.topicF) {
		return false
	}
	if q != "" {
		hay := strings.ToLower(e.Repo + " " + e.Topic + " " + e.Actor + " " + e.Msg + " " + strings.Join(e.Refs, " "))
		if !strings.Contains(hay, q) {
			return false
		}
	}
	return true
}

// windowKeys maps the number row onto the time windows: 1 today, 2 a week,
// 3 a month, 0 the whole wall.
var windowKeys = map[string]string{"1": "today", "2": "7d", "3": "30d", "0": ""}

// windowStart resolves a window to its cutoff. Computed per filter run rather
// than stored, so "today" still means today once midnight passes with the TUI
// open — a stored cutoff would quietly keep showing yesterday.
func windowStart(window string, now time.Time) time.Time {
	switch window {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "7d":
		return now.AddDate(0, 0, -7)
	case "30d":
		return now.AddDate(0, 0, -30)
	}
	return time.Time{}
}

// setWindow re-bounds the whole view: the list jumps to the newest post and
// the panel refolds, because both now describe a different stretch of wall.
func (m *tuiModel) setWindow(w string) {
	m.window = w
	m.cursor, m.scroll = 0, 0
	m.refilter()
	if m.mode == modeMood {
		m.refreshMood()
		m.mood.frame = 0 // a different series deserves its own sweep
	}
}

// ingest pulls new posts into the model. The cursor sticks to the top when
// it was there; otherwise it keeps pointing at the same event even though
// newly arrived posts shift every view position.
func (m *tuiModel) ingest() {
	var fresh []wall.Event
	if np := wall.CurrentFile(m.dir, time.Now()); np != m.tailPath {
		evs, _ := wall.Drain(m.tailPath, m.tailOff)
		fresh = append(fresh, evs...)
		m.tailPath, m.tailOff = np, 0
	}
	evs, off := wall.Drain(m.tailPath, m.tailOff)
	m.tailOff = off
	fresh = append(fresh, evs...)
	if len(fresh) == 0 {
		return
	}
	stick := m.cursor == 0
	selIdx := -1
	if !stick && m.cursor < len(m.view) {
		selIdx = m.view[m.cursor]
	}
	m.events = append(m.events, fresh...)
	m.refilter()
	if m.mode == modeMood {
		before := m.mood.trail.Count
		m.refreshMood()
		if m.mood.trail.Count > before {
			m.mood.flash = moodFlashFrames
		}
	}
	if stick {
		m.cursor, m.scroll = 0, 0
		return
	}
	for vi, ei := range m.view {
		if ei == selIdx {
			m.cursor = vi
			break
		}
	}
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.ingest()
		return m, tickCmd()
	case moodTickMsg:
		if msg.epoch != m.mood.epoch || m.mode != modeMood {
			return m, nil // a clock from an earlier visit — let it die
		}
		m.mood.frame++
		if m.mood.flash > 0 {
			m.mood.flash--
		}
		return m, moodTickCmd(msg.epoch)
	case moodPulseMsg:
		if msg.epoch != m.mood.epoch || m.mode != modeMood {
			return m, nil // a probe from an earlier visit — its reading is not this one's
		}
		m.mood.pulse, m.mood.pulsing = msg.pulse, false
		return m, moodPulseDueCmd(msg.epoch)
	case moodPulseDueMsg:
		if msg.epoch != m.mood.epoch || m.mode != modeMood {
			return m, nil // the panel is closed: nothing to measure for
		}
		m.mood.pulsing = true
		return m, moodPulseCmd(msg.epoch)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *tuiModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.note = ""
	if m.mode == modeSearch {
		switch k.Type {
		case tea.KeyEsc:
			m.search, m.mode = "", modeList
			m.refilter()
		case tea.KeyEnter:
			m.mode = modeList
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyBackspace:
			if r := []rune(m.search); len(r) > 0 {
				m.search = string(r[:len(r)-1])
				m.refilter()
			}
		case tea.KeySpace:
			m.search += " "
			m.refilter()
		case tea.KeyRunes:
			m.search += string(k.Runes)
			m.refilter()
		}
		return m, nil
	}
	if m.mode == modeDetail {
		switch k.String() {
		case "esc", "q", "enter":
			m.mode = modeList
		case "o":
			m.openRef()
		case "c":
			m.openSession()
		case "y":
			m.yankCmd()
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	if m.mode == modeMood {
		return m.handleMoodKey(k)
	}
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.view)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = max(0, len(m.view)-1)
	case "enter":
		if len(m.view) > 0 {
			m.mode = modeDetail
		}
	case "m":
		return m, m.enterMood()
	case "1", "2", "3", "0":
		m.setWindow(windowKeys[k.String()])
	case "/":
		m.mode = modeSearch
	case "esc":
		// peel one layer: the day pin is the newest and narrowest filter, and
		// dropping it along with everything else costs a search you may still
		// want. A second esc clears the rest.
		if !m.dayF.IsZero() {
			m.dayF = time.Time{}
		} else {
			m.search, m.repoF, m.topicF = "", "", ""
		}
		m.refilter()
	case "r":
		if e, ok := m.selected(); ok {
			if m.repoF == "" {
				m.repoF = e.Repo
			} else {
				m.repoF = ""
			}
			m.cursor = 0
			m.refilter()
		}
	case "t":
		if e, ok := m.selected(); ok {
			if m.topicF == "" {
				m.topicF = e.Topic
			} else {
				m.topicF = ""
			}
			m.cursor = 0
			m.refilter()
		}
	case "o":
		m.openRef()
	case "c":
		m.openSession()
	case "y":
		m.yankCmd()
	}
	return m, nil
}

func (m *tuiModel) selected() (wall.Event, bool) {
	if m.cursor < 0 || m.cursor >= len(m.view) {
		return wall.Event{}, false
	}
	return m.events[m.view[m.cursor]], true
}

func (m *tuiModel) openRef() {
	e, ok := m.selected()
	if !ok || len(e.Refs) == 0 {
		m.note = "no refs on this post"
		return
	}
	opener := "open"
	if runtime.GOOS != "darwin" {
		opener = "xdg-open"
	}
	cmd := exec.Command(opener, e.Refs[0])
	if err := cmd.Start(); err != nil {
		m.note = "open failed: " + err.Error()
		return
	}
	go cmd.Wait() // reap — otherwise every 'o' leaves a zombie until quit
	m.note = "opened " + e.Refs[0]
}

// openSession starts a follow-up AI session in the post's repo via the
// user-configured WALLII_SPAWN_CMD; without one it hands over a paste-ready
// command instead of guessing at terminal automation.
func (m *tuiModel) openSession() {
	e, ok := m.selected()
	if !ok {
		return
	}
	dir, found := resolveRepoDir(e.Repo)
	if !found {
		m.note = fmt.Sprintf("no local checkout for %q — set WALLII_REPO_ROOTS", e.Repo)
		return
	}
	prompt := followUpPrompt(e)
	how, err := spawnSession(dir, prompt)
	if errors.Is(err, errNoSpawner) {
		if cerr := copyToClipboard(sessionCmd(dir, prompt)); cerr != nil {
			m.note = "clipboard failed: " + cerr.Error()
			return
		}
		m.note = "no spawner found — command copied, paste into a new pane"
		return
	}
	if err != nil {
		m.note = "spawn failed: " + err.Error()
		return
	}
	m.note = how + " · " + dir
}

func (m *tuiModel) yankCmd() {
	e, ok := m.selected()
	if !ok {
		return
	}
	dir, _ := resolveRepoDir(e.Repo)
	if err := copyToClipboard(sessionCmd(dir, followUpPrompt(e))); err != nil {
		m.note = "clipboard failed: " + err.Error()
		return
	}
	m.note = "command copied to clipboard"
}

func (m *tuiModel) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.mode == modeDetail {
		return m.viewDetail()
	}
	if m.mode == modeMood {
		return m.viewMood()
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
	// the selected row may span several lines — budget in screen lines, not
	// entries, and keep scroll such that the whole expanded row fits
	selLine, selH := "", 0
	if m.cursor >= 0 && m.cursor < len(m.view) {
		selLine = m.line(m.events[m.view[m.cursor]], true)
		if selH = lipgloss.Height(selLine); selH > rows {
			selLine = truncLines(selLine, rows)
			selH = rows
		}
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor > m.scroll+rows-selH {
		m.scroll = m.cursor - (rows - selH)
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	used := 0
	for i := m.scroll; i < len(m.view) && used < rows; i++ {
		l, h := m.line(m.events[m.view[i]], false), 1
		if i == m.cursor {
			l, h = selLine, selH
		}
		if used+h > rows {
			break
		}
		b.WriteString(l + "\n")
		used += h
	}
	if len(m.view) == 0 {
		b.WriteString(styleDim.Render(" no matching posts — esc clears filters") + "\n")
		used++
	}
	for ; used < rows; used++ {
		b.WriteString("\n")
	}
	b.WriteString(m.footer())
	return b.String()
}

func truncLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

// header counts what the list actually shows (the filtered view), not the
// full store — "12 posts" over 3 filtered rows reads like a bug.
func (m *tuiModel) header() string {
	today := 0
	repos := map[string]struct{}{}
	now := time.Now()
	for _, ei := range m.view {
		e := m.events[ei]
		repos[e.Repo] = struct{}{}
		if sameDay(e.TS.Local(), now) {
			today++
		}
	}
	s := styleHeader.Render(fmt.Sprintf(" wallii · %d posts · %d today · %d repos", len(m.view), today, len(repos)))
	var fl []string
	if !m.dayF.IsZero() {
		fl = append(fl, m.dayF.Format("01-02"))
	}
	if m.window != "" {
		fl = append(fl, m.window)
	}
	if m.repoF != "" {
		fl = append(fl, "repo="+m.repoF)
	}
	if m.topicF != "" {
		fl = append(fl, "topic="+m.topicF)
	}
	if m.search != "" || m.mode == modeSearch {
		fl = append(fl, "/"+m.search)
	}
	if len(fl) > 0 {
		s += styleDim.Render("  [" + strings.Join(fl, " ") + "]")
	}
	if m.mode == modeSearch {
		s += styleHeader.Render("▌")
	}
	return s
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

func (m *tuiModel) line(e wall.Event, sel bool) string {
	ts := e.TS.Local()
	tstr := ts.Format("01-02 15:04")
	if sameDay(ts, time.Now()) {
		tstr = "      " + ts.Format("15:04")
	}
	refs := ""
	if n := len(e.Refs); n > 0 {
		refs = fmt.Sprintf(" ↗%d", n)
	}
	if sel {
		// the selected row expands: full message wrapped, actor and ref URLs
		// on their own lines — nothing on the wall is unreadable in place
		var sb strings.Builder
		fmt.Fprintf(&sb, "▶%s  %s %s %s", tstr, pad(e.Repo, 16), pad(e.Topic, 10), e.Msg)
		if e.Actor != "" {
			fmt.Fprintf(&sb, "  — %s", e.Actor)
		}
		if e.Outcome != "" {
			fmt.Fprintf(&sb, " · %s", e.Outcome)
		}
		if e.Mood != "" {
			fmt.Fprintf(&sb, " · mood %s", e.Mood)
		}
		if e.TookS > 0 {
			fmt.Fprintf(&sb, " · %s", fmtTook(e.TookS))
		}
		for i, u := range e.Refs {
			if i == 3 {
				fmt.Fprintf(&sb, "\n   … +%d more refs", len(e.Refs)-3)
				break
			}
			fmt.Fprintf(&sb, "\n   ↗ %s", u)
		}
		return styleSel.Width(m.width).Render(sb.String())
	}
	repoSt := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(repoColor(e.Repo))))
	glyph, gc := outcomeGlyph(e.Outcome)
	mark := glyph
	if gc != 0 {
		mark = lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(gc))).Render(glyph)
	}
	took := ""
	if e.TookS > 0 {
		took = styleDim.Render(" (" + fmtTook(e.TookS) + ")")
	}
	line := fmt.Sprintf(" %s  %s %s %s %s%s%s",
		styleDim.Render(tstr),
		repoSt.Render(pad(e.Repo, 16)),
		styleDim.Render(pad(e.Topic, 10)),
		mark,
		e.Msg,
		took,
		styleDim.Render(refs),
	)
	return lipgloss.NewStyle().MaxWidth(m.width).Render(line)
}

func (m *tuiModel) footer() string {
	hint := " j/k · enter detail · m mood · 1/2/3/0 window · / search · r/t filter · c session · y copy · o ref · esc clear · q quit"
	if !m.dayF.IsZero() {
		hint = " " + m.dayF.Format("2006-01-02") + " only ·" + hint
	}
	if m.note != "" {
		hint = " " + m.note + " ·" + hint
	}
	return styleDim.MaxWidth(m.width).Render(hint)
}

func (m *tuiModel) viewDetail() string {
	e, ok := m.selected()
	if !ok {
		return styleDim.Render(" nothing selected — esc to go back")
	}
	var b strings.Builder
	b.WriteString(styleHeader.Render(" wallii · post detail") + "\n\n")
	field := func(k, v string) {
		if v != "" {
			b.WriteString(fmt.Sprintf("  %s %s\n", styleDim.Render(pad(k, 7)), v))
		}
	}
	field("when", e.TS.Local().Format("2006-01-02 15:04:05 MST"))
	field("repo", e.Repo)
	field("topic", e.Topic)
	field("actor", e.Actor)
	field("outcome", e.Outcome)
	field("mood", e.Mood)
	if e.TookS > 0 {
		field("took", fmtTook(e.TookS))
	}
	b.WriteString("\n  " + lipgloss.NewStyle().Width(max(20, m.width-4)).Render(e.Msg) + "\n")
	if len(e.Refs) > 0 {
		b.WriteString("\n")
		for i, u := range e.Refs {
			b.WriteString(fmt.Sprintf("  %s %s\n", styleDim.Render(fmt.Sprintf("ref%d", i+1)), u))
		}
	}
	b.WriteString("\n" + styleDim.Render(" o open first ref · esc back"))
	return b.String()
}
