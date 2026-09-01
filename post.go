// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	topic := fs.String("t", "", "kind of work: fix, feature, release, ci, deps, docs, security, infra, ops, chore — or obituary, a eulogy for an approach that died")
	actor := fs.String("a", "", `who posts (default: $WALLII_ACTOR or "manual")`)
	outcome := fs.String("outcome", "", "did it land: ok, partial, failed")
	took := fs.String("took", "", "how long the work took, e.g. 25m, 1h30m; default derives it, none disables")
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
	if err := wall.CheckTopic(*topic, *repo); err != nil {
		return err
	}
	*outcome = strings.ToLower(*outcome)
	*mood = strings.ToLower(*mood)
	var tookS int64
	if *took != "" && *took != "none" {
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
	// Every post carries the conditions it was written under: what the API
	// answered in, right now. Taken before the timestamp, so a slow API does
	// not also make the post late — and never fatal, because a grade is worth
	// more than the telemetry around it.
	pulse, pnote := wall.SessionPulse(context.Background())
	if pnote != "" {
		fmt.Fprintln(os.Stderr, "wallii:", pnote)
	}
	pulseMS, pulseSrc := pulse.Fields()
	now := time.Now()

	// one read serves both post-time lints: the actor's own history is the
	// clock for --took and the evidence for the calibration warning. A
	// failure here must not cost the post — telemetry is the garnish, the
	// message is the point.
	prior, rerr := wall.RecentByActor(dir, who, 0, now)
	if rerr != nil {
		fmt.Fprintln(os.Stderr, "wallii: reading own history (non-fatal):", rerr)
	}
	tookSrc := ""
	if *took == "" {
		if d, ok := autoTook(prior, now); ok {
			tookS, tookSrc = int64(d/time.Second), wall.TookAuto
		}
	}

	e := wall.Event{TS: now.UTC(), Repo: *repo, Actor: who, Topic: *topic, Msg: msg, Refs: refs,
		Outcome: *outcome, TookS: tookS, TookSrc: tookSrc, Mood: *mood,
		PulseMS: pulseMS, PulseSrc: pulseSrc}
	if err := wall.Append(dir, e); err != nil {
		return err
	}
	// Notes, never gates: the post is already on the wall, exactly as
	// written. They are worth printing anyway — the next grade is the one
	// they can still change.
	for _, note := range wall.Contradictions(e) {
		fmt.Fprintln(os.Stderr, "wallii:", note)
	}
	if warn := wall.Calibration(prior, e); warn != "" {
		fmt.Fprintln(os.Stderr, "wallii:", warn)
	}
	if warn := wall.Sameness(prior, e); warn != "" {
		fmt.Fprintln(os.Stderr, "wallii:", warn)
	}
	// opportunistic housekeeping: gzip finished months without a cron job
	if _, err := wall.Archive(dir, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "wallii: archive (non-fatal):", err)
	}
	return nil
}

// Bounds for a derived duration. Below the floor the poster is emptying a
// backlog at session end — the wall saw six posts in one minute — and those
// seconds measure typing, not work. Above the ceiling a night sits in the
// gap. Neither is a work duration, so neither gets recorded: an absent took
// is honest, an invented one is not.
const (
	minAutoTook = time.Minute
	maxAutoTook = 8 * time.Hour
)

// autoTook derives how long this work took from the actor's own timeline:
// the wall is its own clock. The basis is whichever is later — the actor's
// previous post or the session start — because both mark a point where the
// current piece of work demonstrably had not started yet.
func autoTook(prior []wall.Event, now time.Time) (time.Duration, bool) {
	var basis time.Time
	if n := len(prior); n > 0 {
		basis = prior[n-1].TS
	}
	if s, ok := sessionStart(); ok && s.After(basis) {
		basis = s
	}
	if basis.IsZero() {
		return 0, false
	}
	d := now.Sub(basis)
	if d < minAutoTook || d > maxAutoTook {
		return 0, false
	}
	return d, true
}

// sessionStart reads $WALLII_SESSION_START, set by a session-start hook, as
// RFC3339 or unix seconds — a shell hook reaches for `date +%s` first.
func sessionStart() (time.Time, bool) {
	v := strings.TrimSpace(os.Getenv("WALLII_SESSION_START"))
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if sec, err := strconv.ParseInt(v, 10, 64); err == nil && sec > 0 {
		return time.Unix(sec, 0), true
	}
	fmt.Fprintf(os.Stderr, "wallii: WALLII_SESSION_START=%q is neither RFC3339 nor unix seconds — ignored\n", v)
	return time.Time{}, false
}

// resolveActor names who posts. $WALLII_ROLE decorates the ambient identity
// ($WALLII_ACTOR) so launchers can split one configured actor into a
// population — claude/main becomes claude/main/review — without touching
// settings. An explicit -a stays exactly what was typed: whoever names
// themselves has already decided who they are.
func resolveActor(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	base := os.Getenv("WALLII_ACTOR")
	if base == "" {
		base = "manual"
	}
	if role := strings.TrimSpace(os.Getenv("WALLII_ROLE")); role != "" && !strings.HasSuffix(base, "/"+role) {
		base += "/" + role
	}
	return base
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
