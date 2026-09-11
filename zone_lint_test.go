// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// time.Local is not a zone this program may use. It cannot be named —
// String() is the literal "Local" — so nothing that reaches a dashboard, a
// timestamp or a day boundary can be built from it, and it ignores WALLII_TZ
// entirely: with it set, a .Local() left standing prints hours that the
// measurements do not agree with.
//
// zone.go is the one place allowed to mention it, to compare against it. The
// allowlist beside it is empty and must stay empty — this test exists
// because a display-side pass is exactly the kind of work that gets left
// half-done, and the half-done state is one where `wallii tail` and `wallii
// dash` disagree about what time a post was written.
func TestNothingReachesForTimeLocal(t *testing.T) {
	// zone.go is the only file that may name it, to compare against it. The
	// allowlist is empty and must stay empty.
	allowed := map[string]bool{
		"zone.go": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		// Tests are exempt where they build a fixture IN a zone — passing
		// time.Local as a loc parameter is choosing a zone, which is what
		// that parameter is for. What a test may not do is reach for the
		// viewer's zone the way production used to, and the one place that
		// did (tui_mood_test) goes through inZone() like the code it mirrors.
		if e.IsDir() || filepath.Ext(name) != ".go" || allowed[name] || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		src := string(b)
		for _, banned := range []string{"time.Local", ".Local()"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s uses %s — every instant goes through inZone(), every boundary through the loc it was handed", name, banned)
			}
		}
	}
	// a guard against the guard: an empty input set would pass silently
	if checked < 10 {
		t.Fatalf("only %d files were checked — this lint is not looking at the package", checked)
	}
}
