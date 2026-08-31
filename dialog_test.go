// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
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
