// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// flagLike matches tokens like -t / --ref / --ref=x, but not "-5" or "--".
var flagLike = regexp.MustCompile(`^--?[A-Za-z]`)

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func cmdPost(args []string) error {
	fs := flag.NewFlagSet("post", flag.ExitOnError)
	repo := fs.String("r", "", "repo name (default: current git repo)")
	topic := fs.String("t", "", "kind of work: fix, feature, release, ci, deps, docs, security, infra, ops, chore")
	actor := fs.String("a", "", `who posts (default: $WALLII_ACTOR or "manual")`)
	outcome := fs.String("outcome", "", "did it land: ok, partial, failed")
	took := fs.String("took", "", "how long the work took, e.g. 25m, 1h30m")
	mood := fs.String("mood", "", "friction report, not politeness: great (first try) … stuck (blocked/escalated)")
	var refs multiFlag
	fs.Var(&refs, "ref", "commit/issue/PR URL (repeatable)")
	fs.Parse(args)

	// flag.Parse stops at the first positional arg: a flag placed after the
	// message (typo'd or not) would silently become message text instead of
	// data, so any flag-shaped token there is an error
	for _, a := range fs.Args() {
		if flagLike.MatchString(a) {
			return fmt.Errorf("%q comes after the message and would silently join the text — put flags before the message, or quote the message if it really contains flag-like words", a)
		}
	}
	msg := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *repo == "" {
		*repo = gitRepoName()
	}
	// a topic that repeats the repo carries zero information and ruins the
	// topic facet in stats — the feed would show the same word twice
	if *topic != "" && strings.EqualFold(*topic, *repo) {
		return fmt.Errorf("topic %q duplicates the repo — say what kind of work it was: fix, feature, release, ci, deps, docs, security, infra, ops, chore", *topic)
	}
	*outcome = strings.ToLower(*outcome)
	*mood = strings.ToLower(*mood)
	var tookS int64
	if *took != "" {
		d, err := parseDur(*took)
		if err != nil {
			return err
		}
		tookS = int64(d / time.Second)
		if tookS == 0 {
			return fmt.Errorf("--took %s is zero and would be dropped — omit the flag or pass a real duration", *took)
		}
	}
	who := resolveActor(*actor)
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	e := wall.Event{TS: time.Now().UTC(), Repo: *repo, Actor: who, Topic: *topic, Msg: msg, Refs: refs,
		Outcome: *outcome, TookS: tookS, Mood: *mood}
	if err := wall.Append(dir, e); err != nil {
		return err
	}
	// opportunistic housekeeping: gzip finished months without a cron job
	if _, err := wall.Archive(dir, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "wallii: archive (non-fatal):", err)
	}
	return nil
}

func resolveActor(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("WALLII_ACTOR"); v != "" {
		return v
	}
	return "manual"
}

// gitRepoName resolves the repo the current directory belongs to. Linked
// worktrees resolve to the main checkout's name — a session worktree like
// fix-issue-42 must not fragment the repo's history on the wall.
func gitRepoName() string {
	if out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		gitDir := strings.TrimSpace(string(out))
		if filepath.Base(gitDir) == ".git" {
			return filepath.Base(filepath.Dir(gitDir))
		}
	}
	// bare repos, submodules, or git < 2.31: the toplevel name is the best we have
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(string(out)))
}
