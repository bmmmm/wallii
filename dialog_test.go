// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
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
