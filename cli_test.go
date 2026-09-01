// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// The wall this shipped for reported 78 ok / 5 partial / 0 failed and
// 27 great / 49 good / 7 ok — every value "in use" by a naive count, yet
// incapable of carrying a single piece of bad news. Counting distinct
// values would have called that healthy.
func TestCalibLineNamesTheMissingLowEnd(t *testing.T) {
	degenerate := wall.Stats{
		OK: 78, Partial: 5, Failed: 0,
		ByMood: []wall.NameCount{{Name: "great", Count: 27}, {Name: "good", Count: 49}, {Name: "ok", Count: 7}},
	}
	got := calibLine(degenerate, "")
	for _, want := range []string{"nothing ever failed", "mood never went below ok"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	healthy := wall.Stats{
		OK: 40, Partial: 6, Failed: 3,
		ByMood: []wall.NameCount{{Name: "good", Count: 20}, {Name: "ok", Count: 9}, {Name: "rough", Count: 4}},
	}
	if got := calibLine(healthy, ""); !strings.Contains(got, "reach their low end") {
		t.Errorf("a wall that reports failures must read as calibrated, got:\n%s", got)
	}
	// The follow-up command must carry the same window the numbers came from.
	windowed := wall.Stats{OK: 9, Contradicting: 2,
		ByMood: []wall.NameCount{{Name: "good", Count: 9}}}
	if got := calibLine(windowed, "14d"); !strings.Contains(got, "tail --contradicting --since 14d") {
		t.Errorf("the hint must name a runnable command scoped to the same window, got:\n%s", got)
	}
	if got := calibLine(windowed, ""); strings.Contains(got, "--since") {
		t.Errorf("without a window the hint must not invent one, got:\n%s", got)
	}
	if got := calibLine(wall.Stats{Posts: 12}, ""); got != "" {
		t.Errorf("no telemetry at all is a coverage question, not a calibration one: %q", got)
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

// The wall is its own clock: every duration on it before this existed was
// rounded to five minutes, so the derived value has to be defensible at the
// edges or it repeats that mistake with a machine's authority.
func TestAutoTookBounds(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	prior := func(ago time.Duration) []wall.Event {
		return []wall.Event{{TS: now.Add(-2 * time.Hour)}, {TS: now.Add(-ago)}}
	}
	cases := []struct {
		name  string
		prior []wall.Event
		want  time.Duration
		ok    bool
	}{
		{"no history at all", nil, 0, false},
		{"batch backfill, seconds apart", prior(3 * time.Second), 0, false},
		{"just under the floor", prior(59 * time.Second), 0, false},
		{"at the floor", prior(time.Minute), time.Minute, true},
		{"a normal work slice", prior(25 * time.Minute), 25 * time.Minute, true},
		{"at the ceiling", prior(8 * time.Hour), 8 * time.Hour, true},
		{"a night in the gap", prior(37 * time.Hour), 0, false},
	}
	for _, c := range cases {
		got, ok := autoTook(c.prior, now)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: autoTook = (%v, %v), want (%v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// The later of the two bounds wins: both mark a point where this piece of
// work demonstrably had not started yet, so the tighter one is the truthful
// one.
func TestAutoTookPrefersLaterOfSessionStartAndLastPost(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	t.Setenv("WALLII_SESSION_START", now.Add(-20*time.Minute).Format(time.RFC3339))
	if got, ok := autoTook([]wall.Event{{TS: now.Add(-3 * time.Hour)}}, now); !ok || got != 20*time.Minute {
		t.Errorf("session start after the last post: got (%v, %v), want 20m", got, ok)
	}
	if got, ok := autoTook([]wall.Event{{TS: now.Add(-5 * time.Minute)}}, now); !ok || got != 5*time.Minute {
		t.Errorf("last post after session start: got (%v, %v), want 5m", got, ok)
	}
	// unix seconds, because a shell hook reaches for `date +%s` first
	t.Setenv("WALLII_SESSION_START", strconv.FormatInt(now.Add(-45*time.Minute).Unix(), 10))
	if got, ok := autoTook(nil, now); !ok || got != 45*time.Minute {
		t.Errorf("unix-seconds session start: got (%v, %v), want 45m", got, ok)
	}
	// garbage must not fabricate a duration
	t.Setenv("WALLII_SESSION_START", "yesterday-ish")
	if _, ok := autoTook(nil, now); ok {
		t.Error("unparseable session start produced a duration")
	}
}

func TestPostDerivesTookFromOwnHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_ACTOR", "tester")
	t.Setenv("WALLII_SESSION_START", strconv.FormatInt(time.Now().Add(-30*time.Minute).Unix(), 10))

	if err := cmdPost([]string{"-r", "x", "-t", "fix", "first post of the session"}); err != nil {
		t.Fatal(err)
	}
	// second post follows within seconds — a backfill, not half an hour of work
	if err := cmdPost([]string{"-r", "x", "-t", "fix", "second post, same minute"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdPost([]string{"-r", "x", "-t", "fix", "--took", "none", "explicitly none"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdPost([]string{"-r", "x", "-t", "fix", "--took", "25m", "measured"}); err != nil {
		t.Fatal(err)
	}
	evs, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 4 {
		t.Fatalf("expected 4 posts, got %d", len(evs))
	}
	if evs[0].TookSrc != wall.TookAuto || evs[0].TookS < 1700 || evs[0].TookS > 1900 {
		t.Errorf("first post should carry ~30m derived from session start, got %ds src=%q", evs[0].TookS, evs[0].TookSrc)
	}
	if evs[1].TookS != 0 || evs[1].TookSrc != "" {
		t.Errorf("a post seconds after the previous one must carry no took, got %ds src=%q", evs[1].TookS, evs[1].TookSrc)
	}
	if evs[2].TookS != 0 {
		t.Errorf("--took none must disable derivation, got %ds", evs[2].TookS)
	}
	if evs[3].TookS != 1500 || evs[3].TookSrc != "" {
		t.Errorf("explicit --took must stay measured, got %ds src=%q", evs[3].TookS, evs[3].TookSrc)
	}
}

// A message that reports its own friction must never be more expensive to
// post than a bland one — otherwise the wall keeps its ratios and loses the
// story. The grade mismatch is noted on stderr and counted in stats; the
// post itself lands unchanged, word for word.
func TestPostKeepsContradictingMessagesVerbatim(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	contradicting := [][]string{
		{"-r", "x", "-t", "ci", "--outcome", "ok", "replicas green, one still broken"},
		{"-r", "x", "-t", "fix", "--mood", "good", "der native Pfad war Sackgasse, der Shim tut es"},
	}
	for _, args := range contradicting {
		if err := cmdPost(args); err != nil {
			t.Errorf("args %v rejected — a post must never cost more for being honest: %v", args, err)
		}
	}
	evs, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected both posts on the wall, got %d", len(evs))
	}
	if evs[0].Msg != "replicas green, one still broken" || evs[1].Msg != "der native Pfad war Sackgasse, der Shim tut es" {
		t.Errorf("messages must land verbatim, got %q / %q", evs[0].Msg, evs[1].Msg)
	}
	if got := wall.Compute(evs).Contradicting; got != 2 {
		t.Errorf("stats should surface both contradictions, got %d", got)
	}
}

// stats counts the contradicting posts and calls them the honest ones — and
// used to offer no way to reach them. The filter is that way, so it has to
// select exactly those and still compose with the other filters: a listing
// that quietly widens the window is worse than no listing.
func TestTailContradictingFilterSelectsOnlyTheHonestOnes(t *testing.T) {
	honest := wall.Event{Repo: "x", Actor: "a", Outcome: wall.OutcomeOK, Msg: "replicas green, one still broken"}
	bland := wall.Event{Repo: "x", Actor: "a", Outcome: wall.OutcomeOK, Msg: "replicas green"}
	otherRepo := wall.Event{Repo: "y", Actor: "a", Outcome: wall.OutcomeOK, Msg: "replicas green, one still broken"}

	// Guard the premise: without it this test would pass on an empty selector.
	if len(wall.Contradictions(honest)) == 0 {
		t.Fatal("fixture is not contradicting — the test would prove nothing")
	}
	if len(wall.Contradictions(bland)) != 0 {
		t.Fatal("bland fixture contradicts — pick a different message")
	}

	f := filter{contradicting: true}
	if !f.match(honest) {
		t.Error("a contradicting post must survive the filter")
	}
	if f.match(bland) {
		t.Error("a post whose grade matches its message must be filtered out")
	}

	scoped := filter{contradicting: true, repo: "x"}
	if scoped.match(otherRepo) {
		t.Error("--contradicting must compose with --repo, not override it")
	}

	// Off by default: every other listing keeps its current contents.
	if !(filter{}).match(bland) {
		t.Error("without the flag the filter must not drop anything")
	}
}

// Selecting the posts is only half of it — the reason has to be readable,
// and only where it was asked for.
func TestRendererPrintsContradictionReasonOnlyWhenAsked(t *testing.T) {
	e := wall.Event{Repo: "x", Topic: "ci", Actor: "a", TS: time.Now(),
		Outcome: wall.OutcomeOK, Msg: "replicas green, one still broken"}

	var quiet, loud strings.Builder
	(&renderer{}).print(&quiet, e, false)
	(&renderer{showContradictions: true}).print(&loud, e, false)

	if strings.Contains(quiet.String(), "↳") {
		t.Errorf("default tail must not print reasons, got:\n%s", quiet.String())
	}
	if !strings.Contains(loud.String(), "↳") {
		t.Errorf("--contradicting must name the reason, got:\n%s", loud.String())
	}
	if !strings.Contains(loud.String(), e.Msg) {
		t.Errorf("the post itself must still be printed, got:\n%s", loud.String())
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

// The grader is the poster's own words, like the message — so it renders
// on every listing with no flag asked for. Whoever moves it back behind a
// flag turns this red: mood is invisible in tail and gets written only
// because a hook asks, and a field that lives on a hook dies with it.
func TestTailShowsGraderWithoutAnyFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	grader := "CI only wanted green — loosening the assert would have done, 20min on it, not taken"
	if err := cmdPost([]string{"-r", "webshop", "-t", "fix", "-a", "bot/builder", "--outcome", "ok",
		"--grader", grader, "race in the retry loop closed: the reader saw the marker before the fsync"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if err := cmdPost([]string{"-r", "webshop", "-t", "docs", "-a", "bot/builder", "readme matches the flags again"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	out := captureStdout(t, func() error { return cmdTail(nil) })
	if !strings.Contains(out, "↷ "+grader) {
		t.Fatalf("bare tail must print the grader line, got:\n%s", out)
	}
	if got := strings.Count(out, "↷"); got != 1 {
		t.Errorf("one post carries a grader, want exactly one ↷ line, got %d in:\n%s", got, out)
	}
	// color mode: the glyph is dim, the words are not — they are the post's
	var b strings.Builder
	(&renderer{color: true}).print(&b, wall.Event{TS: time.Now(), Repo: "webshop", Msg: "closed", Grader: grader}, false)
	if !strings.Contains(b.String(), "↷\x1b[0m "+grader+"\n") {
		t.Errorf("color tail must print the grader line, got:\n%q", b.String())
	}
	// --json carries it under its own key, again without asking
	out = captureStdout(t, func() error { return cmdTail([]string{"--json"}) })
	if !strings.Contains(out, `"grader":"CI only wanted green`) {
		t.Errorf("tail --json must carry the grader, got:\n%s", out)
	}
}

// --grader makes the field findable: a month of them read together is
// where a rule comes from. It selects exactly the posts carrying one,
// composes with the other filters, and — like every filter — never folds.
func TestTailGraderFilterKeepsOnlyPostsWithOne(t *testing.T) {
	named := wall.Event{Repo: "webshop", Actor: "a", Msg: "cache measured, then removed", Grader: "none — the numbers said no"}
	silent := wall.Event{Repo: "webshop", Actor: "a", Msg: "cache measured, then removed"}
	otherRepo := named
	otherRepo.Repo = "api-gateway"

	f := filter{grader: true}
	if !f.match(named) {
		t.Error("a post with a grader must survive the filter")
	}
	if f.match(silent) {
		t.Error("a post without a grader must be filtered out")
	}
	if (filter{grader: true, repo: "webshop"}).match(otherRepo) {
		t.Error("--grader must compose with --repo, not override it")
	}
	if !(filter{}).match(silent) {
		t.Error("without the flag the filter must not drop anything")
	}
	// --grep reads the grader too: it is text on the post, not metadata
	if !(filter{grep: "numbers said"}).match(named) {
		t.Error("--grep must search the grader")
	}

	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	// five posts from one actor: the bare view would fold two of them
	for i, g := range []string{"none — the numbers said no", "", "", "", "regenerated the golden instead of asking why"} {
		args := []string{"-r", "webshop", "-t", "chore", "-a", "bot/busy"}
		if g != "" {
			args = append(args, "--grader", g)
		}
		if err := cmdPost(append(args, "unit "+strconv.Itoa(i)+" done")); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	out := captureStdout(t, func() error { return cmdTail([]string{"--grader"}) })
	if got := strings.Count(out, "↷"); got != 2 {
		t.Errorf("tail --grader must list exactly the two posts with one, unfolded, got %d in:\n%s", got, out)
	}
	if strings.Contains(out, "unit 1 done") || strings.Contains(out, "more from") {
		t.Errorf("tail --grader must neither show bare posts nor fold, got:\n%s", out)
	}
}

// --grader given but blank would vanish under omitempty and read as "never
// asked": the same guard --took has against zero. The form is checked, the
// words are not — a refusal lands like any confession, trimmed.
func TestPostRejectsBlankGrader(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	for _, args := range [][]string{
		{"-r", "x", "--grader", "", "blank grader"},
		{"-r", "x", "--grader", "   ", "whitespace grader"},
	} {
		if err := cmdPost(args); err == nil {
			t.Errorf("args %v accepted — an empty --grader would be silently dropped", args)
		}
	}
	if err := cmdPost([]string{"-r", "x", "--grader", "  none — the skip guards a missing binary  ", "trimmed"}); err != nil {
		t.Fatalf("a refusal was rejected: %v", err)
	}
	evs, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil || len(evs) != 1 {
		t.Fatalf("read back: %v (%d events)", err, len(evs))
	}
	if evs[0].Grader != "none — the skip guards a missing binary" {
		t.Errorf("stored grader = %q, want it trimmed", evs[0].Grader)
	}
}

// Counted, never graded: the line carries a fraction and the distinct
// count and no percentage — a percentage is a dial. Absence names the
// command that ends it, the way the dialog line does.
func TestGraderLineCountsWithoutAPercentage(t *testing.T) {
	got := graderLine(wall.Stats{Posts: 483, WithGrader: 61, GraderDistinct: 58})
	if want := "grader   61/483 posts name a grader moment · 58 distinct"; got != want {
		t.Errorf("graderLine = %q, want %q", got, want)
	}
	if strings.Contains(got, "%") {
		t.Errorf("the grader line must not carry a percentage: %q", got)
	}
	none := graderLine(wall.Stats{Posts: 12})
	if !strings.HasPrefix(none, "grader   none") || !strings.Contains(none, "wallii post --grader") {
		t.Errorf("absence must be named with the command that ends it, got %q", none)
	}
}
