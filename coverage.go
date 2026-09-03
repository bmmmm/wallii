// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// cmdCoverage reports what the wall was posted against: the commits of the
// same window, in the same repos, beside the posts about them.
//
// Two halves, and the output order says which one carries the weight. The
// blind days lead — a day with real work on it and nothing on the wall is
// not fixable by posting thinner, only by posting at all, which makes that
// count structurally resistant to being played. The ratio follows as a
// footnote and gets no percentage: posts per commit rises with every extra
// post whatever it says, so it is a dial, and a dial printed as "37 %
// covered" is an invitation. It describes, it never grades: nothing here is
// a gate, a lint or a challenge, and none of it appears in `wallii stats`.
func cmdCoverage(args []string) error {
	fs := flag.NewFlagSet("coverage", flag.ExitOnError)
	sinceS := fs.String("since", "30d", "window: 2006-01-02, 36h or 3d — begins at local midnight of its first day")
	splitS := fs.String("split", "", "print both halves, before and after this date (2006-01-02)")
	blindCommits := fs.Int("blind-commits", wall.DefaultBlindCommits, "a day counts as worked from this many commits")
	blindPosts := fs.Int("blind-posts", wall.DefaultBlindPosts, "a worked day is blind at or below this many posts")
	repoF := fs.String("repo", "", "filter: repo name")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)

	now := coverageClock()
	loc := time.Local
	since, split, err := coverageWindow(*sinceS, *splitS, now, loc)
	if err != nil {
		return err
	}

	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	// The whole wall, unfiltered — the fold does its own windowing, and the
	// floor under every blind day is the wall's first post ever. Read through
	// a --repo filter that floor would move to the repo's own first post, and
	// the repo nobody ever posted about would come out perfectly covered.
	all, rstats, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		return err
	}
	reportStats(rstats)
	wallStart := firstPost(all)
	evs := all
	if *repoF != "" {
		flt := filter{repo: *repoF}
		evs = evs[:0:0]
		for _, e := range all {
			if flt.match(e) {
				evs = append(evs, e)
			}
		}
	}

	repos := repoNames(evs, since, now)
	if len(repos) == 0 {
		fmt.Println("no posts in this window — nothing to measure against; wall dir:", dir)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout())
	defer cancel()
	commits := collectCommits(ctx, repos, since, now, loc)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		if split.IsZero() {
			return enc.Encode(wall.Coverage(evs, commits, loc, since, now, wallStart, *blindCommits, *blindPosts))
		}
		return enc.Encode([]wall.Cov{
			wall.Coverage(evs, commits, loc, since, split, wallStart, *blindCommits, *blindPosts),
			wall.Coverage(evs, commits, loc, split, now, wallStart, *blindCommits, *blindPosts),
		})
	}

	// The head names the price of the branch decision before any number is
	// read: only HEAD of each main checkout is counted, so a week spent on a
	// branch that never merged reads here as a week of nothing.
	fmt.Printf("coverage · %s … %s · only HEAD of each main checkout counts —\n",
		since.In(loc).Format("2006-01-02"), now.In(loc).Format("2006-01-02"))
	fmt.Println("           work on a branch that never merged is not in these numbers.")
	if split.IsZero() {
		c := wall.Coverage(evs, commits, loc, since, now, wallStart, *blindCommits, *blindPosts)
		printMeasured(c)
		printCov(c, "")
		return nil
	}
	before := wall.Coverage(evs, commits, loc, since, split, wallStart, *blindCommits, *blindPosts)
	after := wall.Coverage(evs, commits, loc, split, now, wallStart, *blindCommits, *blindPosts)
	// the commit map spans the whole window, so both halves carry the same
	// measurement — it is named once, by the first
	printMeasured(before)
	printCov(before, "before "+*splitS)
	fmt.Println()
	printCov(after, "since "+*splitS)
	return nil
}

// coverageClock is the command's clock. A test that hangs a fixture off a
// day boundary pins it, so the edge it measures cannot drift with the wall
// clock between building the fixture and running the command.
var coverageClock = time.Now

// coverageWindow turns the flags into the window git and the fold are asked
// for. The window begins at local midnight of its first day: `--since 30d`
// typed at 23:00 would otherwise hand the oldest day only its last hour —
// commits and posts alike — and then judge it as a whole day. A day is
// judged whole or not at all, and the head line's first date is the date
// the window really starts on.
func coverageWindow(sinceS, splitS string, now time.Time, loc *time.Location) (since, split time.Time, err error) {
	since, err = parseSince(sinceS, now)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if since.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("--since is required for coverage — git is asked for an absolute window, not for everything")
	}
	since = wall.DayStart(since, loc)
	if splitS != "" {
		split, err = time.ParseInLocation("2006-01-02", splitS, loc)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("cannot parse --split %q — use 2006-01-02", splitS)
		}
		if !split.After(since) || !split.Before(now) {
			return time.Time{}, time.Time{}, fmt.Errorf("--split %s lies outside the window %s … %s",
				splitS, since.Format("2006-01-02"), now.Format("2006-01-02"))
		}
	}
	return since, split, nil
}

// firstPost is the wall's own birthday: the oldest regular post it holds.
// Nothing older than this can be a day the wall missed.
func firstPost(evs []wall.Event) time.Time {
	for _, e := range evs {
		if e.Kind == "" {
			return e.TS
		}
	}
	return time.Time{}
}

// repoNames lists the repos the wall knows about in this window — the
// candidate set for the git side. The wall decides which repos are asked
// about; nothing here walks a disk looking for more.
func repoNames(evs []wall.Event, from, to time.Time) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range evs {
		if e.Kind != "" || e.Repo == "" || seen[e.Repo] {
			continue
		}
		if e.TS.Before(from) || !e.TS.Before(to) {
			continue
		}
		seen[e.Repo] = true
		out = append(out, e.Repo)
	}
	sort.Strings(out)
	return out
}

// printMeasured names, once, what the ratio stands over: how many of the
// repos the wall knows were measured, and which were not and why. The commit
// map spans the whole window, so under --split both halves carry the same
// figures — printed per half they read as two findings.
func printMeasured(c wall.Cov) {
	fmt.Printf("measured      %d of %d repos the wall knows\n", c.Measured, c.Measured+len(c.Unresolved))
	if len(c.Unresolved) > 0 {
		// Named, and out of both sides of the ratio. A repo dropped in
		// silence would leave the figure standing over a subset nobody can
		// see — the mistake this whole command exists to stop making.
		parts := make([]string, 0, len(c.Unresolved))
		for _, u := range c.Unresolved {
			parts = append(parts, fmt.Sprintf("%s (%s)", u.Name, u.Why))
		}
		fmt.Printf("not measured  %s\n", strings.Join(parts, " · "))
	}
}

func printCov(c wall.Cov, label string) {
	head := plural(c.OnWall, "repo") + " posted to"
	if label != "" {
		head = label + " · " + head
	}
	fmt.Println()
	fmt.Println(head)
	if c.PostsUnmeasured > 0 {
		// per window: the posts of the unmeasured repos that fell into it
		fmt.Printf("              %s left the ratio with the unmeasured repos\n", plural(c.PostsUnmeasured, "post"))
	}
	if c.PreWallDays > 0 {
		// The window reaches back past the wall's first post. Those days are
		// shown and judged by nothing: "no wall yet" and "nobody posted" are
		// the same silence in the data and the opposite finding.
		fmt.Printf("before the wall  %d day(s) older than the first post (%s) carry %s — not judged, not counted\n",
			c.PreWallDays, c.WallStart.Local().Format("2006-01-02"), plural(c.PreWallCommits, "commit"))
	}

	if c.WorkDays == 0 {
		fmt.Printf("blind days    none — no day reached %d commits, so none could be blind\n", c.BlindCommits)
	} else {
		fmt.Printf("blind days    %d of %d worked days — a day of ≥%d commits and ≤%d posts\n",
			c.BlindDays, c.WorkDays, c.BlindCommits, c.BlindPosts)
	}
	maxDay := 0
	for _, d := range c.Days {
		if d.Commits > maxDay {
			maxDay = d.Commits
		}
	}
	for _, d := range c.Days {
		if !d.Blind {
			continue
		}
		fmt.Printf("  %s  %-20s %s · %s\n", d.Day, bar(d.Commits, maxDay, 20),
			plural(d.Commits, "commit"), plural(d.Posts, "post"))
	}

	// No percentage, on purpose — see the doctrine at the top of the file.
	fmt.Printf("ratio         posts %d · commits %d · %.2f per commit\n", c.Posts, c.Commits, c.PerCommit())
	if c.Others > 0 {
		fmt.Printf("others        %s by other authors — beside the count, never inside it\n",
			plural(c.Others, "commit"))
	}
	var noIdentity []string
	for _, r := range c.Repos {
		if r.Mine == "" {
			noIdentity = append(noIdentity, r.Name)
		}
	}
	if len(noIdentity) > 0 {
		fmt.Printf("              no user.email in %s — every commit there sits in others\n", strings.Join(noIdentity, ", "))
	}

	if len(c.Repos) == 0 {
		return
	}
	fmt.Println()
	top := c.Repos
	maxRepo := top[0].Commits
	for i, r := range top {
		if i == 12 {
			fmt.Printf("… +%d more repos\n", len(top)-12)
			break
		}
		fmt.Printf("%-24s %-20s %3d commits · %3d posts\n", r.Name, bar(r.Commits, maxRepo, 20), r.Commits, r.Posts)
	}
}
