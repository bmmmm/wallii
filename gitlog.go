// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// The exec boundary for the coverage reading, next to spawn.go and post.go.
// internal/wall has zero os/exec imports and keeps them: everything below
// turns git into a map of counts, and the fold that judges them never learns
// where they came from.
//
// git runs here for `wallii coverage` and `wallii dash` only — both are
// typed by a person. Never for post, tail, stats, tui, agents, audit or
// archive: the Stop hook calls tail inside a ten-second budget, and stats is
// the one output this measurement must stay out of.

// gitTimeout caps the whole collection. Five seconds is enough for a few
// dozen local repos and short enough that a stale network mount cannot hold
// a dashboard hostage.
func gitTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("WALLII_GIT_TIMEOUT")); v != "" {
		if d, err := parseDur(v); err == nil && d > 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "wallii: WALLII_GIT_TIMEOUT=%q is not a duration — using 5s\n", v)
	}
	return 5 * time.Second
}

// gitEnv strips the three variables that would silently point every child
// git at one repository. A `wallii dash` started from inside a hook inherits
// GIT_DIR and GIT_WORK_TREE, and -C does not save you from them: the same
// repo would be counted once per name on the wall.
func gitEnv() []string {
	env := os.Environ()
	out := env[:0:0]
	for _, kv := range env {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// gitCmd builds a git invocation with the environment cleaned and optional
// locks off — reading a repo must never write its index, least of all one
// somebody is working in right now.
func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := append([]string{"--no-optional-locks", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = gitEnv()
	return cmd
}

// repoCheckout maps a wall repo name to the main checkout it was written
// from. resolveRepoDir (spawn.go) does the probing, unchanged — it joins
// each root with the name and stats once, it never walks a tree — and the
// hardening lives here rather than in the resolver, because the TUI's
// follow-up sessions want a directory and are right not to care whether it
// is a repository.
//
// The check is the same call gitRepoName() makes when it writes a name onto
// the wall, and that is the point: this resolver has to invert that function,
// or it resolves names nobody ever posted under. Linked worktrees and
// subdirectories collapse onto the main checkout for free, and a wall repo
// that happens to share a name with a plain home folder — Documents, work —
// fails the comparison instead of being counted as that folder's enclosing
// repository.
func repoCheckout(ctx context.Context, repo string) (dir, src string, err error) {
	cand, ok := resolveRepoDir(repo)
	if !ok {
		return "", "", fmt.Errorf("no checkout found")
	}
	out, err := gitCmd(ctx, cand, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", "", fmt.Errorf("not a git checkout")
	}
	gitDir := strings.TrimSpace(string(out))
	main := filepath.Dir(gitDir)
	if filepath.Base(gitDir) != ".git" {
		// The writer's second branch. A submodule's common dir is
		// <super>/.git/modules/<name>, whose basename is the repo name —
		// taking it for the checkout would hand Dir a git directory that
		// git log happens to accept. The toplevel is the checkout; bare
		// repos and git < 2.31 land here too, and a bare repo has none.
		top, err := gitCmd(ctx, cand, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return "", "", fmt.Errorf("not a git checkout")
		}
		main = strings.TrimSpace(string(top))
	}
	if filepath.Base(main) != repo {
		// cand sits inside somebody else's repository — resolving it would
		// count that repository's commits under this name
		return "", "", fmt.Errorf("%s belongs to %s", cand, filepath.Base(main))
	}
	return main, "roots", nil
}

// gitEmail reads the identity commits in this repo are made under. The merged
// config is the right answer: a repo without its own user.email still commits
// under the global one.
func gitEmail(ctx context.Context, dir string) string {
	out, err := gitCmd(ctx, dir, "config", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(out)))
}

// commitDays counts one repo's commits per local calendar day.
//
// Only HEAD of the main checkout, never --all: a rebased commit whose old
// branch still exists would be counted twice, and once a repo keeps its
// branches around that is most of them. The price belongs in the output —
// work on a branch that never merged does not appear in these numbers.
//
// --no-merges because a merge is not a unit of work anybody owed a post for.
//
// The window is passed to git as an absolute ISO timestamp, never as the
// duration a person typed. `git log --since=30d` is accepted without a word
// of complaint and returns nothing at all — git does not read "30d", and the
// silence is total: measured here, 0 commits against 77 for the same window
// written as --since=30.days. A window that reports zero looks exactly like
// a repo where nothing happened.
//
// The day is derived from the committer timestamp in loc, not from a date
// git formatted: --since filters on the committer date, so both sides of the
// comparison have to, and an author backdate stays as invisible here as it is
// to the hook that watches the same repos.
func commitDays(ctx context.Context, dir string, from, to time.Time, loc *time.Location, mine string) (byDay, others map[string]int, err error) {
	cmd := gitCmd(ctx, dir, "log", "HEAD", "--no-merges",
		"--since="+from.Format(time.RFC3339),
		"--until="+to.Format(time.RFC3339),
		"--pretty=format:%ct%x09%ae")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("timed out")
		}
		// A repo whose first commit is still to come was measured and had
		// nothing — the one git failure that is an answer, not a gap.
		var ee *exec.ExitError
		if errors.As(err, &ee) && (bytes.Contains(ee.Stderr, []byte("does not have any commits yet")) ||
			bytes.Contains(ee.Stderr, []byte("unknown revision"))) {
			return map[string]int{}, map[string]int{}, nil
		}
		return nil, nil, fmt.Errorf("git log failed")
	}
	byDay, others = map[string]int{}, map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		ts, mail, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		sec, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
		if err != nil {
			continue
		}
		t := time.Unix(sec, 0)
		if t.Before(from) || !t.Before(to) {
			continue
		}
		day := t.In(loc).Format("2006-01-02")
		// An unsplittable repo puts everything in others rather than claiming
		// it: an author column with nobody in it is a finding the reader can
		// see, a silent claim is not.
		if mine == "" || !strings.EqualFold(strings.TrimSpace(mail), mine) {
			others[day]++
			continue
		}
		byDay[day]++
	}
	return byDay, others, nil
}

// collectCommits measures every named repo, at most min(NumCPU, 8) at a
// time — the work is git processes and page cache, and more of them past
// that only queues.
//
// Every repo comes back with something. A repo the deadline never reached
// carries Err "timed out", never a zero: the fold treats an entry it cannot
// trust as unmeasured and names it, and that is the difference between "no
// commits" and "nobody looked".
func collectCommits(ctx context.Context, repos []string, from, to time.Time, loc *time.Location) map[string]wall.RepoCommits {
	out := make(map[string]wall.RepoCommits, len(repos))
	var mu sync.Mutex
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range jobs {
				rc := measureRepo(ctx, repo, from, to, loc)
				mu.Lock()
				out[repo] = rc
				mu.Unlock()
			}
		}()
	}
	for _, repo := range repos {
		select {
		case jobs <- repo:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	for _, repo := range repos {
		if _, ok := out[repo]; !ok {
			out[repo] = wall.RepoCommits{Err: "timed out"}
		}
	}
	return out
}

func measureRepo(ctx context.Context, repo string, from, to time.Time, loc *time.Location) wall.RepoCommits {
	dir, src, err := repoCheckout(ctx, repo)
	if err != nil {
		if ctx.Err() != nil {
			return wall.RepoCommits{Err: "timed out"}
		}
		return wall.RepoCommits{Err: err.Error()}
	}
	mine := gitEmail(ctx, dir)
	byDay, others, err := commitDays(ctx, dir, from, to, loc, mine)
	if err != nil {
		return wall.RepoCommits{Dir: dir, Src: src, Mine: mine, Err: err.Error()}
	}
	return wall.RepoCommits{ByDay: byDay, OthersByDay: others, Mine: mine, Dir: dir, Src: src}
}
