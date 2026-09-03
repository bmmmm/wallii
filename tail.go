// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

type filter struct {
	repo, topic, actor, grep string
	since                    time.Time
	// contradicting keeps only posts whose grade disagrees with their own
	// message. stats counts them and calls them the honest ones; without
	// this there was no way to actually read them.
	contradicting bool
	// grader keeps only posts that name the cheap path they saw. The field
	// is worth nothing unless it can be found again — a month of them, read
	// together, is where a rule comes from.
	grader bool
}

func (f filter) match(e wall.Event) bool {
	if f.contradicting && len(wall.Contradictions(e)) == 0 {
		return false
	}
	if f.grader && e.Grader == "" {
		return false
	}
	if f.repo != "" && !strings.EqualFold(e.Repo, f.repo) {
		return false
	}
	if f.topic != "" && !strings.EqualFold(e.Topic, f.topic) {
		return false
	}
	// an actor, or a family: "claude" is claude, claude/main and claude/ops
	// together, "claude/main" is that one actor
	if f.actor != "" && !strings.EqualFold(e.Actor, f.actor) && !strings.EqualFold(wall.ActorFamily(e.Actor), f.actor) {
		return false
	}
	if !f.since.IsZero() && e.TS.Before(f.since) {
		return false
	}
	if f.grep != "" {
		hay := strings.ToLower(e.Repo + " " + e.Topic + " " + e.Actor + " " + e.Msg + " " + e.Grader + " " + strings.Join(e.Refs, " "))
		if !strings.Contains(hay, strings.ToLower(f.grep)) {
			return false
		}
	}
	return true
}

func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if d, err := parseDur(s); err == nil {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("cannot parse --since %q — use 2006-01-02, 36h or 3d", s)
}

func cmdTail(args []string) error {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	n := fs.Int("n", 30, "number of entries (0 = all)")
	follow := fs.Bool("f", false, "keep following new posts")
	repo := fs.String("repo", "", "filter: repo name")
	topic := fs.String("topic", "", "filter: topic")
	actor := fs.String("actor", "", "filter: actor, or a family (claude, codex) — the part before / or :")
	sinceS := fs.String("since", "", "filter: 2006-01-02, 36h or 3d")
	grep := fs.String("grep", "", "filter: substring across all fields")
	contra := fs.Bool("contradicting", false, "filter: only posts whose grade disagrees with their message")
	graderF := fs.Bool("grader", false, "filter: only posts that name the cheap path they saw (--grader)")
	ids := fs.Bool("ids", false, "show event IDs (for wallii react / challenge)")
	all := fs.Bool("all", false, "render every post in full (default: 3 prime slots per actor and day, rest folded)")
	asJSON := fs.Bool("json", false, "raw NDJSON output")
	fs.Parse(args)

	since, err := parseSince(*sinceS, time.Now())
	if err != nil {
		return err
	}
	flt := filter{repo: *repo, topic: *topic, actor: *actor, grep: *grep, since: since, contradicting: *contra, grader: *graderF}

	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	events, stats, err := wall.ReadLast(dir, *n, flt.match)
	if err != nil {
		return err
	}
	reportStats(stats)
	r := newRenderer()
	// Only under the flag: naming the reason is the point when you asked for
	// these posts, and noise in every other listing.
	r.showContradictions = *contra
	r.showIDs = *ids
	// Replies render indented under the event they answer, when that event is
	// in the window; a reply whose parent fell outside still shows, marked as
	// answering something further up.
	threads := wall.Thread(events)
	shown := map[string]bool{}
	// Prime slots: per actor and day only the first three posts render in
	// full, the rest fold into one grey line. The NDJSON keeps everything —
	// scarcity lives purely in the view, and whoever knows only three lines
	// stay visible starts curating instead of telegraphing 19 of them.
	// Dialogue, registry events, filtered listings and --json stay whole.
	// Folding applies to the unfiltered wall view only: whoever filters has
	// already asked for something specific and gets all of it.
	fold := !*all && !*asJSON && !*contra && !*graderF && *grep == "" && *repo == "" && *topic == "" && *actor == ""
	folded := map[string]int{} // actor → folded count for the current day
	slot := map[string]int{}
	day := ""
	flushFolds := func() {
		for actor, n := range folded {
			r.printFold(os.Stdout, actor, n)
			delete(folded, actor)
		}
	}
	for _, e := range events {
		if (e.Kind == wall.KindReact || e.Kind == wall.KindChallenge) && shown[e.ID()] {
			continue
		}
		if fold {
			if d := e.TS.Local().Format("2006-01-02"); d != day {
				flushFolds()
				day = d
				slot = map[string]int{}
			}
			if e.Kind == "" {
				slot[e.Actor]++
				if slot[e.Actor] > primeSlots {
					folded[e.Actor]++
					continue
				}
			}
		}
		r.printThread(os.Stdout, e, threads, shown, *asJSON)
	}
	flushFolds()
	if !*follow {
		if len(events) == 0 && !*asJSON {
			fmt.Fprintln(os.Stderr, "wallii: no matching posts — wall dir:", dir)
		}
		return nil
	}
	return followLoop(dir, flt, r, *asJSON)
}

func reportStats(stats wall.ReadStats) {
	if stats.BadLines > 0 {
		fmt.Fprintf(os.Stderr, "wallii: skipped %d malformed line(s)\n", stats.BadLines)
	}
	if len(stats.SkippedFiles) > 0 {
		fmt.Fprintf(os.Stderr, "wallii: skipped unreadable file(s): %s — inspect or move them out of the wall dir\n",
			strings.Join(stats.SkippedFiles, ", "))
	}
}

func followLoop(dir string, flt filter, r *renderer, asJSON bool) error {
	emit := func(evs []wall.Event) {
		for _, e := range evs {
			if flt.match(e) {
				r.print(os.Stdout, e, asJSON)
			}
		}
	}
	path := wall.CurrentFile(dir, time.Now())
	off := fileSize(path)
	for {
		time.Sleep(500 * time.Millisecond)
		if np := wall.CurrentFile(dir, time.Now()); np != path {
			evs, _ := wall.Drain(path, off)
			emit(evs)
			path, off = np, 0
		}
		var evs []wall.Event
		evs, off = wall.Drain(path, off)
		emit(evs)
	}
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

type renderer struct {
	color   bool
	lastDay string
	// showContradictions prints, under each post, why its grade disagrees
	// with its own message. Off by default: on a normal tail it would be
	// noise, and the stats line already carries the count.
	showContradictions bool
	// showIDs prints each event's derived ID — the handle react/challenge
	// take. Off by default: a column of hex on every listing is noise until
	// you want to answer something.
	showIDs bool
}

func newRenderer() *renderer {
	fi, err := os.Stdout.Stat()
	istty := err == nil && fi.Mode()&os.ModeCharDevice != 0
	return &renderer{color: istty && os.Getenv("NO_COLOR") == ""}
}

// repoPalette: 256-color codes picked for legibility on dark and light terms.
var repoPalette = []int{33, 39, 42, 45, 75, 99, 118, 141, 161, 172, 203, 208, 214, 220}

func repoColor(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	// stay in uint32: int(Sum32()) is negative on 32-bit and panics via modulo
	return repoPalette[h.Sum32()%uint32(len(repoPalette))]
}

// outcomeGlyph maps an outcome onto its feed marker and base ANSI color
// (1 red, 2 green, 3 yellow) — a failed post must be as loud in the feed
// as it is in stats. Unreported posts keep the column blank so messages
// stay aligned.
func outcomeGlyph(outcome string) (string, int) {
	switch outcome {
	case wall.OutcomeOK:
		return "✓", 2
	case wall.OutcomePartial:
		return "◐", 3
	case wall.OutcomeFailed:
		return "✗", 1
	}
	return " ", 0
}

func (r *renderer) print(w io.Writer, e wall.Event, asJSON bool) {
	r.printAt(w, e, asJSON, 0)
}

// printThread prints e and, indented below it, every reply that answers it
// (recursively — a challenge can be answered, and the answer challenged).
func (r *renderer) printThread(w io.Writer, e wall.Event, threads map[string][]wall.Event, shown map[string]bool, asJSON bool) {
	r.printAt(w, e, asJSON, 0)
	shown[e.ID()] = true
	r.printChildren(w, e.ID(), threads, shown, asJSON, 1)
}

func (r *renderer) printChildren(w io.Writer, id string, threads map[string][]wall.Event, shown map[string]bool, asJSON bool, depth int) {
	for _, c := range threads[id] {
		if shown[c.ID()] {
			continue
		}
		shown[c.ID()] = true
		r.printAt(w, c, asJSON, depth)
		r.printChildren(w, c.ID(), threads, shown, asJSON, depth+1)
	}
}

// primeSlots is how many posts per actor and day render in full. Three is
// enough for a day's story and few enough that a 19-post day has to choose.
const primeSlots = 3

// printFold is the grey line a day's surplus collapses into.
func (r *renderer) printFold(w io.Writer, actor string, n int) {
	line := fmt.Sprintf("                 · +%d more from %s — wallii tail --all", n, orDash(actor))
	if r.color {
		fmt.Fprintf(w, "\x1b[2m%s\x1b[0m\n", line)
		return
	}
	fmt.Fprintln(w, line)
}

// replyGlyph marks dialogue in the feed: a react answers, a challenge doubts.
func replyGlyph(kind string) (string, int) {
	if kind == wall.KindChallenge {
		return "⚔", 3
	}
	return "↳", 0
}

// printReply renders a react/challenge line indented under its parent. depth
// 0 means the parent is not in the rendered window (or -f streamed it in), so
// the line names what it answers instead of hanging in the air.
func (r *renderer) printReply(w io.Writer, e wall.Event, depth int) {
	r.dayHeader(w, e.TS.Local())
	indent := strings.Repeat("  ", depth)
	glyph, gc := replyGlyph(e.Kind)
	orphan := ""
	if depth == 0 {
		orphan = " (re " + e.Parent + ")"
	}
	id := ""
	if r.showIDs {
		id = e.ID() + "  "
	}
	if !r.color {
		line := fmt.Sprintf("%s%s         %s%s %s: %s%s", id, e.TS.Local().Format("15:04"), indent, glyph, orDash(e.Actor), e.Msg, orphan)
		if len(e.Refs) > 0 {
			line += "  " + strings.Join(e.Refs, " ")
		}
		fmt.Fprintln(w, line)
		return
	}
	mark := glyph
	if gc != 0 {
		mark = fmt.Sprintf("\x1b[3%dm%s\x1b[0m", gc, glyph)
	}
	if id != "" {
		id = fmt.Sprintf("\x1b[2m%s\x1b[0m  ", e.ID())
	}
	if orphan != "" {
		orphan = fmt.Sprintf(" \x1b[2m(re %s)\x1b[0m", e.Parent)
	}
	refs := ""
	for i, u := range e.Refs {
		refs += fmt.Sprintf("  \x1b]8;;%s\x1b\\\x1b[4;34m↗%d\x1b[0m\x1b]8;;\x1b\\", u, i+1)
	}
	fmt.Fprintf(w, "%s\x1b[2m%s\x1b[0m         %s%s \x1b[2m%s:\x1b[0m %s%s%s\n",
		id, e.TS.Local().Format("15:04"), indent, mark, orDash(e.Actor), e.Msg, orphan, refs)
}

func (r *renderer) dayHeader(w io.Writer, ts time.Time) {
	day := ts.Format("2006-01-02")
	if day == r.lastDay {
		return
	}
	r.lastDay = day
	if r.color {
		fmt.Fprintf(w, "\x1b[2m── %s ──\x1b[0m\n", day)
	} else {
		fmt.Fprintf(w, "── %s ──\n", day)
	}
}

func (r *renderer) printAt(w io.Writer, e wall.Event, asJSON bool, depth int) {
	var notes []string
	if r.showContradictions {
		notes = wall.Contradictions(e)
	}
	if asJSON {
		b, _ := json.Marshal(e)
		// Additive keys only, so raw-NDJSON consumers keep working: the
		// derived id (the handle react/challenge take — without it a JSON
		// consumer could never answer anything), and the contradiction
		// reasons when they exist.
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			m["id"] = e.ID()
			if len(notes) > 0 {
				m["contradictions"] = notes
			}
			if b2, err := json.Marshal(m); err == nil {
				b = b2
			}
		}
		fmt.Fprintln(w, string(b))
		return
	}
	if e.Kind == wall.KindReact || e.Kind == wall.KindChallenge {
		r.printReply(w, e, depth)
		return
	}
	defer func() {
		// The grader is the poster's own words, like Msg and Refs — so it
		// renders on every listing, never behind a flag. mood is invisible
		// here and gets written only because a hook asks; a field that
		// lives on a hook dies with it. ↷ rather than ↳: that glyph is
		// dialogue's, and this is not a reply.
		if e.Grader != "" {
			if r.color {
				fmt.Fprintf(w, "                 \x1b[2m↷\x1b[0m %s\n", e.Grader)
			} else {
				fmt.Fprintf(w, "                 ↷ %s\n", e.Grader)
			}
		}
		for _, n := range notes {
			if r.color {
				fmt.Fprintf(w, "                    \x1b[2m↳ %s\x1b[0m\n", n)
			} else {
				fmt.Fprintf(w, "                    ↳ %s\n", n)
			}
		}
	}()
	ts := e.TS.Local()
	r.dayHeader(w, ts)
	id := ""
	if r.showIDs {
		id = e.ID() + "  "
		if r.color {
			id = fmt.Sprintf("\x1b[2m%s\x1b[0m  ", e.ID())
		}
	}
	repo := pad(e.Repo, 16)
	topic := pad(e.Topic, 10)
	glyph, gc := outcomeGlyph(e.Outcome)
	took := ""
	if e.TookS > 0 {
		took = " (" + fmtTook(e.TookS) + ")"
	}
	if !r.color {
		line := fmt.Sprintf("%s%s  %s %s %s %s%s", id, ts.Format("15:04"), repo, topic, glyph, e.Msg, took)
		if len(e.Refs) > 0 {
			line += "  " + strings.Join(e.Refs, " ")
		}
		if e.Actor != "" {
			line += "  ·" + e.Actor
		}
		fmt.Fprintln(w, line)
		return
	}
	mark := glyph
	if gc != 0 {
		mark = fmt.Sprintf("\x1b[3%dm%s\x1b[0m", gc, glyph)
	}
	if took != "" {
		took = fmt.Sprintf("\x1b[2m%s\x1b[0m", took)
	}
	refs := ""
	for i, u := range e.Refs {
		if i == 3 {
			refs += fmt.Sprintf(" \x1b[2m+%d\x1b[0m", len(e.Refs)-3)
			break
		}
		refs += fmt.Sprintf("  \x1b]8;;%s\x1b\\\x1b[4;34m↗%d\x1b[0m\x1b]8;;\x1b\\", u, i+1)
	}
	actor := ""
	if e.Actor != "" {
		actor = fmt.Sprintf("  \x1b[2m·%s\x1b[0m", e.Actor)
	}
	fmt.Fprintf(w, "%s\x1b[2m%s\x1b[0m  \x1b[38;5;%dm%s\x1b[0m \x1b[2m%s\x1b[0m %s %s%s%s%s\n",
		id, ts.Format("15:04"), repoColor(e.Repo), repo, topic, mark, e.Msg, took, refs, actor)
}

func pad(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}
