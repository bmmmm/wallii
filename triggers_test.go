// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProtocol points HOME at a temp dir and puts a protocol file where the
// hook would have written one — no lines at all means no protocol.
func writeProtocol(t *testing.T, lines ...string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if len(lines) == 0 {
		return
	}
	dir := filepath.Join(home, ".claude", "wall-post-reminders")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "stops-2026-09.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stopLine(iso, sid, repo, exit, sig, idle, commit string) string {
	return strings.Join([]string{iso, sid, repo, "exit=" + exit, "sig=" + sig, "idle=" + idle, "commit=" + commit}, "\t")
}

// Without a protocol the command says there is none. "0 firings" would be the
// same lie the protocol was built against, one level up: a hook that never
// ran and a trigger whose condition never held would read alike, and the
// zero would be believed.
func TestCmdTriggersSaysNoProtocolRatherThanZero(t *testing.T) {
	writeProtocol(t)
	out := captureStdout(t, func() error { return cmdTriggers(nil) })
	if !strings.Contains(out, "no protocol") {
		t.Fatalf("output = %q, want it to say there is no protocol", out)
	}
	for _, forbidden := range []string{"0 stops", "0 firings", "reached 0"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output reports %q for a hook that never wrote a line:\n%s", forbidden, out)
		}
	}
}

// The first number is reach, because it decides how to read every other one:
// a trigger with no firings across Stops it never reached has not been
// measured. Three of these four Stops died on the loop breaker.
func TestCmdTriggersLeadsWithReach(t *testing.T) {
	writeProtocol(t,
		stopLine("2026-09-01T08:00:00Z", "s1", "webshop", "loop", "unreached", "unreached", "unreached"),
		stopLine("2026-09-01T08:30:00Z", "s2", "webshop", "loop", "unreached", "unreached", "unreached"),
		stopLine("2026-09-01T09:00:00Z", "s3", "webshop", "loop", "unreached", "unreached", "unreached"),
		stopLine("2026-09-01T10:00:00Z", "s4", "webshop", "idle", "clean", "fired", "unreached"),
	)
	out := captureStdout(t, func() error { return cmdTriggers(nil) })
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(first, "reached ") {
		t.Fatalf("first line = %q, want the reach count first", first)
	}
	if !strings.Contains(first, "1 of 4 stops") {
		t.Errorf("first line = %q, want 1 of 4 stops", first)
	}
	if !strings.Contains(out, "fired 1") || !strings.Contains(out, "unreached 3") {
		t.Errorf("output does not put the one firing beside the three Stops that never reached it:\n%s", out)
	}
}
