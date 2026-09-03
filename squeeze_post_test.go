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

// fakeStatusline writes a claudii-shaped cache file. Invented values in a
// real format: key=value lines, the two limit percentages and the unix second
// each window refills at.
func fakeStatusline(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "session-abcd1234")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// rawWall returns the stored lines exactly as they were written — the only
// place the difference between "no field" and "a field holding zero" is
// visible, and that difference is the whole design of these three fields.
func rawWall(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
	}
	return b.String()
}

// The law of the house, made executable. A post written with the limits all
// but spent keeps exactly the grade it was given: the pressure is stored
// beside it and never inside it.
//
// This is the test that has to stay red-able forever. The wall's open
// question is whether the bottom of the mood scale is ever reached, and the
// answer is due on 2026-09-22. A mood pushed down by arithmetic would be read
// there as that answer arriving — the one failure mode this whole feature
// could produce, and the one nobody would notice.
func TestAPostedMoodSurvivesTheSqueeze(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_SQUEEZE", "")
	t.Setenv(wall.SqueezeFileEnv, fakeStatusline(t,
		"rate_5h=97\nrate_7d=96\nreset_5h=1800000000\nreset_7d=1800600000\n"))

	if err := cmdPost([]string{"-r", "x", "-t", "fix", "--mood", "great", "landed first try, with the week nearly spent"}); err != nil {
		t.Fatal(err)
	}
	e := readWall(t, dir)[0]
	if e.Mood != "great" {
		t.Errorf("mood came back %q, want %q — the squeeze rewrote a posted grade", e.Mood, "great")
	}
	if e.SqueezeP != 96 || e.Squeeze5h != 97 || e.SqueezeSrc != wall.SqueezeSession {
		t.Errorf("post carries 7d %g / 5h %g from %q, want 96 / 97 from %q",
			e.SqueezeP, e.Squeeze5h, e.SqueezeSrc, wall.SqueezeSession)
	}
	// what is stored is what was read, not what it was worth: a pressure in
	// the field would be a number between 0 and 3 where a percentage belongs
	if wall.Squeeze(97, 0, wall.SqueezeDensityUnknown) == e.Squeeze5h {
		t.Errorf("squeeze_5h holds the pressure %g rather than the percentage read", e.Squeeze5h)
	}
}

// Test 21: every way of not having a reading ends in an absent field. Not a
// zero — zero is a budget somebody looked at and found untouched, and the two
// must never be the same line on the wall.
func TestAnUnreadableBudgetStoresNothingAtAll(t *testing.T) {
	fresh := fakeStatusline(t, "rate_5h=80\nrate_7d=70\n")
	stale := fakeStatusline(t, "rate_5h=80\nrate_7d=70\n")
	old := time.Now().Add(-wall.SqueezeMaxAge - time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ file, off string }{
		"a claudii file that is not there": {filepath.Join(t.TempDir(), "session-nothere"), ""},
		"a claudii file nobody rewrote":    {stale, ""},
		"the squeeze switched off":         {fresh, "off"},
	}
	for name, c := range cases {
		dir := t.TempDir()
		t.Setenv("WALLII_DIR", dir)
		t.Setenv("WALLII_SQUEEZE", c.off)
		t.Setenv(wall.SqueezeFileEnv, c.file)

		if err := cmdPost([]string{"-r", "x", "-t", "fix", "--mood", "ok", "posted with no idea what was left"}); err != nil {
			t.Fatal(err)
		}
		e := readWall(t, dir)[0]
		if e.SqueezeSrc != "" || e.SqueezeP != 0 || e.Squeeze5h != 0 {
			t.Errorf("%s: post carries %g / %g from %q, want nothing", name, e.SqueezeP, e.Squeeze5h, e.SqueezeSrc)
		}
		if raw := rawWall(t, dir); strings.Contains(raw, "squeeze") {
			t.Errorf("%s: the stored line mentions a squeeze: %s", name, raw)
		}
	}
}

// stats reports the stored halves and stops there.
func TestSqueezeLineReportsTheLimitsAndGradesNobody(t *testing.T) {
	got := squeezeLine(wall.Stats{SqueezePosts: 4, Squeeze5hTotal: 320, SqueezePTotal: 200})
	for _, want := range []string{"5h 80%", "7d 50%", "across 4 posts", "never in them"} {
		if !strings.Contains(got, want) {
			t.Errorf("squeeze line %q is missing %q", got, want)
		}
	}
	// no grade, no coverage figure, no arithmetic against the mood: the same
	// rule the grader line follows, and for the same reason
	for _, banned := range []string{"of posts", "%)", "off a mood"} {
		if strings.Contains(got, banned) {
			t.Errorf("squeeze line %q turned a reading into a dial with %q", got, banned)
		}
	}
	// most of the wall predates the field, and zero coverage is not an empty
	// budget
	if quiet := squeezeLine(wall.Stats{Posts: 400}); quiet != "" {
		t.Errorf("a wall with no readings printed %q", quiet)
	}
}

// The live term sits among the conditions and never inside an expression:
// `window 3.8 − 0.4` is arithmetic on the grade, this is not.
func TestMoodSqueezeTermIsReportedNeverSubtracted(t *testing.T) {
	b := wall.Budget{Pct5h: 91, Pct7d: 30, Src: wall.SqueezeSession}
	got := moodSqueezeTerm(b, wall.MoodNow{Avg: 4, Squeeze: 1.4})
	for _, want := range []string{"squeeze 1.4", "5h 91%", "7d 30%"} {
		if !strings.Contains(got, want) {
			t.Errorf("receipt term %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "−") || strings.Contains(got, " - ") {
		t.Errorf("receipt term %q reads as arithmetic on the grade", got)
	}
	// a budget nobody read says nothing, rather than saying zero
	if quiet := moodSqueezeTerm(wall.Budget{}, wall.MoodNow{}); quiet != "" {
		t.Errorf("an unread budget rendered %q", quiet)
	}
	// a read budget under no pressure still names the limits: that is the
	// finding, and the pressure is what has nothing to say
	loose := moodSqueezeTerm(wall.Budget{Pct5h: 4, Pct7d: 9, Src: wall.SqueezeSession}, wall.MoodNow{})
	if strings.Contains(loose, "squeeze") || !strings.Contains(loose, "5h 4%") {
		t.Errorf("a loose budget rendered %q", loose)
	}
}

// Per column, the conditions the post was written under — the stored
// percentages, beside the api term and following it in shape.
func TestMoodInspectorNamesTheSqueeze(t *testing.T) {
	evs := moodPosts("good", "rough", "ok")
	evs[0].SqueezeP, evs[0].Squeeze5h, evs[0].SqueezeSrc = 88, 94, wall.SqueezeSession
	st := moodStateOf(evs, 99)

	st.cursor = 0
	if got := moodInspect(st, 200); !strings.Contains(got, "squeeze 5h 94% 7d 88%") {
		t.Errorf("inspector = %q, want the limits this post was written under", got)
	}
	st.cursor = 1
	if got := moodInspect(st, 200); strings.Contains(got, "squeeze") {
		t.Errorf("inspector = %q, want silence where nobody measured", got)
	}
}

// A folded day averages over the posts that carried a reading, never over the
// ones nobody measured — the separation PulseN keeps, kept here too.
func TestMoodDayColumnFoldsTheSqueeze(t *testing.T) {
	evs := moodPosts("good", "good", "rough")
	evs[0].SqueezeP, evs[0].Squeeze5h, evs[0].SqueezeSrc = 40, 60, wall.SqueezeSession
	evs[1].SqueezeP, evs[1].Squeeze5h, evs[1].SqueezeSrc = 60, 80, wall.SqueezeSession
	st := moodStateOf(evs, 99)
	st.daily = true
	st.refold()
	st.cursor = 0

	p, _ := st.at()
	if p.SqueezeP != 50 || p.Squeeze5h != 70 || p.SqueezeN != 2 {
		t.Fatalf("day folds to 7d %g / 5h %g over %d readings; want 50 / 70 over 2", p.SqueezeP, p.Squeeze5h, p.SqueezeN)
	}
	if got := moodInspect(st, 200); !strings.Contains(got, "squeeze ~5h 70% 7d 50%") {
		t.Errorf("day inspector = %q, want the mean marked as one", got)
	}
}
