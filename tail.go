// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
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
}

func (f filter) match(e wall.Event) bool {
	if f.repo != "" && !strings.EqualFold(e.Repo, f.repo) {
		return false
	}
	if f.topic != "" && !strings.EqualFold(e.Topic, f.topic) {
		return false
	}
	if f.actor != "" && !strings.EqualFold(e.Actor, f.actor) {
		return false
	}
	if !f.since.IsZero() && e.TS.Before(f.since) {
		return false
	}
	if f.grep != "" {
		hay := strings.ToLower(e.Repo + " " + e.Topic + " " + e.Actor + " " + e.Msg + " " + strings.Join(e.Refs, " "))
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
	if strings.HasSuffix(s, "d") {
		var days float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%f", &days); err == nil {
			return now.Add(-time.Duration(days * 24 * float64(time.Hour))), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
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
	actor := fs.String("actor", "", "filter: actor")
	sinceS := fs.String("since", "", "filter: 2006-01-02, 36h or 3d")
	grep := fs.String("grep", "", "filter: substring across all fields")
	asJSON := fs.Bool("json", false, "raw NDJSON output")
	fs.Parse(args)

	since, err := parseSince(*sinceS, time.Now())
	if err != nil {
		return err
	}
	flt := filter{repo: *repo, topic: *topic, actor: *actor, grep: *grep, since: since}

	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	events, bad, err := wall.ReadLast(dir, *n, flt.match)
	if err != nil {
		return err
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "wallii: skipped %d malformed line(s)\n", bad)
	}
	r := newRenderer()
	for _, e := range events {
		r.print(os.Stdout, e, *asJSON)
	}
	if !*follow {
		if len(events) == 0 && !*asJSON {
			fmt.Fprintln(os.Stderr, "wallii: no matching posts — wall dir:", dir)
		}
		return nil
	}
	return followLoop(dir, flt, r, *asJSON)
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
			evs, _ := drainEvents(path, off)
			emit(evs)
			path, off = np, 0
		}
		var evs []wall.Event
		evs, off = drainEvents(path, off)
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

// drainEvents parses complete new lines past off and returns the new offset;
// a partially-written trailing line stays unconsumed until its newline lands.
func drainEvents(path string, off int64) ([]wall.Event, int64) {
	if fileSize(path) <= off {
		return nil, off
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, off
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, off
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, off
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 {
		return nil, off
	}
	var out []wall.Event
	for _, line := range bytes.Split(buf[:last], []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e wall.Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, off + int64(last) + 1
}

type renderer struct {
	color   bool
	lastDay string
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
	return repoPalette[int(h.Sum32())%len(repoPalette)]
}

func (r *renderer) print(w io.Writer, e wall.Event, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(e)
		fmt.Fprintln(w, string(b))
		return
	}
	ts := e.TS.Local()
	if day := ts.Format("2006-01-02"); day != r.lastDay {
		r.lastDay = day
		if r.color {
			fmt.Fprintf(w, "\x1b[2m── %s ──\x1b[0m\n", day)
		} else {
			fmt.Fprintf(w, "── %s ──\n", day)
		}
	}
	repo := pad(e.Repo, 16)
	topic := pad(e.Topic, 10)
	if !r.color {
		line := fmt.Sprintf("%s  %s %s %s", ts.Format("15:04"), repo, topic, e.Msg)
		if len(e.Refs) > 0 {
			line += "  " + strings.Join(e.Refs, " ")
		}
		fmt.Fprintln(w, line)
		return
	}
	refs := ""
	for i, u := range e.Refs {
		if i == 3 {
			refs += fmt.Sprintf(" \x1b[2m+%d\x1b[0m", len(e.Refs)-3)
			break
		}
		refs += fmt.Sprintf("  \x1b]8;;%s\x1b\\\x1b[4;34m↗%d\x1b[0m\x1b]8;;\x1b\\", u, i+1)
	}
	fmt.Fprintf(w, "\x1b[2m%s\x1b[0m  \x1b[38;5;%dm%s\x1b[0m \x1b[2m%s\x1b[0m %s%s\n",
		ts.Format("15:04"), repoColor(e.Repo), repo, topic, e.Msg, refs)
}

func pad(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}
