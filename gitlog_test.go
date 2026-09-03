// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Everything below builds its own repositories from invented data. Nothing
// is read from a real checkout, and the suite skips rather than pretends
// when git is absent — a green run without git would only prove that the
// skip works.

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — the exec boundary cannot be measured without it")
	}
}

// runGit runs a fixture command and fails loudly. Fixture setup is the one
// place where a silent git failure would turn into a green test over an
// empty repository.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"), // the machine's identity stays out
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Fixture", "GIT_COMMITTER_NAME=Fixture",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo builds a repo whose commits sit on given days, committed by mail.
func newRepo(t *testing.T, root, name, mine string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", mine)
	runGit(t, dir, "config", "user.name", "Fixture")
	return dir
}

// commitAt writes one commit with an explicit committer date and author mail.
func commitAt(t *testing.T, dir, file, mail string, when time.Time) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(when.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", file)
	stamp := when.Format(time.RFC3339)
	cmd := exec.Command("git", "-c", "user.email="+mail, "commit", "-q", "-m", "fixture "+file)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Fixture", "GIT_COMMITTER_NAME=Fixture",
		"GIT_AUTHOR_EMAIL="+mail, "GIT_COMMITTER_EMAIL="+mail,
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

// The trap, and the reason this file talks to a real git at all: `git log
// --since=30d` is accepted without a word of complaint and returns nothing.
// git does not read "30d", and a window that reports zero commits looks
// exactly like a repo where nobody worked. Measured on this project the day
// this was written: 0 against 77 for the identical window written as
// --since=30.days. Turn the ISO timestamp in commitDays back into the
// duration a person typed and this test goes red.
func TestCommitDaysCountsARealWindow(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	dir := newRepo(t, root, "webshop", "dev@example.invalid")
	now := time.Now()
	for i := 1; i <= 5; i++ {
		commitAt(t, dir, "f"+string(rune('a'+i)), "dev@example.invalid", now.AddDate(0, 0, -i))
	}
	byDay, others, err := commitDays(context.Background(), dir, now.AddDate(0, 0, -30), now, time.Local, "dev@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range byDay {
		total += n
	}
	if total != 5 {
		t.Fatalf("a repo with five commits in the window reported %d — a zero here is indistinguishable from an idle month", total)
	}
	if len(byDay) != 5 {
		t.Fatalf("five commits on five days want five day keys, got %d: %v", len(byDay), byDay)
	}
	if len(others) != 0 {
		t.Fatalf("every commit is the repo's own identity, got %v in others", others)
	}
	for key := range byDay {
		if _, err := time.Parse("2006-01-02", key); err != nil {
			t.Fatalf("day key %q is not the format the fold and the dashboard agree on", key)
		}
	}
}

// Foreign authors are reported beside the count, never inside it and never
// hidden: bot blocklists go stale, an identity comparison does not.
func TestCommitDaysSplitsForeignAuthors(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	dir := newRepo(t, root, "webshop", "dev@example.invalid")
	now := time.Now()
	commitAt(t, dir, "mine", "dev@example.invalid", now.AddDate(0, 0, -1))
	commitAt(t, dir, "theirs", "ci-runner[bot]@example.invalid", now.AddDate(0, 0, -1))
	commitAt(t, dir, "theirs2", "someone@example.invalid", now.AddDate(0, 0, -2))

	byDay, others, err := commitDays(context.Background(), dir, now.AddDate(0, 0, -30), now, time.Local, "dev@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	mine, foreign := 0, 0
	for _, n := range byDay {
		mine += n
	}
	for _, n := range others {
		foreign += n
	}
	if mine != 1 || foreign != 2 {
		t.Fatalf("split wrong: %d own, %d foreign — want 1 and 2 (%v / %v)", mine, foreign, byDay, others)
	}
	// no identity at all claims nothing rather than everything
	_, allForeign, err := commitDays(context.Background(), dir, now.AddDate(0, 0, -30), now, time.Local, "")
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, n := range allForeign {
		sum += n
	}
	if sum != 3 {
		t.Fatalf("without a user.email nothing may be claimed as own, got %d of 3 in others", sum)
	}
}

// A linked worktree is the same repository. Session worktrees are how this
// project works, and counting one as a repo of its own would double every
// commit made in it — the same reason gitRepoName() collapses them when it
// writes a name onto the wall.
func TestRepoCheckoutCollapsesWorktreesAndSubdirs(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	dir := newRepo(t, root, "webshop", "dev@example.invalid")
	commitAt(t, dir, "seed", "dev@example.invalid", time.Now().Add(-time.Hour))
	t.Setenv("WALLII_REPO_ROOTS", root)

	main, src, err := repoCheckout(context.Background(), "webshop")
	if err != nil {
		t.Fatalf("the plain checkout must resolve: %v", err)
	}
	if src != "roots" {
		t.Fatalf("src names how it was found, got %q", src)
	}

	// a linked worktree offered under the repo's own name, from a second
	// root, is the repository and resolves to the main checkout — not to
	// itself. Make repoCheckout hand back the candidate and this is red.
	root2 := t.TempDir()
	runGit(t, dir, "worktree", "add", "-q", "-b", "twin", filepath.Join(root2, "webshop"))
	t.Setenv("WALLII_REPO_ROOTS", root2)
	got, _, err := repoCheckout(context.Background(), "webshop")
	if err != nil {
		t.Fatalf("a worktree named like its repo must resolve: %v", err)
	}
	if samePath(t, got) != samePath(t, dir) {
		t.Fatalf("a worktree must collapse onto the main checkout: got %s, want %s", got, dir)
	}
	t.Setenv("WALLII_REPO_ROOTS", root)

	// a linked worktree living under a root, named like a repo of its own
	wt := filepath.Join(root, "webshop-hotfix")
	runGit(t, dir, "worktree", "add", "-q", "-b", "hotfix", wt)
	if _, _, err := repoCheckout(context.Background(), "webshop-hotfix"); err == nil {
		t.Fatal("a worktree must not resolve under its own name — its commits belong to the main checkout")
	}

	// a plain directory that is not a repository at all, and one that sits
	// inside somebody else's: a wall repo called Documents must not silently
	// become the home folder's enclosing repository
	if err := os.MkdirAll(filepath.Join(root, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repoCheckout(context.Background(), "Documents"); err == nil {
		t.Fatal("a plain directory resolved as a checkout")
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WALLII_REPO_ROOTS", dir)
	if _, _, err := repoCheckout(context.Background(), "docs"); err == nil {
		t.Fatalf("a subdirectory of %s resolved under its own name", main)
	}
}

// A `wallii dash` started from inside a hook inherits GIT_DIR and
// GIT_WORK_TREE, and -C does not save you from them: every repo on the wall
// would be measured as the hook's own, once per name. Unset the stripping in
// gitEnv and this goes red.
func TestCollectCommitsIgnoresAnInheritedGitDir(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	own := newRepo(t, root, "webshop", "dev@example.invalid")
	other := newRepo(t, root, "ledger", "dev@example.invalid")
	now := time.Now()
	commitAt(t, own, "a", "dev@example.invalid", now.AddDate(0, 0, -1))
	commitAt(t, own, "b", "dev@example.invalid", now.AddDate(0, 0, -1))
	commitAt(t, other, "only-one", "dev@example.invalid", now.AddDate(0, 0, -1))

	t.Setenv("WALLII_REPO_ROOTS", root)
	t.Setenv("GIT_DIR", filepath.Join(own, ".git"))
	t.Setenv("GIT_WORK_TREE", own)

	got := collectCommits(context.Background(), []string{"webshop", "ledger"}, now.AddDate(0, 0, -30), now, time.Local)
	count := func(name string) int {
		n := 0
		for _, v := range got[name].ByDay {
			n += v
		}
		return n
	}
	if got["ledger"].Err != "" {
		t.Fatalf("ledger was not measured: %q", got["ledger"].Err)
	}
	if count("webshop") != 2 || count("ledger") != 1 {
		t.Fatalf("an inherited GIT_DIR leaked into the count: webshop %d, ledger %d — want 2 and 1",
			count("webshop"), count("ledger"))
	}
}

// A repo the deadline never reached carries a reason, never a zero: the fold
// treats an entry it cannot trust as unmeasured and names it, and "nobody
// looked" must never render as "nothing happened".
func TestCollectCommitsMarksUnvisitedReposTimedOut(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	newRepo(t, root, "webshop", "dev@example.invalid")
	t.Setenv("WALLII_REPO_ROOTS", root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the deadline is already gone before the first repo is reached
	got := collectCommits(ctx, []string{"webshop", "ledger"}, time.Now().AddDate(0, 0, -30), time.Now(), time.Local)
	for _, name := range []string{"webshop", "ledger"} {
		rc, ok := got[name]
		if !ok {
			t.Fatalf("%s vanished from the result — the fold would read that as never measured, which is right, but the reason is lost", name)
		}
		if rc.Measured() {
			t.Fatalf("%s came back as a measurement after the context died: %+v", name, rc)
		}
	}
}

// WALLII_GIT_TIMEOUT is a duration, and a value that is not one says so
// instead of silently becoming zero.
func TestGitTimeoutOverride(t *testing.T) {
	t.Setenv("WALLII_GIT_TIMEOUT", "12s")
	if got := gitTimeout(); got != 12*time.Second {
		t.Fatalf("override ignored, got %v", got)
	}
	t.Setenv("WALLII_GIT_TIMEOUT", "soon")
	if got := gitTimeout(); got != 5*time.Second {
		t.Fatalf("garbage must fall back to the default, got %v", got)
	}
}

// A submodule's common dir is <super>/.git/modules/<name>, and its basename
// is the very name the wall knows the repo by: gitRepoName falls through to
// --show-toplevel there, and the resolver has to invert that branch too.
// Without it Dir pointed into the superproject's .git — a directory git log
// happens to accept, so the count was right by accident while the "belongs
// to" guard never saw the checkout it was meant to check.
func TestRepoCheckoutResolvesASubmoduleToItsCheckout(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	sub := newRepo(t, root, "sub", "dev@example.invalid")
	commitAt(t, sub, "seed", "dev@example.invalid", time.Now().Add(-time.Hour))
	super := newRepo(t, root, "super", "dev@example.invalid")
	runGit(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sub")
	t.Setenv("WALLII_REPO_ROOTS", super)

	got, _, err := repoCheckout(context.Background(), "sub")
	if err != nil {
		t.Fatalf("the submodule checkout must resolve: %v", err)
	}
	if strings.Contains(got, ".git") {
		t.Fatalf("resolved into a git directory, not a checkout: %s", got)
	}
	if want := samePath(t, filepath.Join(super, "sub")); samePath(t, got) != want {
		t.Fatalf("Dir = %s, want %s", got, want)
	}
}

// samePath follows symlinks so a path git printed compares with the one the
// fixture built — macOS hands out temp dirs through /var, git answers /private/var.
func samePath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
