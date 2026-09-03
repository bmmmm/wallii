// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

//go:embed dash.html
var dashTemplate string

// dashEvent is the compact per-post tuple inlined into the dashboard; the
// browser does all aggregation so the range filter works offline.
type dashEvent struct {
	T     int64    `json:"t"` // unix ms
	Repo  string   `json:"repo"`
	Actor string   `json:"actor,omitempty"`
	Topic string   `json:"topic,omitempty"`
	Out   string   `json:"out,omitempty"`
	Mood  string   `json:"mood,omitempty"`
	Took  int64    `json:"took,omitempty"`
	Src   string   `json:"src,omitempty"` // "auto" when wallii derived the duration
	Vs    int      `json:"vs,omitempty"`  // 1 when the grade disagrees with the message
	Refs  []string `json:"refs,omitempty"`
	Msg   string   `json:"msg"`
	// Grader is quoted under the message, never aggregated: the browser
	// gets the words, not a count of them.
	Grader string `json:"grader,omitempty"`
	// Signals are what the session's diff showed, quoted beside the poster's
	// own words for the same reason and with the same restraint: the
	// dashboard shows them, it does not count them. stats already counts
	// distinct shortcuts, and a second aggregation in the browser is a
	// second chance to count the wrong thing — which is exactly what
	// happened when that count went by posts.
	Signals []string `json:"signals,omitempty"`
}

// dashCoverage is what the c-blind card draws on: commits per local day,
// keyed exactly the way dash.html's dayKey() builds its bucket keys.
//
// The whole struct is nil — inlined as `null`, never as `[]` or `{}` — when
// nothing was measured. An empty object cannot tell "measured, no commits"
// from "nobody looked", and the card that cannot tell them apart paints a
// month of blindness out of nothing.
//
// From is mandatory for the same reason one level down: the dashboard's
// range buttons reach past the window these commits were collected for, and
// every bucket older than From has to render as a gap that says "not
// measured", never as a day with no commits on it. To closes the same
// contract at the other end — a dashboard opened a week after it was
// written must not paint the days since as days with no commits.
//
// Repos names the repos whose commits are in Days, and the card counts the
// posts of these and no others: a repo without a checkout leaves both sides
// of the ratio here exactly as it does in `wallii coverage`, or the card
// would print a ratio its own footnote contradicts.
type dashCoverage struct {
	From         int64          `json:"from"`  // unix ms, local midnight of the collected window's first day
	To           int64          `json:"to"`    // unix ms, local midnight after its last day
	Days         map[string]int `json:"days"`  // dayKey() → commits
	Repos        []string       `json:"repos"` // the measured repos — the card's numerator is their posts
	BlindCommits int            `json:"blind_commits"`
	BlindPosts   int            `json:"blind_posts"`
	Measured     int            `json:"measured"`
	OnWall       int            `json:"on_wall"`
	Others       int            `json:"others,omitempty"`
	Unresolved   []string       `json:"unresolved,omitempty"`
}

// collectDashCoverage measures the same window the dashboard inlines posts
// for. git runs here and in cmdCoverage only — both are commands a person
// types. Returns nil when no repo could be measured, which is the honest
// answer and the one the card knows how to render.
func collectDashCoverage(evs []wall.Event, wallStart, since, now time.Time) *dashCoverage {
	loc := time.Local
	from := since
	if from.IsZero() {
		// no --since: the window is the wall itself, starting at its first post
		if wallStart.IsZero() {
			return nil
		}
		from = wallStart
	}
	from = wall.DayStart(from, loc)
	repos := repoNames(evs, from, now)
	if len(repos) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout())
	defer cancel()
	c := wall.Coverage(evs, collectCommits(ctx, repos, from, now, loc), loc, from, now,
		wallStart, wall.DefaultBlindCommits, wall.DefaultBlindPosts)
	if c.Measured == 0 {
		return nil
	}
	// From is the floor, not the flag: with a window reaching past the wall's
	// first post the earlier days are emitted by nobody and must render as
	// gaps. Handing the browser the flag's date instead would file them as
	// days with no commits — a stretch of perfect coverage before the wall
	// existed, drawn out of nothing.
	fromDay := from
	if c.WallStart.After(fromDay) {
		fromDay = wall.DayStart(c.WallStart, loc)
	}
	toDay := wall.DayStart(now, loc).AddDate(0, 0, 1)
	out := &dashCoverage{
		From: fromDay.UnixMilli(), To: toDay.UnixMilli(), Days: map[string]int{},
		Repos:        make([]string, 0, len(c.Repos)),
		BlindCommits: c.BlindCommits, BlindPosts: c.BlindPosts,
		Measured: c.Measured, OnWall: c.OnWall, Others: c.Others,
	}
	for _, r := range c.Repos {
		out.Repos = append(out.Repos, r.Name)
	}
	for _, d := range c.Days {
		if d.PreWall {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", d.Day, loc)
		if err != nil {
			continue
		}
		out.Days[wall.DashDayKey(day)] = d.Commits
	}
	for _, u := range c.Unresolved {
		out.Unresolved = append(out.Unresolved, u.Name)
	}
	return out
}

func cmdDash(args []string) error {
	fs := flag.NewFlagSet("dash", flag.ExitOnError)
	outPath := fs.String("o", "", "output file (default: <wall dir>/dashboard.html)")
	sinceS := fs.String("since", "", "only inline posts newer than 2006-01-02, 36h or 3d (default: everything)")
	openIt := fs.Bool("open", false, "open the dashboard in the browser")
	fs.Parse(args)

	since, err := parseSince(*sinceS, time.Now())
	if err != nil {
		return err
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	// The whole wall, then the window: the commit card needs the wall's own
	// first post as the floor under every blind day, and read through the
	// --since filter that floor would move with the flag.
	all, rstats, err := wall.ReadLast(dir, 0, func(e wall.Event) bool { return e.Kind == "" })
	if err != nil {
		return err
	}
	reportStats(rstats)
	wallStart := firstPost(all)
	evs := all
	if !since.IsZero() {
		evs = all[:0:0]
		for _, e := range all {
			if !e.TS.Before(since) {
				evs = append(evs, e)
			}
		}
	}

	out := make([]dashEvent, 0, len(evs))
	for _, e := range evs {
		vs := 0
		if len(wall.Contradictions(e)) > 0 {
			vs = 1
		}
		out = append(out, dashEvent{
			T: e.TS.UnixMilli(), Repo: e.Repo, Actor: e.Actor, Topic: e.Topic,
			Out: e.Outcome, Mood: e.Mood, Took: e.TookS, Src: e.TookSrc, Vs: vs, Refs: e.Refs, Msg: e.Msg,
			Grader: e.Grader, Signals: e.Signals,
		})
	}
	// json.Marshal HTML-escapes < > & — safe to inline in a <script> block
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	// actor → family, so the page colors and filters by family without
	// carrying the rule that names one: that rule lives in wall.ActorFamily
	families := map[string]string{}
	for _, e := range evs {
		if e.Actor != "" {
			families[e.Actor] = wall.ActorFamily(e.Actor)
		}
	}
	fam, err := json.Marshal(families)
	if err != nil {
		return err
	}
	stamp := time.Now().Format("2006-01-02 15:04")
	if *sinceS != "" {
		// the range buttons cannot reach past what was inlined — say so
		stamp += " · only posts since " + *sinceS + " included"
	}
	// json.Marshal of a nil *dashCoverage is the literal null the card reads
	// as "nobody measured"
	cov, err := json.Marshal(collectDashCoverage(evs, wallStart, since, time.Now()))
	if err != nil {
		return err
	}
	// substitute the stamp BEFORE the data: once user-controlled message text
	// is in the string, a literal "__GENERATED__" inside a post could be hit.
	// The commits and the families go in before the posts for exactly the
	// same reason — they are the last things in the file that are not a post.
	html := strings.Replace(dashTemplate, "__GENERATED__", stamp, 1)
	html = strings.Replace(html, "__WALLII_COMMITS__", string(cov), 1)
	html = strings.Replace(html, "__WALLII_FAMILIES__", string(fam), 1)
	html = strings.Replace(html, "__WALLII_DATA__", string(data), 1)

	path := *outPath
	if path == "" {
		path = filepath.Join(dir, "dashboard.html")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		return err
	}
	fmt.Printf("dashboard: %s (%d posts inlined)\n", path, len(out))
	if *openIt {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, path).Start(); err != nil {
			return fmt.Errorf("could not open browser (%w) — open %s yourself", err, path)
		}
	}
	return nil
}
