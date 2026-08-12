// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

func TestFmtTook(t *testing.T) {
	cases := map[int64]string{
		59:   "1m", // rounds, matching the dashboard's math
		100:  "2m",
		1500: "25m",
		3599: "1h00m", // never "60m"
		7199: "2h00m", // never "1h60m"
	}
	for sec, want := range cases {
		if got := fmtTook(sec); got != want {
			t.Errorf("fmtTook(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestMoodWord(t *testing.T) {
	cases := map[float64]string{5: "great", 4.5: "great", 3.6: "good", 2.5: "ok", 1.6: "rough", 1: "stuck"}
	for avg, want := range cases {
		if got := moodWord(avg); got != want {
			t.Errorf("moodWord(%.1f) = %q, want %q", avg, got, want)
		}
	}
}

func TestPostGuardRejectsFlagAfterMessage(t *testing.T) {
	t.Setenv("WALLII_DIR", t.TempDir())
	bad := [][]string{
		{"-r", "x", "fixed", "stuff", "--ref", "https://x.example/1"},
		{"-r", "x", "fixed", "--ref=https://x.example/1"},
		{"-r", "x", "done", "--outcom", "ok"}, // typo'd flag must not be swallowed either
	}
	for _, args := range bad {
		if err := cmdPost(args); err == nil {
			t.Errorf("args %v accepted — flag after message must error", args)
		}
	}
	good := [][]string{
		{"-r", "x", "quoted message mentioning --ref inline"}, // one quoted arg
		{"-r", "x", "temperature", "-5", "degrees"},           // negative number is not a flag
	}
	for _, args := range good {
		if err := cmdPost(args); err != nil {
			t.Errorf("args %v rejected: %v", args, err)
		}
	}
}

func TestPostNormalizesOutcomeCase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	if err := cmdPost([]string{"-r", "x", "--outcome", "OK", "--mood", "Good", "cased flags"}); err != nil {
		t.Fatalf("post with cased enums failed: %v", err)
	}
	evs, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil || len(evs) != 1 {
		t.Fatalf("read back: %v (%d events)", err, len(evs))
	}
	if evs[0].Outcome != "ok" || evs[0].Mood != "good" {
		t.Fatalf("stored outcome/mood = %q/%q, want ok/good", evs[0].Outcome, evs[0].Mood)
	}
}

func TestPostRejectsTopicEqualRepo(t *testing.T) {
	t.Setenv("WALLII_DIR", t.TempDir())
	if err := cmdPost([]string{"-r", "myrepo", "-t", "MyRepo", "work done"}); err == nil {
		t.Fatal("topic == repo accepted — it duplicates the repo column and ruins the topic facet")
	}
	if err := cmdPost([]string{"-r", "myrepo", "-t", "fix", "work done"}); err != nil {
		t.Fatalf("distinct topic rejected: %v", err)
	}
}

func TestGitRepoNameResolvesWorktreeToMainCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	main := filepath.Join(root, "mainrepo")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(main, "init", "-q")
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "session-worktree-42")
	git(main, "worktree", "add", "-q", wt)

	t.Chdir(wt)
	if got := gitRepoName(); got != "mainrepo" {
		t.Errorf("in worktree: gitRepoName() = %q, want %q — session worktrees must not fragment repo history", got, "mainrepo")
	}
	t.Chdir(main)
	if got := gitRepoName(); got != "mainrepo" {
		t.Errorf("in main checkout: gitRepoName() = %q, want %q", got, "mainrepo")
	}
}

func TestTailRendersTelemetry(t *testing.T) {
	r := &renderer{color: false}
	var b strings.Builder
	r.print(&b, wall.Event{
		TS: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC), Repo: "x", Actor: "worker/ci",
		Topic: "fix", Msg: "broke", Outcome: wall.OutcomeFailed, Mood: "stuck", TookS: 1500,
	}, false)
	out := b.String()
	for _, want := range []string{"✗", "(25m)", "·worker/ci"} {
		if !strings.Contains(out, want) {
			t.Errorf("tail line missing %q:\n%s", want, out)
		}
	}
	b.Reset()
	r.print(&b, wall.Event{TS: time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC), Repo: "x", Msg: "no telemetry"}, false)
	if strings.Contains(b.String(), "✓") || strings.Contains(b.String(), "(") {
		t.Errorf("unreported post must not fake telemetry:\n%s", b.String())
	}
	b.Reset()
	rc := &renderer{color: true, lastDay: "2026-08-12"}
	rc.print(&b, wall.Event{
		TS: time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC), Repo: "x", Topic: "fix",
		Msg: "broke", Outcome: wall.OutcomeFailed,
	}, false)
	if !strings.Contains(b.String(), "\x1b[31m✗\x1b[0m") {
		t.Errorf("failed outcome must render red in color mode:\n%q", b.String())
	}
}

func TestPostRejectsZeroTook(t *testing.T) {
	t.Setenv("WALLII_DIR", t.TempDir())
	if err := cmdPost([]string{"-r", "x", "--took", "0s", "zero duration"}); err == nil {
		t.Fatal("--took 0s accepted — it would be silently dropped by omitempty")
	}
}

func TestCmdDashWritesSubstitutedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist-yet") // fresh install: wall dir absent
	t.Setenv("WALLII_DIR", dir)
	if err := cmdDash(nil); err != nil {
		t.Fatalf("dash on empty wall dir failed: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "dashboard.html"))
	if err != nil {
		t.Fatalf("dashboard not written: %v", err)
	}
	html := string(b)
	for _, ph := range []string{"__WALLII_DATA__", "__GENERATED__"} {
		if strings.Contains(html, ph) {
			t.Errorf("placeholder %s survived substitution", ph)
		}
	}
	if !strings.Contains(html, "const RAW = []") {
		t.Error("empty wall should inline an empty RAW array")
	}
}
