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
	// ID is what a challenge or a haunted pair points at.
	ID string `json:"id"`
	// Usd is the unit cost — the delta to the session's previous post, read
	// by wall.UnitCosts over the whole wall. Absent means nobody measured,
	// never free: a pointer, so a missing reading is left out and a unit
	// under the rounding still arrives as the 0 it was.
	Usd  *float64 `json:"usd,omitempty"`
	Sess string   `json:"sess,omitempty"`
	// Sq7 and Sq5 are the plan limits spent when the post was written, in
	// percent — recorded beside the grades, never in them.
	Sq7 float64 `json:"sq7,omitempty"`
	Sq5 float64 `json:"sq5,omitempty"`
}

// dashDoubt is what the wall says against itself, inlined whole so the page
// can list it: challenges nobody answered yet, and oks a fix came back for.
// Texts to read, not a score — the tile counts them, nothing ranks by them.
type dashDoubt struct {
	Challenges []dashChallenge `json:"challenges"`
	Haunted    []dashHaunt     `json:"haunted"`
}

type dashChallenge struct {
	T      int64  `json:"t"`
	Actor  string `json:"actor"`
	Msg    string `json:"msg"`
	Target string `json:"target,omitempty"` // the challenged post's id
	Repo   string `json:"repo,omitempty"`   // the challenged post's repo
	Who    string `json:"who,omitempty"`    // the challenged post's actor
	TMsg   string `json:"tmsg,omitempty"`   // the challenged post's message
}

// dashHaunt pairs by id: both sides are in RAW whenever the ok is, because
// the fix comes after it. Only the ok's id and the fix's are carried — the
// page looks the posts up rather than inlining their text twice.
type dashHaunt struct {
	OK     string   `json:"ok"`
	Fix    string   `json:"fix"`
	Shared []string `json:"shared"`
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
	Commits []int    `json:"commits"`
	FromI   int      `json:"from_i"` // first measured day, index into T0
	ToI     int      `json:"to_i"`   // one past the last measured day
	Repos   []string `json:"repos"`  // the measured repos — the card's numerator is their posts
	// RepoCommits is index-aligned with Repos: each repo's counted commits
	// over the whole collected window. Per repo, never per repo and day — a
	// second axis would be a second calendar, and the page reads it only
	// as the window's total beside that repo's posts.
	RepoCommits  []int    `json:"repo_commits"`
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
		RepoCommits:  make([]int, 0, len(c.Repos)),
		BlindCommits: c.BlindCommits, BlindPosts: c.BlindPosts,
		Measured: c.Measured, OnWall: c.OnWall, Others: c.Others,
	}
	for _, r := range c.Repos {
		out.Repos = append(out.Repos, r.Name)
		out.RepoCommits = append(out.RepoCommits, r.Commits)
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

// dashDoubtOf gathers the doubt the page lists. Open challenges are taken
// over the whole wall and carried whatever --since says: a challenge from
// before the window is still waiting, and it names its target's words so
// the page need not have that post inlined. Hauntings are paired over the
// whole wall too, the way `wallii mirror` pairs them, and kept when the ok
// lies inside the window — then both sides are in RAW, since a fix always
// comes after its ok.
func dashDoubtOf(wallAll, posts []wall.Event, units map[string]wall.Unit, since time.Time) dashDoubt {
	d := dashDoubt{Challenges: []dashChallenge{}, Haunted: []dashHaunt{}}
	for _, c := range wall.OpenChallenges(wallAll) {
		dc := dashChallenge{T: c.Challenge.TS.UnixMilli(), Actor: c.Challenge.Actor, Msg: c.Challenge.Msg}
		if c.HasTarget {
			dc.Target, dc.Repo, dc.Who, dc.TMsg = c.Target.ID(), c.Target.Repo, c.Target.Actor, c.Target.Msg
		}
		d.Challenges = append(d.Challenges, dc)
	}
	for _, h := range wall.HauntingsWith(posts, units) {
		if h.OK.TS.Before(since) {
			continue
		}
		d.Haunted = append(d.Haunted, dashHaunt{OK: h.OK.ID(), Fix: h.Fix.ID(), Shared: h.Shared})
	}
	return d
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
	if *serve && (*port < 1 || *port > 65535) {
		// Port 0 binds — the kernel picks one — and the whole point of
		// refusing a fallback port is that nobody has to go looking for the
		// one being served.
		return fmt.Errorf("--port %d is not a port to serve on — pick one, 8484 by default", *port)
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
	lastReadStats := ""
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
		//
		// Every kind is read — the dialogue is where the open challenges are
		// — and the posts are cut out of it right after. The feed, the charts
		// and every count below still see posts only.
		wallAll, rstats, err := wall.ReadLast(dir, 0, func(e wall.Event) bool {
			return e.Kind == "" || e.Kind == wall.KindReact || e.Kind == wall.KindChallenge
		})
		if err != nil {
			return nil, err
		}
		all := make([]wall.Event, 0, len(wallAll))
		for _, e := range wallAll {
			if e.Kind == "" {
				all = append(all, e)
			}
		}
		// Unit costs over the whole wall, before any window is cut: a unit
		// is the delta to the session's previous post, and a session does
		// not end where --since does. Read over the window instead, the
		// first post inside it would count from session start and carry
		// every unit before the edge.
		units := wall.UnitCosts(all)
		// Said on every render, served or written — a month file that cannot
		// be read takes its posts off the page, and silence about that is
		// the one thing this whole file is built not to do. Under --serve it
		// is said once per distinct state rather than once per rebuild, so a
		// standing problem is a line, not a stream.
		if !live {
			reportStats(rstats)
		} else if s := statsLine(rstats); s != lastReadStats {
			lastReadStats = s
			if s != "" {
				reportStats(rstats)
			}
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
			de := dashEvent{
				T: e.TS.UnixMilli(), Repo: e.Repo, Actor: e.Actor, Topic: e.Topic,
				Out: e.Outcome, Mood: e.Mood, Took: e.TookS, Src: e.TookSrc, Vs: vs, Refs: e.Refs, Msg: e.Msg,
				Grader: e.Grader, Signals: e.Signals,
				ID: e.ID(), Sess: e.Sess, Sq7: e.SqueezeP, Sq5: e.Squeeze5h,
			}
			if u, ok := units[e.ID()]; ok {
				usd := u.CostUSD
				de.Usd = &usd
			}
			out = append(out, de)
		}
		doubt := dashDoubtOf(wallAll, all, units, since)
		doubtJSON, err := json.Marshal(doubt)
		if err != nil {
			return nil, err
		}
		// json.Marshal HTML-escapes < > & — safe to inline in a <script> block
		data, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		// actor → family, so the page colors and filters by family without
		// carrying the rule that names one: that rule lives in wall.ActorFamily
		//
		// Over the whole wall, dialogue included, not the window: a challenge
		// is listed whatever --since says, and the chip files it by the
		// family of the actor it waits on — one who has not posted since the
		// cut must still resolve to its family, not become a family of its own.
		families := map[string]string{}
		for _, e := range wallAll {
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
			// A served page from 14:22 must not imply a measurement from
			// 14:22 when the last one ran at 14:05. The date is part of it:
			// a server running past midnight would otherwise say "23:00",
			// which reads as tonight.
			//
			// And it says "measured", not "commits measured", because the
			// whole reading is frozen together — the blind-day card's posts
			// and repo set come from that same run, while the feed above it
			// is current.
			when := cachedAt.In(loc).Format("15:04")
			if !sameDay(cachedAt.In(loc), now.In(loc)) {
				when = cachedAt.In(loc).Format("2006-01-02 15:04")
			}
			stamp += " · live, this page reloads itself · commits and the blind-day card measured " + when
		} else {
			stamp += " · snapshot, refresh with `wallii dash`"
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
			"__WALLII_DOUBT__", string(doubtJSON),
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
