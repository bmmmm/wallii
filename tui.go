// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
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
}

func newTUI(dir string, events []wall.Event) *tuiModel {
	m := &tuiModel{dir: dir, events: events, mode: modeList}
	m.tailPath = wall.CurrentFile(dir, time.Now())
	m.tailOff = fileSize(m.tailPath)
	m.refilter()
	return m
}

func (m *tuiModel) Init() tea.Cmd { return tickCmd() }

func (m *tuiModel) refilter() {
	m.view = m.view[:0]
	q := strings.ToLower(m.search)
	for i := len(m.events) - 1; i >= 0; i-- {
		e := m.events[i]
		if m.repoF != "" && !strings.EqualFold(e.Repo, m.repoF) {
			continue
		}
		if m.topicF != "" && !strings.EqualFold(e.Topic, m.topicF) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(e.Repo + " " + e.Topic + " " + e.Actor + " " + e.Msg + " " + strings.Join(e.Refs, " "))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		m.view = append(m.view, i)
	}
	if m.cursor >= len(m.view) {
		m.cursor = max(0, len(m.view)-1)
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
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
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
	case "/":
		m.mode = modeSearch
	case "esc":
		m.search, m.repoF, m.topicF = "", "", ""
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

func (m *tuiModel) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.mode == modeDetail {
		return m.viewDetail()
	}
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+rows {
		m.scroll = m.cursor - rows + 1
	}
	end := min(m.scroll+rows, len(m.view))
	for i := m.scroll; i < end; i++ {
		b.WriteString(m.line(m.events[m.view[i]], i == m.cursor) + "\n")
	}
	for i := end - m.scroll; i < rows; i++ {
		b.WriteString("\n")
	}
	b.WriteString(m.footer())
	return b.String()
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
		plain := fmt.Sprintf("▶%s  %s %s %s%s", tstr, pad(e.Repo, 16), pad(e.Topic, 10), e.Msg, refs)
		return styleSel.MaxWidth(m.width).Render(plain)
	}
	repoSt := lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(repoColor(e.Repo))))
	line := fmt.Sprintf(" %s  %s %s %s%s",
		styleDim.Render(tstr),
		repoSt.Render(pad(e.Repo, 16)),
		styleDim.Render(pad(e.Topic, 10)),
		e.Msg,
		styleDim.Render(refs),
	)
	return lipgloss.NewStyle().MaxWidth(m.width).Render(line)
}

func (m *tuiModel) footer() string {
	hint := " j/k move · enter detail · / search · r/t filter repo/topic · o open ref · esc clear · q quit"
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
