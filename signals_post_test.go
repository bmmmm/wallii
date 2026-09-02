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

// hookMarker points HOME at a temp dir and names a session, so the post
// looks for the Stop hook's marker where a test can put one. Returns the
// path the hook would write for repo.
func hookMarker(t *testing.T, repo string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-0001")
	dir := filepath.Join(home, ".claude", "wall-post-reminders")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "sess-0001-"+repo+".shortcut")
}

// The hook's findings land on the post mechanically, whatever the poster
// wrote — through the real command, into the real store, and out again
// through tail --json under their own keys. Three markers, three readings:
// findings, a clean scan, and no scan at all.
func TestPostCarriesTheHooksSignals(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	marker := hookMarker(t, "webshop")
	if err := os.WriteFile(marker, []byte("internal/cart/cart_test.go\tt.Skip(\"flaky under load\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmdPost([]string{"-r", "webshop", "-t", "fix", "-a", "bot/builder", "--outcome", "ok",
		"cart totals no longer drift on the second discount"}); err != nil {
		t.Fatal(err)
	}
	e := readWall(t, dir)[0]
	if e.SignalSrc != wall.SignalHook || len(e.Signals) != 1 || e.Signals[0] != `internal/cart/cart_test.go: t.Skip("flaky under load")` {
		t.Fatalf("post carries %q from %q, want the marker's line from %q", e.Signals, e.SignalSrc, wall.SignalHook)
	}
	out := captureStdout(t, func() error { return cmdTail([]string{"--json"}) })
	for _, want := range []string{`"signals":["internal/cart/cart_test.go: t.Skip(\"flaky under load\")"]`, `"signal_src":"hook"`} {
		if !strings.Contains(out, want) {
			t.Errorf("tail --json is missing %s in:\n%s", want, out)
		}
	}

	// the marker is per session, not per post: the next post of the same
	// session carries the same finding — consuming it would break the
	// hook's "reported once"
	if err := cmdPost([]string{"-r", "webshop", "-t", "docs", "-a", "bot/builder", "readme names the new flag"}); err != nil {
		t.Fatal(err)
	}
	if evs := readWall(t, dir); len(evs[1].Signals) != 1 {
		t.Errorf("second post of the session carries %q, want the same finding", evs[1].Signals)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the post consumed the hook's marker: %v", err)
	}
}

func TestPostCarriesACleanScanAsASourceOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	marker := hookMarker(t, "webshop")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdPost([]string{"-r", "webshop", "-t", "fix", "-a", "bot/builder", "nothing to hide, nothing found"}); err != nil {
		t.Fatal(err)
	}
	e := readWall(t, dir)[0]
	if e.SignalSrc != wall.SignalHook || len(e.Signals) != 0 {
		t.Fatalf("post carries %q from %q, want no signals from %q — measured, nothing found", e.Signals, e.SignalSrc, wall.SignalHook)
	}
	out := captureStdout(t, func() error { return cmdTail([]string{"--json"}) })
	if !strings.Contains(out, `"signal_src":"hook"`) || strings.Contains(out, `"signals"`) {
		t.Errorf("tail --json must carry the source and no signals key, got:\n%s", out)
	}
}

// The stats line reports the difference between measurement and report and
// computes nothing from it — no percentage, ever. Silent where nobody
// measured, and a clean scan says so rather than printing zeros.
func TestSignalsLineReportsWithoutAPercentage(t *testing.T) {
	got := signalsLine(wall.Stats{Posts: 60, SignalsMeasured: 40, SignalsShown: 14, SignalsNamed: 9})
	if got != "signals  14 distinct shortcuts across 40 measured posts · 9 named a grader moment, 5 did not" {
		t.Errorf("signals line reads %q", got)
	}
	if strings.Contains(got, "%") {
		t.Errorf("a percentage is a dial, and this line must not have one: %q", got)
	}
	if clean := signalsLine(wall.Stats{SignalsMeasured: 3}); clean != "signals  measured on 3 posts, none carried a shortcut" {
		t.Errorf("clean window reads %q", clean)
	}
	if one := signalsLine(wall.Stats{SignalsMeasured: 1, SignalsShown: 1, SignalsNamed: 1}); one != "signals  1 distinct shortcut across 1 measured post · 1 named a grader moment, 0 did not" {
		t.Errorf("one post reads %q, want the singular on both counts", one)
	}
	if quiet := signalsLine(wall.Stats{Posts: 400, WithGrader: 12}); quiet != "" {
		t.Errorf("a wall nobody measured printed %q", quiet)
	}
}

// The audit is the honeypot that already existed: an ok that carried a
// measured shortcut and then drew a fix is marked, the line the diff showed
// is printed under it, and the two summary lines count both directions.
func TestAuditMarksMeasuredShortcutsAndCountsNamedOksThatHeld(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	now := time.Now().UTC()
	sig := `cart_test.go: t.Skip("flaky under load")`
	// old enough that the oks have outlived hauntProximity — "held" is a
	// claim about a window that has run out, and a fixture posted an hour
	// ago cannot make it
	old := -10 * 24 * time.Hour
	seed := []wall.Event{
		{TS: now.Add(old - time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "feature", Outcome: wall.OutcomeOK,
			Msg: "cart totals stable across discount rounds", Signals: []string{sig}, SignalSrc: wall.SignalHook},
		{TS: now.Add(old), Repo: "webshop", Actor: "bot/builder", Topic: "fix",
			Msg: "cart totals drifted on discount rounds once more"},
		{TS: now.Add(old + time.Hour), Repo: "webshop", Actor: "bot/reviewer", Topic: "feature", Outcome: wall.OutcomeOK,
			Msg: "checkout survives an expired voucher", Grader: "none — the timeout guards a missing sandbox binary"},
	}
	for _, e := range seed {
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	out := captureStdout(t, func() error { return cmdAudit([]string{"--since", "30d"}) })
	for _, want := range []string{
		"· measured shortcut",
		"    signal " + sig,
		"1 of 2 ok posts drew a fix",
		"1 of them came out of a session the hook had measured a shortcut in",
		"1 ok post named a grader moment and drew no fix — naming the cheap path costs nothing; leaving it out costs later.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("audit is missing %q in:\n%s", want, out)
		}
	}
	// the mark names what was measured — the session carried a shortcut —
	// and never which of the two the fix answered: signals hang on every
	// post of a session, so the line may sit in a file this post never saw
	if strings.Contains(out, "the skipped check was the gap") {
		t.Errorf("the audit claims a cause nobody measured:\n%s", out)
	}
	// the JSON shape carries the mark under its own key, and only when set
	out = captureStdout(t, func() error { return cmdAudit([]string{"--since", "30d", "--json"}) })
	if !strings.Contains(out, `"measured":true`) {
		t.Errorf("audit --json must carry the mark, got:\n%s", out)
	}
	// a window where everything held still says what the named oks prove
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := wall.Append(dir, seed[2]); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() error { return cmdAudit([]string{"--since", "30d"}) })
	if !strings.Contains(out, "no haunted oks") || !strings.Contains(out, "1 ok post named a grader moment and drew no fix") {
		t.Errorf("a held window must still count the named oks, got:\n%s", out)
	}
	// but an ok posted this morning has held nothing yet, and the line that
	// would claim it does stays away entirely rather than printing a zero
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	fresh := seed[2]
	fresh.TS = now.Add(-2 * time.Hour)
	if err := wall.Append(dir, fresh); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() error { return cmdAudit([]string{"--since", "30d"}) })
	if strings.Contains(out, "named a grader moment and drew no fix") {
		t.Errorf("a two-hour-old ok was counted as having held:\n%s", out)
	}
	if strings.Contains(out, "0 ok post") {
		t.Errorf("a zero was printed as if it were a finding:\n%s", out)
	}
	// and the same at the other end of the audit: a window WITH a haunting
	// but with nothing that held reads the zero line as a grading finding
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, e := range seed[:2] {
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	out = captureStdout(t, func() error { return cmdAudit([]string{"--since", "30d"}) })
	if !strings.Contains(out, "1 of 1 ok posts drew a fix") {
		t.Fatalf("the fixture must still haunt, got:\n%s", out)
	}
	if strings.Contains(out, "named a grader moment and drew no fix") {
		t.Errorf("nothing held here, and the audit said something about it:\n%s", out)
	}
}

// No marker is nobody measured: the stored line is indistinguishable from
// one written before the field existed.
func TestPostWithoutAMarkerCarriesNoSignalFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SESSION_START", "")
	hookMarker(t, "webshop")
	if err := cmdPost([]string{"-r", "webshop", "-t", "fix", "-a", "bot/builder", "posted with no hook in sight"}); err != nil {
		t.Fatal(err)
	}
	e := readWall(t, dir)[0]
	if e.SignalSrc != "" || e.Signals != nil {
		t.Fatalf("post carries %q from %q, want nothing at all", e.Signals, e.SignalSrc)
	}
	out := captureStdout(t, func() error { return cmdTail([]string{"--json"}) })
	if strings.Contains(out, "signal") {
		t.Errorf("an unmeasured post must not mention signals, got:\n%s", out)
	}
}
