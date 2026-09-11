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

// dashCalendar is the axis, computed once in Go and handed over whole. The
// browser stopped doing calendar arithmetic when this was introduced: Go
// knows the zone, carries tzdata, and has one tested place where a day
// boundary is made. Two calendars for one file can never agree — the same
// dashboard read "12 commits" under Europe/Berlin and "0 of 0 worked" under
// Pacific/Auckland.
//
// T0 is every day's first instant, ascending and gapless, so a day is an
// index and never a formatted string. A string key is what the old bug was
// made of; making it cheaper would have preserved the class of error. The
// invariant is len(commits) == len(t0), checkable in one line.
type dashCalendar struct {
	TZ  string  `json:"tz"`  // IANA name — for the label and for Intl
	T0  []int64 `json:"t0"`  // unix ms, each day's first instant, ascending
	Wd0 int     `json:"wd0"` // weekday of T0[0], 0 = Monday
	End int64   `json:"end"` // first instant after the last day
}

// dashCoverage is what the c-blind card draws on: commits per day, indexed
// into dashCalendar.T0.
//
// The whole struct is nil — inlined as `null`, never as `[]` or `{}` — when
// nothing was measured. An empty object cannot tell "measured, no commits"
// from "nobody looked", and the card that cannot tell them apart paints a
// month of blindness out of nothing.
//
// FromI is mandatory for the same reason one level down: the dashboard's
// range buttons reach past the window these commits were collected for, and
// every bucket older than FromI has to render as a gap that says "not
// measured", never as a day with no commits on it. ToI closes the same
// contract at the other end — a dashboard opened a week after it was
// written must not paint the days since as days with no commits.
//
// Repos names the repos whose commits are in Days, and the card counts the
// posts of these and no others: a repo without a checkout leaves both sides
// of the ratio here exactly as it does in `wallii coverage`, or the card
// would print a ratio its own footnote contradicts.
type dashCoverage struct {
	// Commits is index-aligned with dashCalendar.T0 and always has the same
	// length; entries outside [FromI, ToI) are zero and mean nothing, which
	// is why the bounds and not the value decide whether a day was measured.
	Commits      []int    `json:"commits"`
	FromI        int      `json:"from_i"` // first measured day, index into T0
	ToI          int      `json:"to_i"`   // one past the last measured day
	Repos        []string `json:"repos"`  // the measured repos — the card's numerator is their posts
	BlindCommits int      `json:"blind_commits"`
	BlindPosts   int      `json:"blind_posts"`
	Measured     int      `json:"measured"`
	OnWall       int      `json:"on_wall"`
	Others       int      `json:"others,omitempty"`
	Unresolved   []string `json:"unresolved,omitempty"`

	// byDay carries the collector's own answer from collectDashCoverage to
	// the point where the calendar exists and it can be indexed. Never
	// serialized: the browser gets indices, never date strings.
	byDay map[string]int
	from  time.Time
	to    time.Time
}

// collectDashCoverage measures the same window the dashboard inlines posts
// for. git runs here and in cmdCoverage only — both are commands a person
// types. Returns nil when no repo could be measured, which is the honest
// answer and the one the card knows how to render.
func collectDashCoverage(evs []wall.Event, wallStart, since, now time.Time, loc *time.Location) *dashCoverage {
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
	toDay := wall.NextDay(now, loc)
	out := &dashCoverage{
		from: fromDay, to: toDay, byDay: map[string]int{},
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
		out.byDay[d.Day] = d.Commits
	}
	for _, u := range c.Unresolved {
		out.Unresolved = append(out.Unresolved, u.Name)
	}
	return out
}

// buildDashCalendar walks the axis the page is drawn on: every day from the
// earliest thing the file knows about up to and including the day it was
// written. It subsumes the browser's old allStart() — "all" has to reach
// back to the collected commit window and not only to the first post, or the
// days before that post are never walked and their commits leave without a
// trace.
//
// It ends at the generation day, full stop. A static file cannot contain a
// post from after it was written, so a day beyond it has no posts, no
// commits and no measurement — walking it would draw an empty gap column
// carrying no information, and it is the only thing the page would need a
// clock for.
//
// The calendar always exists, even for an empty wall — then with exactly one
// day, because the page divides by the number of buckets.
func buildDashCalendar(evs []wall.Event, cov *dashCoverage, now time.Time, loc *time.Location) dashCalendar {
	start := wall.DayStart(now, loc)
	for _, e := range evs { // the events are not guaranteed sorted here
		if d := wall.DayStart(e.TS, loc); d.Before(start) {
			start = d
		}
	}
	if cov != nil && cov.from.Before(start) {
		start = wall.DayStart(cov.from, loc)
	}
	end := wall.NextDay(now, loc)
	cal := dashCalendar{
		TZ:  loc.String(),
		End: end.UnixMilli(),
		Wd0: (int(start.Weekday()) + 6) % 7, // Go counts from Sunday, the page from Monday
	}
	for d := start; d.Before(end); {
		cal.T0 = append(cal.T0, d.UnixMilli())
		next := wall.NextDay(d, loc)
		if !next.After(d) {
			// A day boundary that does not advance grows this slice until
			// the process dies. It cannot happen through NextDay — that is
			// what NextDay is for — so this is here to make the failure of
			// a future step function finite and loud instead of an OOM.
			break
		}
		d = next
	}
	return cal
}

// indexedCoverage lays the collector's per-day counts onto a calendar and
// returns a NEW value. Commits ends up the same length as T0 — the one
// invariant that replaces a day-key string format pinned across two
// languages.
//
// It copies rather than fills in place because --serve reuses one
// measurement across rebuilds: the measurement is byDay/from/to, the
// projection onto today's axis is Commits/FromI/ToI, and writing the
// projection back into the shared value would change a page already served.
func indexedCoverage(src *dashCoverage, cal dashCalendar, loc *time.Location) *dashCoverage {
	if src == nil {
		return nil
	}
	cov := *src
	cov.Commits = make([]int, len(cal.T0))
	cov.FromI, cov.ToI = len(cal.T0), len(cal.T0)
	for i, ms := range cal.T0 {
		day := time.UnixMilli(ms).In(loc)
		if day.Before(cov.from) || !day.Before(cov.to) {
			continue
		}
		if i < cov.FromI {
			cov.FromI = i
		}
		cov.ToI = i + 1
		cov.Commits[i] = cov.byDay[day.Format("2006-01-02")]
	}
	return &cov
}

func cmdDash(args []string) error {
	fs := flag.NewFlagSet("dash", flag.ExitOnError)
	outPath := fs.String("o", "", "output file (default: <wall dir>/dashboard.html)")
	sinceS := fs.String("since", "", "only inline posts from the day of 2006-01-02, 36h or 3d onwards — rounded down to midnight in the report zone, like coverage (default: everything)")
	openIt := fs.Bool("open", false, "open the dashboard in the browser")
	serve := fs.Bool("serve", false, "serve on 127.0.0.1 and reload the page as the wall grows — writes nothing")
	port := fs.Int("port", 8484, "port for --serve (loopback only)")
	commitsS := fs.String("commits", "5m", "with --serve: how often git may be asked again (0 = every rebuild, off = never)")
	fs.Parse(args)

	loc, err := reportZone()
	if err != nil {
		return err
	}
	if *serve && *outPath != "" {
		// The served copy carries the reload snippet. Letting it become the
		// file somebody publishes would put a page that polls /live on a
		// static host, where it reloads forever against a 404.
		return fmt.Errorf("-o and --serve are mutually exclusive — the served page is never written to disk")
	}
	if err := serveOptsCheck(*serve, *commitsS); err != nil {
		return err
	}
	// Read once here so a bad --since fails before anything is built or
	// bound; the render resolves it again per rebuild, because a relative
	// window must not freeze at the moment the server started.
	if _, err := parseSince(*sinceS, time.Now(), loc); err != nil {
		return err
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	inlined := 0 // how many posts the written file carries, for the closing line

	// One render, reusable: --serve calls it again for every rebuild, and a
	// page built two different ways would be two pages. live carries the
	// reload snippet; version is the build stamp it polls for.
	//
	// `since` is re-resolved inside, not captured: a server left running
	// overnight must not keep cutting "3d" at the day it was started.
	var cachedCov *dashCoverage
	var cachedAt time.Time
	render := func(live bool, version int64) ([]byte, error) {
		now := time.Now()
		since, err := parseSince(*sinceS, now, loc)
		if err != nil {
			return nil, err
		}
		// Round the window down to midnight in the report zone, the way
		// coverageWindow does, BEFORE anything is cut with it. git is asked
		// for whole days — that is what a day bucket is — so a raw timestamp
		// here cuts the posts at noon and the commits at midnight, and the
		// first day of the window carries a full day of commits against half
		// a day of posts. `--since 3d` read a day as blind that `wallii
		// coverage --since 3d` read as covered, off the same wall and the
		// same window.
		if !since.IsZero() {
			since = wall.DayStart(since, loc)
		}
		// The whole wall, then the window: the commit card needs the wall's
		// own first post as the floor under every blind day, and read through
		// the --since filter that floor would move with the flag.
		all, rstats, err := wall.ReadLast(dir, 0, func(e wall.Event) bool { return e.Kind == "" })
		if err != nil {
			return nil, err
		}
		if !live {
			reportStats(rstats)
		}
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
			return nil, err
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
			return nil, err
		}

		// The git half forks per repo — 0.42s over 40 repos here, up to
		// gitTimeout() at worst — so under --serve it keeps its own budget
		// and the measurement is reused between runs. The reused value is
		// never written to: indexedCoverage returns a new one.
		if cachedCov == nil || !live || dashCommitsFresh.Load() {
			cachedCov = collectDashCoverage(evs, wallStart, since, now, loc)
			cachedAt = now
		}
		cal := buildDashCalendar(evs, cachedCov, now, loc)
		covv := indexedCoverage(cachedCov, cal, loc)
		calJSON, err := json.Marshal(cal)
		if err != nil {
			return nil, err
		}
		// json.Marshal of a nil *dashCoverage is the literal null the card
		// reads as "nobody measured"
		cov, err := json.Marshal(covv)
		if err != nil {
			return nil, err
		}

		stamp := now.In(loc).Format("2006-01-02 15:04") + " · " + loc.String()
		if *sinceS != "" {
			// The range buttons cannot reach past what was inlined — say so,
			// and name the day the window actually starts on rather than the
			// flag: `--since 36h` reaches back to the midnight before, and a
			// reader counting posts against the flag would come up short.
			stamp += " · only posts since " + since.In(loc).Format("2006-01-02") + " included"
		}
		if live {
			// A served page from 14:22 must not imply a commit measurement
			// from 14:22 when the last one ran at 14:05.
			stamp += " · live, commits measured " + cachedAt.In(loc).Format("15:04")
		}

		live_ := ""
		if live {
			live_ = dashLiveSnippet(version, dashLivePollMS)
		}
		// One pass over the template, never five in a row. Sequential Replace
		// calls let a value that came off the wall stand in for a placeholder
		// that has not been substituted yet: the commits JSON carries repo
		// names (Repos, Unresolved), it goes in before the families and sits
		// ahead of them in the file, so a repo named "__WALLII_FAMILIES__"
		// captured that placeholder — `const FAMILIES = __WALLII_FAMILIES__;`
		// stayed in the output, the whole script block died of a SyntaxError,
		// and one post was enough to leave every later dashboard permanently
		// blank. NewReplacer walks the template once and copies replacement
		// text out verbatim, so nothing it inserts can be read as a
		// placeholder.
		html := strings.NewReplacer(
			"__GENERATED__", stamp,
			"__WALLII_COMMITS__", string(cov),
			"__WALLII_CAL__", string(calJSON),
			"__WALLII_FAMILIES__", string(fam),
			"__WALLII_DATA__", string(data),
			"__WALLII_LIVE__", live_,
		).Replace(dashTemplate)
		if live {
			return []byte(html), nil
		}
		inlined = len(out)
		return []byte(html), nil
	}

	if *serve {
		ctx, stop := dashSignalContext()
		defer stop()
		every, off := parseCommitBudget(*commitsS)
		return serveDash(ctx, dir, dashServeOpts{
			port: *port, open: *openIt, commitEvery: every, commitsOff: off,
		}, render)
	}

	html, err := render(false, 0)
	if err != nil {
		return err
	}
	path := *outPath
	if path == "" {
		path = filepath.Join(dir, "dashboard.html")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, html, 0o600); err != nil {
		return err
	}
	fmt.Printf("dashboard: %s (%d posts inlined)\n", path, inlined)
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

// parseCommitBudget reads --commits: a duration, 0 for "every rebuild", or
// "off" for never. Validated up front by serveOptsCheck, so anything that
// reaches here has already been accepted.
func parseCommitBudget(s string) (every time.Duration, off bool) {
	s = strings.TrimSpace(s)
	if s == "off" {
		return 0, true
	}
	if s == "0" || s == "" {
		return 0, false
	}
	d, _ := parseDur(s)
	return d, false
}

// serveOptsCheck rejects a --commits value that cannot be read, rather than
// quietly measuring on a schedule nobody asked for.
func serveOptsCheck(serve bool, commitsS string) error {
	if !serve {
		return nil
	}
	s := strings.TrimSpace(commitsS)
	if s == "off" || s == "0" || s == "" {
		return nil
	}
	if d, err := parseDur(s); err != nil || d <= 0 {
		return fmt.Errorf("cannot read --commits %q — use a duration like 5m, 0 for every rebuild, or off", commitsS)
	}
	return nil
}
