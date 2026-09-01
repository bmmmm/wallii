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
		"deps bumped, two suites still failing",
		"retry budget raised, der Consumer haengt noch immer offen",
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
		// German "still" means *silently*, not *not yet* — and this very
		// fixture says so: the DNS was silently dead and is "jetzt umgebogen".
		// It sat in the noted list until 2026-08-20, which made the marker
		// fire on the everyday vocabulary of a German-written wall
		// ("stirbt still", "still gruen", "still falsch"). The English form
		// below still fires; only the German reading is exonerated.
		"staging DNS war still tot nach dem Rollout, jetzt umgebogen",
		"der Job stirbt still, Exit 127 nach der letzten Log-Zeile — Preamble drin",
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

// A marker under a negation is the statement that there is no leftover and
// no friction — the lint fired on exactly the posts that said so. The
// window is two words and stops at clause punctuation: a negation further
// away or in another clause must not silence a real marker.
func TestContradictionsHonorNegation(t *testing.T) {
	quiet := []string{
		"sweep closed: 5 of 5 hosts patched, nothing parked",
		"Runde zu: 4 von 4 Alerts wieder scharf, nichts vertagt",
		"kein TODO mehr im Checkout-Flow, alles verdrahtet",
		"no leftover in the migration, both shims removed",
		"zero remaining flags after the cleanup, config is flat",
		"todo list empty after the import sweep, three bugs closed",
		"TODO-Backlog leer: Rundung, stdin-Slurp und der Cache-Race sind zu",
		"not a dead end after all, the cache was the culprit",
		"ohne Workaround gelandet, der Upstream-Fix reichte",
		"no hack needed, the upstream patch landed in time",
	}
	for _, msg := range quiet {
		if got := Contradictions(Event{Msg: msg, Outcome: OutcomeOK, Mood: "good"}); len(got) > 0 {
			t.Errorf("negated marker noted in %q: %v", msg, got)
		}
	}
	noted := []string{
		"no time left, the remaining two consumers stay parked",
		"queue empty now, not yet wired into the alerting",
		"the pool is not yet empty, two jobs remaining",
		"nichts gefunden im Log; der Export ist noch nicht verdrahtet",
		"no rollback this time, but the retry path is still broken",
	}
	for _, msg := range noted {
		if got := Contradictions(Event{Msg: msg, Outcome: OutcomeOK}); len(got) == 0 {
			t.Errorf("a negation elsewhere silenced a real leftover: %q", msg)
		}
	}
}

// A marker glued into a path or identifier is a name: docs/todo-cutover.md
// is a file, not the statement that something is left to do. Four of the
// six TODO notes on the wall in 14 days were file names.
func TestContradictionsSkipNames(t *testing.T) {
	quiet := []string{
		"cutover runbook in docs/todo-cutover.md — deploy, seed, links covered",
		"TODO.md committed with the three follow-ups, probe tools rescued",
		"handover: TODO+memory+plan brought to the current state",
		"notes in todo.txt, the parked.json fixture renamed",
		"remaining_budget field added to the ledger export",
	}
	for _, msg := range quiet {
		if got := Contradictions(Event{Msg: msg, Outcome: OutcomeOK, Mood: "good"}); len(got) > 0 {
			t.Errorf("name noted as a leftover in %q: %v", msg, got)
		}
	}
	noted := []string{
		"TODO: wire the invalidation, the cache layer is in",
		"docs updated, invalidation todo — cache layer landed",
		"parked: the legacy shim, import path migration done",
		"TODO.md rewritten, and the exporter is still parked",
	}
	for _, msg := range noted {
		if got := Contradictions(Event{Msg: msg, Outcome: OutcomeOK}); len(got) == 0 {
			t.Errorf("a real leftover next to a name went unnoted: %q", msg)
		}
	}
}

// German "hing an" means depended on, not hung — the same homonym trap as
// "still". Only the readings that mean *hung* are friction.
func TestContradictionsReadHingAsHung(t *testing.T) {
	quiet := []string{
		"hook test hing an fremdem worktree, eigene fixture, mutation macht ihn rot",
		"der Export hängt an der Queue-Reihenfolge, jetzt deterministisch",
	}
	for _, msg := range quiet {
		if got := Contradictions(Event{Msg: msg, Mood: "good"}); len(got) > 0 {
			t.Errorf("dependency read as friction in %q: %v", msg, got)
		}
	}
	noted := []string{
		"der Build hing fest im Module-Cache, Lock geräumt",
		"der Runner hing minutenlang bei 80%, dann doch grün",
		"Deploy blieb hängen, bis der alte Container weg war",
		"build hung on the module cache until the lock was cleared",
	}
	for _, msg := range noted {
		if got := Contradictions(Event{Msg: msg, Mood: "good"}); len(got) == 0 {
			t.Errorf("hung build went unnoted: %q", msg)
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

// Doubts is the engine and Contradictions its renderer: the count note first,
// then one note per doubt, leftover before friction — the lines every other
// reader already printed. A count is never a doubt: "17 of 304" is a
// measurement far more often than a leftover, and a doubt is what gets
// raised as a challenge.
func TestDoubtsDriveContradictions(t *testing.T) {
	e := Event{Msg: "12 von 13 zu, der erste Ansatz war Sackgasse, rest parked", Outcome: OutcomeOK, Mood: "great"}
	ds := Doubts(e)
	if len(ds) != 2 || ds[0].Class != DoubtLeftover || ds[1].Class != DoubtFriction {
		t.Fatalf("want leftover then friction, got %+v", ds)
	}
	if ds[0].Marker != "parked" || ds[1].Marker != "Sackgasse" {
		t.Errorf("markers must be the words as written, got %q / %q", ds[0].Marker, ds[1].Marker)
	}
	want := []string{
		`the message says "12 von 13" but the outcome says ok — partial matches what you wrote`,
		ds[0].Note, ds[1].Note,
	}
	got := Contradictions(e)
	if len(got) != len(want) {
		t.Fatalf("Contradictions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	count := Event{Msg: "all green: 12 von 13 Alerts scharf, der Exporter kippt weiter weg", Outcome: OutcomeOK, Mood: "good"}
	if ds := Doubts(count); len(ds) != 0 {
		t.Errorf("a count must never become a doubt, got %+v", ds)
	}
	if got := Contradictions(count); len(got) != 1 {
		t.Errorf("the count note itself must survive, got %v", got)
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
