// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"strings"
	"testing"
	"time"
)

func TestCheckTopicRejectsRepoEcho(t *testing.T) {
	if err := CheckTopic("wallii", "wallii"); err == nil {
		t.Error("topic equal to repo accepted")
	}
	if err := CheckTopic("WALLII", "wallii"); err == nil {
		t.Error("case-insensitive echo accepted")
	}
	for _, topic := range []string{"", "fix", "release"} {
		if err := CheckTopic(topic, "wallii"); err != nil {
			t.Errorf("topic %q rejected: %v", topic, err)
		}
	}
}

// Fixtures are invented, in the style of the demo wall (webshop, api-gateway,
// docs-site …) — a test suite is a bad place to republish whatever happens to
// be on the author's own wall. The shapes they exercise are real, though:
// each noted case mirrors a way a post has actually claimed ok while saying
// otherwise, and "rest timer" is why "rest" alone is not a marker.
func TestContradictionsSpotsLeftovers(t *testing.T) {
	noted := []string{
		"12 von 13 Alerts wieder scharf, der Exporter kippt weiter weg",
		"8 of 10 flaky specs fixed, pushed to main",
		"staging DNS war still tot nach dem Rollout, jetzt umgebogen",
		"deps bumped, two suites still failing",
		"import path migration done, the legacy shim is parked for now",
		"cache layer landed, invalidation not yet wired",
		"queue drained except for the poison messages",
		"config split teilweise durch, der Rest wartet auf review",
	}
	for _, msg := range noted {
		if got := Contradictions(Event{Msg: msg, Outcome: OutcomeOK}); len(got) == 0 {
			t.Errorf("no note for: %q", msg)
		}
	}
	quiet := []string{
		"rest timer in the checkout poller reset to 30s, no more double charge",
		"rest endpoint returns 204 on an empty cart instead of 500",
		"3 von 3 Replicas gruen, Alerting zieht",
		"all 12 of 12 checks green after the runner bump",
		"gift card flow behind a feature flag, preview build attached",
		"order lookup by number highlights the matching row",
	}
	for _, msg := range quiet {
		if got := Contradictions(Event{Msg: msg, Outcome: OutcomeOK}); len(got) > 0 {
			t.Errorf("spurious note for %q: %v", msg, got)
		}
	}
}

// Anything but ok is the poster admitting the leftover already — there is no
// contradiction left to name.
func TestContradictionsIgnoreHonestOutcomes(t *testing.T) {
	msg := "12 von 13 zu, der Rest ist noch nicht angefasst"
	for _, out := range []string{"", OutcomePartial, OutcomeFailed} {
		if got := Contradictions(Event{Msg: msg, Outcome: out}); len(got) > 0 {
			t.Errorf("outcome %q noted: %v", out, got)
		}
	}
}

func TestContradictionsSpotsFriction(t *testing.T) {
	noted := []string{
		"backup share endlich read-only gemountet, der FUSE-Weg war Sackgasse",
		"endlich gruen: der Runner brauchte das Image-Pin",
		"submit button pinned at 552px — im dritten Anlauf, scroll anchor an 5 Stellen",
		"deploy rolled back, the health check never went green",
		"shipped with a workaround for the upstream panic",
		"tracked it down after several attempts, the map was a red herring",
		"gave up on the native path, the shim stays",
		"build hung on the module cache until the lock was cleared",
	}
	for _, msg := range noted {
		for _, mood := range []string{"great", "good"} {
			if got := Contradictions(Event{Msg: msg, Mood: mood}); len(got) == 0 {
				t.Errorf("no note for %s: %q", mood, msg)
			}
		}
	}
	quiet := []string{
		// the defect is named, not the journey — a flake or a race can be
		// found on the first try
		"fixed flaky bats test, pushed to main",
		"race condition in the queue writer closed, one mutex",
		// a post about the mood vocabulary is not a friction report
		"docs: the mood scale spelled out — great, good, ok, rough, stuck",
		"checkout total recomputed after a coupon change, unit test added",
		"renderer moved into its own module, service worker cache bumped",
	}
	for _, msg := range quiet {
		if got := Contradictions(Event{Msg: msg, Mood: "good"}); len(got) > 0 {
			t.Errorf("spurious note for %q: %v", msg, got)
		}
	}
}

// ok/rough/stuck already report the friction — nothing to say.
func TestContradictionsIgnoreHonestMoods(t *testing.T) {
	msg := "der erste Ansatz war Sackgasse, dritter Anlauf"
	for _, mood := range []string{"", "ok", "rough", "stuck"} {
		if got := Contradictions(Event{Msg: msg, Mood: mood}); len(got) > 0 {
			t.Errorf("mood %q noted: %v", mood, got)
		}
	}
}

// The whole point of the redesign: a post that names its own friction is the
// most valuable kind, so posting it must never cost more than posting a bland
// one. Contradictions returns notes, never an error, and nothing else here
// can turn them into one.
func TestContradictionsNeverGate(t *testing.T) {
	e := Event{Repo: "x", Msg: "der erste Ansatz war Sackgasse, 12 von 13 zu", Outcome: OutcomeOK, Mood: "great"}
	if got := Contradictions(e); len(got) < 2 {
		t.Fatalf("expected notes for both scales, got %v", got)
	}
	if err := e.Validate(); err != nil {
		t.Errorf("a contradicting post must still be storable: %v", err)
	}
}

func streak(n int, outcome, mood string) []Event {
	out := make([]Event, n)
	for i := range out {
		out[i] = Event{TS: time.Now(), Repo: "x", Actor: "a", Msg: "m", Outcome: outcome, Mood: mood}
	}
	return out
}

func TestCalibrationFiresOnlyAtRunLength(t *testing.T) {
	last := Event{Repo: "x", Actor: "a", Msg: "m", Outcome: OutcomeOK, Mood: "good"}
	// the new post counts towards the run, so CalibrationRun-1 priors are
	// already the threshold
	if got := Calibration(streak(CalibrationRun-2, OutcomeOK, "good"), last); got != "" {
		t.Errorf("fired one short of the run: %q", got)
	}
	got := Calibration(streak(CalibrationRun-1, OutcomeOK, "good"), last)
	if got == "" {
		t.Fatal("silent at the run length")
	}
	if !strings.Contains(got, "outcome=ok") || !strings.Contains(got, "mood great/good") {
		t.Errorf("both degenerate scales must be named: %q", got)
	}
	if !strings.Contains(got, "a:") {
		t.Errorf("warning must name the actor: %q", got)
	}
}

func TestCalibrationBreaksOnAnyOtherValue(t *testing.T) {
	prior := streak(CalibrationRun*2, OutcomeOK, "good")
	prior[len(prior)-1].Outcome = OutcomePartial
	prior[len(prior)-1].Mood = "rough"
	last := Event{Repo: "x", Actor: "a", Msg: "m", Outcome: OutcomeOK, Mood: "good"}
	if got := Calibration(prior, last); got != "" {
		t.Errorf("a single differing post must reset both runs, got %q", got)
	}
}

// Untelemetered posts neither extend nor break a run: they say nothing about
// the grade, and treating them as a reset would let an actor stay silent
// every eighth post to mute the warning.
func TestCalibrationSkipsPostsWithoutTelemetry(t *testing.T) {
	prior := streak(CalibrationRun-1, OutcomeOK, "good")
	prior = append(prior, Event{Repo: "x", Actor: "a", Msg: "no telemetry"})
	last := Event{Repo: "x", Actor: "a", Msg: "m", Outcome: OutcomeOK, Mood: "good"}
	if Calibration(prior, last) == "" {
		t.Error("a bare post must not reset the run")
	}
}

func TestCalibrationIgnoresRegistrationEvents(t *testing.T) {
	prior := streak(CalibrationRun-1, OutcomeOK, "good")
	prior = append(prior, Event{Repo: "x", Actor: "a", Msg: "attached", Kind: KindAttach, Outcome: OutcomeFailed, Mood: "stuck"})
	last := Event{Repo: "x", Actor: "a", Msg: "m", Outcome: OutcomeOK, Mood: "good"}
	if Calibration(prior, last) == "" {
		t.Error("attach/detach must not count as work and must not reset the run")
	}
}

// A mixed but healthy history is the case that must stay silent — otherwise
// the warning becomes wallpaper and stops being read.
func TestCalibrationSilentOnSpread(t *testing.T) {
	prior := []Event{
		{Outcome: OutcomeOK, Mood: "good"}, {Outcome: OutcomePartial, Mood: "ok"},
		{Outcome: OutcomeOK, Mood: "great"}, {Outcome: OutcomeFailed, Mood: "stuck"},
		{Outcome: OutcomeOK, Mood: "rough"}, {Outcome: OutcomeOK, Mood: "good"},
	}
	if got := Calibration(prior, Event{Actor: "a", Outcome: OutcomeOK, Mood: "good"}); got != "" {
		t.Errorf("fired on a spread history: %q", got)
	}
}
