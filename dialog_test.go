// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// Roundtrip through the real commands: post → react → challenge → open list.
// All data invented.
func TestDialogRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")

	if err := cmdPost([]string{"-r", "demo", "-t", "fix", "-a", "bot/builder", "--outcome", "ok", "flux capacitor aligned, gates green"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	evs, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil || len(evs) != 1 {
		t.Fatalf("want 1 event, got %d (%v)", len(evs), err)
	}
	id := evs[0].ID()

	if err := cmdReact([]string{"-a", "bot/fan", id, "which gate — the one that can go red?"}); err != nil {
		t.Fatalf("react: %v", err)
	}
	if err := cmdChallenge([]string{"-a", "bot/critic", id[:5], "CI shows no run for this commit"}); err != nil {
		t.Fatalf("challenge by prefix: %v", err)
	}
	// red: reacting to a nonexistent ID must fail loudly
	if err := cmdReact([]string{"-a", "bot/fan", "ffffff0", "into the void"}); err == nil {
		t.Fatal("react to unknown ID must error")
	}

	evs, _, err = wall.ReadLast(dir, 0, nil)
	if err != nil || len(evs) != 3 {
		t.Fatalf("want 3 events, got %d (%v)", len(evs), err)
	}
	open := wall.OpenChallenges(evs)
	if len(open) != 1 || open[0].Target.Actor != "bot/builder" {
		t.Fatalf("want 1 open challenge against bot/builder, got %+v", open)
	}

	// the builder answers the challenge → closed
	if err := cmdReact([]string{"-a", "bot/builder", open[0].Challenge.ID(), "gate 3, red-proven yesterday"}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	evs, _, _ = wall.ReadLast(dir, 0, nil)
	if open := wall.OpenChallenges(evs); len(open) != 0 {
		t.Fatalf("challenge must be closed after the answer, still open: %+v", open)
	}

	// dialogue must not shift the actor's took clock: the reply above is
	// invisible to RecentByActor
	prior, err := wall.RecentByActor(dir, "bot/builder", 0, time.Now())
	if err != nil || len(prior) != 1 {
		t.Fatalf("RecentByActor must see only the post, got %d (%v)", len(prior), err)
	}
}

func captureStdout(t *testing.T, run func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := run()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("command failed: %v", runErr)
	}
	return string(out)
}

func captureStderr(t *testing.T, run func() error) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	runErr := run()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("command failed: %v", runErr)
	}
	return string(out)
}

// The lint's doubt, end to end through the real commands: a post that
// contradicts its grade draws a challenge from wallii/lint, the note on
// stderr carries the handle, the challenge lists as open against the poster,
// a second post of the same class is suppressed and says so, the registry
// never lists the lint, and the poster's react closes it. All data invented.
func TestLintChallengeRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	t.Setenv("WALLII_AUTO_CHALLENGE", "")

	msg := "cache layer landed, invalidation not yet wired"
	note := captureStderr(t, func() error {
		return cmdPost([]string{"-r", "webshop", "-t", "feature", "-a", "bot/builder", "--outcome", "ok", msg})
	})
	evs, _, _ := wall.ReadLast(dir, 0, nil)
	if len(evs) != 2 || evs[0].Msg != msg || evs[1].Kind != wall.KindChallenge || evs[1].Actor != wall.LintActor {
		t.Fatalf("want the post and one lint challenge, got %+v", evs)
	}
	id := evs[1].ID()
	if !strings.Contains(note, "raised as challenge "+id) || !strings.Contains(note, "wallii react "+id) {
		t.Fatalf("stderr must hand over the challenge, got:\n%s", note)
	}
	if !strings.Contains(note, `the message says "not yet"`) {
		t.Fatalf("the note itself must still be there, got:\n%s", note)
	}

	out := captureStdout(t, func() error { return cmdChallenge([]string{"--open", "--actor", "bot/builder"}) })
	if !strings.Contains(out, wall.LintActor) || !strings.Contains(out, "1 open") {
		t.Fatalf("the lint's challenge must list as open against the poster, got:\n%s", out)
	}

	// same class again: suppressed, and the note says under which handle
	note = captureStderr(t, func() error {
		return cmdPost([]string{"-r", "docs-site", "-t", "docs", "-a", "bot/builder", "--outcome", "ok", "queue drained except for the poison messages"})
	})
	if !strings.Contains(note, "already open as "+id) {
		t.Fatalf("a suppressed challenge must be named, got:\n%s", note)
	}
	evs, _, _ = wall.ReadLast(dir, 0, nil)
	if len(evs) != 3 {
		t.Fatalf("the second post must not add a second challenge, got %d events", len(evs))
	}
	if pairs := wall.Attachments(evs); len(pairs) != 2 || pairs[0].Actor != "bot/builder" || pairs[1].Actor != "bot/builder" {
		t.Fatalf("the lint must never appear in the registry, got %+v", pairs)
	}
	if s := wall.Compute(evs); s.Challenges != 1 || s.ChallengesAuto != 1 || len(s.ByChallenged) != 0 {
		t.Fatalf("stats must count the lint apart: %+v", s)
	}

	// the poster answers: closed, and the next doubt raises afresh
	if err := cmdReact([]string{"-a", "bot/builder", id, "fair — partial"}); err != nil {
		t.Fatal(err)
	}
	if evs, _, _ = wall.ReadLast(dir, 0, nil); len(wall.OpenChallenges(evs)) != 0 {
		t.Fatal("the poster's react must close the lint's challenge")
	}
	note = captureStderr(t, func() error {
		return cmdPost([]string{"-r", "webshop", "-t", "feature", "-a", "bot/builder", "--outcome", "ok", "import path migration done, the legacy shim is parked for now"})
	})
	if !strings.Contains(note, "raised as challenge") {
		t.Fatalf("after an answer the class must raise again, got:\n%s", note)
	}

	// the human's switch: note without handle, nothing on the wall
	t.Setenv("WALLII_AUTO_CHALLENGE", "off")
	before := len(readWall(t, dir))
	note = captureStderr(t, func() error {
		return cmdPost([]string{"-r", "webshop", "-t", "fix", "-a", "bot/reviewer", "--outcome", "ok", "deps bumped, two suites still failing"})
	})
	if !strings.Contains(note, "still failing") || strings.Contains(note, "challenge") {
		t.Fatalf("off must keep the note and drop the challenge, got:\n%s", note)
	}
	if got := len(readWall(t, dir)); got != before+1 {
		t.Fatalf("off must write the post and nothing else, got %d new events", got-before)
	}
}

// The post must never die: a failing challenge costs the challenge, not the
// post. This is what the seam in post.go exists for.
func TestPostSurvivesAFailingLintChallenge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	t.Setenv("WALLII_AUTO_CHALLENGE", "")
	orig := raiseLintChallenge
	raiseLintChallenge = func(string, wall.Event, time.Time) (wall.LintChallenge, error) {
		return wall.LintChallenge{}, errors.New("the wall dir vanished under the challenge")
	}
	t.Cleanup(func() { raiseLintChallenge = orig })

	var err error
	note := captureStderr(t, func() error {
		err = cmdPost([]string{"-r", "webshop", "-t", "fix", "-a", "bot/builder", "--outcome", "ok", "cache layer landed, invalidation not yet wired"})
		return nil
	})
	if err != nil {
		t.Fatalf("a failing challenge cost the post: %v", err)
	}
	evs := readWall(t, dir)
	if len(evs) != 1 || evs[0].Kind != "" {
		t.Fatalf("want exactly the post on the wall, got %+v", evs)
	}
	if !strings.Contains(note, "non-fatal") || !strings.Contains(note, `the message says "not yet"`) {
		t.Fatalf("the failure and the note must both be said, got:\n%s", note)
	}
}

// A window in which only the lint spoke is not a window in which the wall
// talked back: the counter is printed, and the nudge stays.
func TestDialogLineSeparatesLintChallenges(t *testing.T) {
	lintOnly := dialogLines(wall.Stats{Challenges: 2, ChallengesAuto: 2, ChallengesOpen: 2})
	if len(lintOnly) != 2 || !strings.Contains(lintOnly[0], "2 raised by the lint, 0 by an agent") || !strings.Contains(lintOnly[1], "nobody answered anyone") {
		t.Fatalf("lint-only window must print the counter and the nudge, got %q", lintOnly)
	}
	mixed := dialogLines(wall.Stats{Reactions: 4, Challenges: 2, ChallengesAuto: 2, ChallengesOpen: 2})
	if len(mixed) != 1 || mixed[0] != "dialog   4 reaction(s) · 2 challenge(s) (2 open · 2 raised by the lint, 0 by an agent)" {
		t.Fatalf("mixed window renders %q", mixed)
	}
	agents := dialogLines(wall.Stats{Challenges: 1, ChallengesOpen: 1, ByChallenged: []wall.NameCount{{Name: "bot/builder", Count: 1}}})
	if len(agents) != 1 || agents[0] != "dialog   0 reaction(s) · 1 challenge(s) (1 open) — most challenged: bot/builder (1)" {
		t.Fatalf("an agent's challenge must render as before, got %q", agents)
	}
	if quiet := dialogLines(wall.Stats{}); len(quiet) != 1 || !strings.Contains(quiet[0], "nobody answered anyone") {
		t.Fatalf("a silent wall must get the nudge, got %q", quiet)
	}
}

// Prime slots: the view folds, the store never does.
func TestPrimeSlotsFoldTheView(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	msgs := []string{
		"first unit: parser rebuilt around the seam",
		"second unit: queue drained, workers idle",
		"third unit: docs match reality again",
		"fourth unit: cache measured, then removed",
		"fifth unit: release cut and verified",
	}
	for _, m := range msgs {
		if err := cmdPost([]string{"-r", "demo", "-t", "chore", "-a", "bot/busy", m}); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	out := captureStdout(t, func() error { return cmdTail([]string{"-n", "0"}) })
	if got := strings.Count(out, "unit:"); got != primeSlots {
		t.Fatalf("want %d rendered posts, got %d in:\n%s", primeSlots, got, out)
	}
	if !strings.Contains(out, "+2 more from bot/busy") {
		t.Fatalf("fold line missing in:\n%s", out)
	}
	out = captureStdout(t, func() error { return cmdTail([]string{"-n", "0", "--all"}) })
	if got := strings.Count(out, "unit:"); got != len(msgs) {
		t.Fatalf("--all must render everything, got %d in:\n%s", got, out)
	}
	// a filtered view is a question already asked — no folding
	out = captureStdout(t, func() error { return cmdTail([]string{"-n", "0", "--actor", "bot/busy"}) })
	if got := strings.Count(out, "unit:"); got != len(msgs) {
		t.Fatalf("filtered view must render everything, got %d in:\n%s", got, out)
	}
}

func TestResolveActorRole(t *testing.T) {
	t.Setenv("WALLII_ACTOR", "bot/base")
	t.Setenv("WALLII_ROLE", "review")
	if got := resolveActor(""); got != "bot/base/review" {
		t.Fatalf("role must decorate the ambient actor, got %q", got)
	}
	// an explicit -a stays exactly what was typed
	if got := resolveActor("bot/named"); got != "bot/named" {
		t.Fatalf("explicit actor must not grow a role, got %q", got)
	}
	// no doubling when the identity already carries the role
	t.Setenv("WALLII_ACTOR", "bot/base/review")
	if got := resolveActor(""); got != "bot/base/review" {
		t.Fatalf("role must not double, got %q", got)
	}
}

func TestAttachPersona(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)

	if err := cmdAttach([]string{"-r", "demo", "-a", "bot/grump", "--persona", "the grumbler", "here to doubt"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	evs, _, _ := wall.ReadLast(dir, 0, nil)
	pairs := wall.Attachments(evs)
	if len(pairs) != 1 || pairs[0].Persona != "the grumbler" {
		t.Fatalf("persona must survive into the registry, got %+v", pairs)
	}
	// a new persona on an attached pair is a state change, not a no-op
	if err := cmdAttach([]string{"-r", "demo", "-a", "bot/grump", "--persona", "the mellowed grumbler"}); err != nil {
		t.Fatalf("re-attach with new persona: %v", err)
	}
	evs, _, _ = wall.ReadLast(dir, 0, nil)
	if pairs = wall.Attachments(evs); pairs[0].Persona != "the mellowed grumbler" {
		t.Fatalf("latest persona must win, got %+v", pairs)
	}
	// red: persona on a plain post must be rejected at the store
	bad := wall.Event{TS: time.Now().UTC(), Repo: "demo", Actor: "bot/grump", Msg: "work", Persona: "sneaky"}
	if err := bad.Validate(); err == nil {
		t.Fatal("persona outside attach must be rejected")
	}
}
