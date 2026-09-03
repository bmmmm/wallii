// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"fmt"
	"sort"
	"time"
)

// The wall measures what was posted. It never measured what the posting was
// up against — the work that happened in the same window, in the same repos.
// Coverage is that second half, and it is deliberately two numbers rather
// than one, because they are not equally honest:
//
//   - The blind days are the headline. A day with real work on it and
//     nothing on the wall cannot be lifted by posting thinner: the only way
//     out is to post at all on a day someone worked. That makes the count
//     structurally resistant to being played.
//   - The ratio is the footnote. posts-per-commit rises with every extra
//     post, whatever the post says, so it is a dial and is printed as a
//     description — "posts 209 · commits 762 · 0.27 per commit" — never as a
//     percentage. The same reason graderLine carries none.
//
// Nothing here reads a message, grades anything, or gates a post. The fold
// is pure: every git call lives at the exec boundary in package main, and
// this package keeps its zero os/exec imports.

// Default blind-day thresholds. Both are arbitrary — that is why they are
// flags, and why they travel in the result: a day judged by numbers the
// reader cannot see is a verdict, not a measurement.
const (
	DefaultBlindCommits = 10
	DefaultBlindPosts   = 2
)

// RepoCommits is the measurement of ONE repo. A repo missing from the map
// was never measured; one present with an empty ByDay was measured and had
// nothing. A plain map[string]int per repo could not tell those apart — Go
// would hand out a zero for the repo nobody looked at, and the ratio would
// stand over a subset it never names.
type RepoCommits struct {
	ByDay map[string]int `json:"by_day"` // "2006-01-02" (local) → commits by this repo's own identity
	// OthersByDay carries the commits of other authors — bots included — per
	// day rather than as one total, for the same reason ByDay does: a --split
	// folds the same map twice, and a plain total would report the whole
	// month's foreign commits inside each half.
	OthersByDay map[string]int `json:"others_by_day,omitempty"`
	// Mine is the email ByDay was split against. Empty means the split could
	// not be made at all, and then every commit sits in OthersByDay — an
	// author column with nobody in it is a finding, not a zero.
	Mine string `json:"mine,omitempty"`
	Dir  string `json:"dir,omitempty"`
	Src  string `json:"src,omitempty"` // how the checkout was found — "roots"
	Err  string `json:"err,omitempty"` // why nothing was measured, when nothing was
}

// Measured reports whether this entry is a measurement rather than a reason
// there is none. A timed-out repo carries Err and no counts: it must never
// read as a repo where nothing happened.
func (r RepoCommits) Measured() bool { return r.Err == "" }

// DayRow is one local calendar day: what was committed against what was
// posted. Blind is the verdict of the two thresholds in Cov.
type DayRow struct {
	Day     string `json:"day"` // "2006-01-02", local
	Commits int    `json:"commits"`
	Posts   int    `json:"posts"`
	Blind   bool   `json:"blind,omitempty"`
	// PreWall marks a day older than the wall's first post. Its commits are
	// real and it is emitted so a reader can see it, but it is judged by
	// nothing and counted into nothing: a wall that did not exist yet cannot
	// have missed a day. Measured against the live wall, five such days sat
	// inside a 30-day window and three quarters of the blind days it reported
	// were this artefact.
	PreWall bool `json:"pre_wall,omitempty"`
}

// RepoRow is one measured repo's line.
type RepoRow struct {
	Name    string `json:"repo"`
	Posts   int    `json:"posts"`
	Commits int    `json:"commits"`
	Others  int    `json:"others,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Src     string `json:"src,omitempty"`
	Mine    string `json:"mine,omitempty"`
}

// RepoNote names a repo that was not measured, and why. Naming it is the
// whole point: a repo dropped in silence turns the ratio into a figure over
// an unnamed subset.
type RepoNote struct {
	Name string `json:"repo"`
	Why  string `json:"why"`
}

// Cov is the whole reading.
type Cov struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// WallStart is the wall's own first post, the floor under every judgment
	// below. PreWallDays and PreWallCommits are what fell under it — named,
	// because "the wall was not there yet" and "nobody posted" are the same
	// silence and must not read as the same finding.
	WallStart      time.Time `json:"wall_start"`
	PreWallDays    int       `json:"pre_wall_days,omitempty"`
	PreWallCommits int       `json:"pre_wall_commits,omitempty"`

	BlindCommits int `json:"blind_commits"` // the thresholds this reading used
	BlindPosts   int `json:"blind_posts"`

	Days      []DayRow `json:"days"`
	WorkDays  int      `json:"work_days"`  // days that cleared BlindCommits
	BlindDays int      `json:"blind_days"` // of those, the ones the wall never saw

	Posts   int `json:"posts"`
	Commits int `json:"commits"`
	Others  int `json:"others"`

	OnWall   int `json:"on_wall"`  // repos with at least one post in the window
	Measured int `json:"measured"` // of those (plus any extra), repos whose commits were counted
	// PostsUnmeasured counts the posts that fell out with their repo. The
	// ratio is honest only while this number is visible next to it.
	PostsUnmeasured int `json:"posts_unmeasured"`

	Unresolved []RepoNote `json:"unresolved"`
	Repos      []RepoRow  `json:"repos"`
}

// PerCommit is the footnote number: posts per counted commit. Zero commits
// yield zero — there is no ratio over nothing, and NaN in a report is worse
// than a blank.
func (c Cov) PerCommit() float64 {
	if c.Commits == 0 {
		return 0
	}
	return float64(c.Posts) / float64(c.Commits)
}

// Coverage folds posts against commits over [from, to), one local calendar
// day at a time. loc is a parameter and not time.Local because a day
// boundary is the whole measurement here and t.Setenv("TZ") does not move
// time.Local after process start — a test that could not choose its own
// timezone would only ever prove the machine's.
//
// wallStart is the wall's first post ever, and it is a parameter for the
// same reason: derived from evs it would move with every filter, and
// `--repo x` would then hand the repo nobody ever posted about a floor at
// its own first post — perfect coverage for the worst case. Days below the
// floor are emitted, judged by nothing, and counted into nothing.
//
// Repos that were not measured leave the numerator AND the denominator and
// are named in Unresolved. Their posts are counted apart, in
// PostsUnmeasured, so the reader can see the size of what the ratio omits.
func Coverage(evs []Event, commits map[string]RepoCommits, loc *time.Location, from, to, wallStart time.Time, blindCommits, blindPosts int) Cov {
	if loc == nil {
		loc = time.UTC
	}
	c := Cov{From: from, To: to, WallStart: wallStart, BlindCommits: blindCommits, BlindPosts: blindPosts}
	floor := from
	if wallStart.After(floor) {
		floor = DayStart(wallStart, loc)
	}

	// posts per repo and per day, regular posts only — a reaction is a reply,
	// not a report on a day's work, and counting it would pay for dialogue
	// with coverage
	postsByRepo := map[string]int{}
	postsByDay := map[string]map[string]int{} // repo → day → posts
	for _, e := range evs {
		if e.Kind != "" || e.Repo == "" {
			continue
		}
		if e.TS.Before(floor) || !e.TS.Before(to) {
			continue
		}
		day := e.TS.In(loc).Format("2006-01-02")
		postsByRepo[e.Repo]++
		if postsByDay[e.Repo] == nil {
			postsByDay[e.Repo] = map[string]int{}
		}
		postsByDay[e.Repo][day]++
	}
	c.OnWall = len(postsByRepo)

	// the candidate set is every repo either side knows about: a repo with
	// commits and no post at all is exactly the case a blind day is about
	names := map[string]bool{}
	for r := range postsByRepo {
		names[r] = true
	}
	for r := range commits {
		names[r] = true
	}
	sorted := make([]string, 0, len(names))
	for r := range names {
		sorted = append(sorted, r)
	}
	sort.Strings(sorted)

	commitsByDay := map[string]int{}
	preWallByDay := map[string]int{}
	measuredPostsByDay := map[string]int{}
	for _, name := range sorted {
		rc, ok := commits[name]
		if !ok {
			c.Unresolved = append(c.Unresolved, RepoNote{Name: name, Why: "never measured"})
			c.PostsUnmeasured += postsByRepo[name]
			continue
		}
		if !rc.Measured() {
			c.Unresolved = append(c.Unresolved, RepoNote{Name: name, Why: rc.Err})
			c.PostsUnmeasured += postsByRepo[name]
			continue
		}
		c.Measured++
		row := RepoRow{Name: name, Posts: postsByRepo[name], Dir: rc.Dir, Src: rc.Src, Mine: rc.Mine}
		for day, n := range rc.ByDay {
			switch {
			case inWindow(day, loc, floor, to):
				row.Commits += n
				commitsByDay[day] += n
			case inWindow(day, loc, from, floor):
				c.PreWallCommits += n
				preWallByDay[day] += n
			}
		}
		for day, n := range rc.OthersByDay {
			if inWindow(day, loc, floor, to) {
				row.Others += n
			}
		}
		for day, n := range postsByDay[name] {
			measuredPostsByDay[day] += n
		}
		c.Posts += row.Posts
		c.Commits += row.Commits
		// Others is carried beside the count, never inside it: a bot's commit
		// is not work anybody owed the wall a post for, and folding those in
		// put a quarter of the denominator out of everybody's reach.
		c.Others += row.Others
		c.Repos = append(c.Repos, row)
	}
	sort.Slice(c.Repos, func(i, j int) bool {
		if c.Repos[i].Commits != c.Repos[j].Commits {
			return c.Repos[i].Commits > c.Repos[j].Commits
		}
		return c.Repos[i].Name < c.Repos[j].Name
	})

	// One row per local calendar day, walked as dates rather than added as
	// milliseconds — a DST transition must not merge two days into one.
	for d := DayStart(from, loc); !d.After(DayStart(to.Add(-time.Nanosecond), loc)); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if d.Before(floor) {
			// shown, judged by nothing: there was no wall to miss this day
			c.PreWallDays++
			c.Days = append(c.Days, DayRow{Day: key, Commits: preWallByDay[key], PreWall: true})
			continue
		}
		row := DayRow{Day: key, Commits: commitsByDay[key], Posts: measuredPostsByDay[key]}
		if row.Commits >= blindCommits {
			c.WorkDays++
			if row.Posts <= blindPosts {
				row.Blind = true
				c.BlindDays++
			}
		}
		c.Days = append(c.Days, row)
	}
	return c
}

// DayStart is midnight of t's local calendar day in loc — the one boundary a
// coverage window may start on. Exported so the commands that build a window
// (coverage, dash) share it instead of spelling the date arithmetic out.
func DayStart(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// inWindow keeps a per-repo day inside the fold's own bounds. The collector
// works over the widest window the command asked for; a --split half folds
// the same map twice and must not carry the other half's commits with it.
func inWindow(day string, loc *time.Location, from, to time.Time) bool {
	d, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return false
	}
	return !d.Add(24*time.Hour-time.Nanosecond).Before(from) && d.Before(to)
}

// DashDayKey renders a day the way dash.html's dayKey() builds its bucket
// keys: year-monthIndex-day, month 0-based, no leading zeros. A mismatch is
// not an off-by-one in the card, it is a card with no data at all — every
// day filed under a key the browser never looks up, and the panel would draw
// a month of blindness out of nothing.
func DashDayKey(t time.Time) string {
	y, m, d := t.Date()
	return fmt.Sprintf("%d-%d-%d", y, int(m)-1, d)
}
