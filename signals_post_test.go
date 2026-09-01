// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
