// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

func TestResolveRepoDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "example-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WALLII_REPO_ROOTS", root)

	dir, ok := resolveRepoDir("example-repo")
	if !ok || dir != filepath.Join(root, "example-repo") {
		t.Fatalf("want %s, got %q ok=%v", filepath.Join(root, "example-repo"), dir, ok)
	}
	if _, ok := resolveRepoDir("no-such-repo"); ok {
		t.Fatal("resolved a repo that does not exist")
	}
	if _, ok := resolveRepoDir(""); ok {
		t.Fatal("resolved the empty repo name")
	}
}

func TestFollowUpPromptCarriesContext(t *testing.T) {
	e := wall.Event{
		TS:    time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		Repo:  "example-repo",
		Actor: "worker/ci",
		Topic: "fix",
		Msg:   "rate limiter off-by-one fixed",
		Refs:  []string{"https://git.example.com/x/example-repo/issues/44"},
	}
	p := followUpPrompt(e)
	for _, want := range []string{"example-repo", "fix", "worker/ci", "rate limiter off-by-one fixed", "issues/44"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q: %s", want, p)
		}
	}
}

func TestSessionCmdQuoting(t *testing.T) {
	// a single quote in the message must not break out of the shell quoting
	got := sessionCmd("/tmp/x", `it's "quoted"`)
	want := `cd '/tmp/x' && claude 'it'\''s "quoted"'`
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
	if got := sessionCmd("", "hi"); got != "claude 'hi'" {
		t.Fatalf("dirless command wrong: %s", got)
	}
}
