// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Post lints. Telemetry a poster grades itself degenerates: after 126 posts
// the wall held 0 failed and 0 rough/stuck while the messages themselves
// reported dead ends and leftovers. Prose in a convention file did not fix
// that; the topic lint — a hard reject — did.
//
// But force is only free where it cannot cost anything worth having. Exactly
// one thing here is rejected: a topic echoing the repo, a field with no
// information in it either way. Everything about the grades is reported and
// let through. The wall's value is the account it gives of a day's work, and
// any check that can be satisfied by writing a duller message will
// eventually be satisfied that way. So Contradictions and Calibration
// observe and say what they see; they never make a post cost more.

// wordRe wraps alternatives in non-letter boundaries. \b is ASCII-only in
// RE2, so it would not fire before "übrig" — the wall is bilingual and every
// German marker would silently never match.
func wordRe(alts ...string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(` + strings.Join(alts, "|") + `)(?:[^\p{L}\p{N}]|$)`)
}

// leftoverMarkers name work that did not land completely. Deliberately
// narrow: a marker that fires on ordinary posts teaches agents to write
// around the lint instead of grading honestly. "rest" is absent for exactly
// that reason — "rest timer", "rest screen" are real feature names.
var leftoverMarkers = []*regexp.Regexp{
	wordRe("remaining", "übrig", "uebrig", "leftover"),
	wordRe("parked", "geparkt", "vertagt", "punted"),
	wordRe("teilweise", "partially", "partly"),
	wordRe("bis auf", "except for", "apart from"),
	wordRe("noch nicht", "not yet", "todo", "to do"),
	// English "still" only. German "still" means *silently*, not *not yet* —
	// "war still tot", "stirbt still", "still grün" all describe a condition
	// that was found and fixed, the opposite of a leftover. Pairing English
	// "still" with German state words fired on exactly the vocabulary this
	// wall is written in (2026-08-17, servers: "Split-DNS B-Seite war still
	// tot (bind-interfaces-Race). Repariert" — graded ok, and correctly so).
	wordRe("still (?:broken|failing|open|dead|red|missing)"),
	// The German equivalent is "noch immer"/"immer noch", never bare "still".
	wordRe("(?:noch immer|immer noch) (?:offen|tot|kaputt|rot|fehlt|defekt)"),
	wordRe("nur (?:der|die|das|den) erste", "only the first"),
}

// There is no count marker. "12 von 13" / "8 of 10" used to be one — a
// count against a larger total read as a leftover by arithmetic — and read
// against 14 days of the wall it was wrong every single time: 18 hits, 18
// measurements ("40 von 43 turns lagen unter dem ersten anker", "12 von 100
// antworten kippen", "17 of 304 oks drew a fix" — wallii's own audit number,
// reported back to the poster as unfinished work). A note that is wrong
// eighteen times in eighteen is not read, and it drags the notes beside it
// down with it: with it gone, `tail --contradicting` lists the four posts a
// human would also have doubted instead of twenty-two. No wording rule
// separates a finding from a leftover, so none is attempted.

// frictionMarkers name how the journey felt, not which bug was fixed. A
// "flaky test" or a "race condition" can be found and fixed on the first
// try — those words describe the defect. "Sackgasse", "endlich", "third
// attempt" describe the path, and the path is what mood grades.
var frictionMarkers = []*regexp.Regexp{
	wordRe("sackgasse", "dead end", "holzweg", "falsche fährte", "red herring"),
	wordRe("endlich", "finally", "doch noch", "at last"),
	wordRe("workaround", "hack", "gefrickelt", "frickel", "krücke", "kruecke"),
	wordRe("gab auf", "gave up", "aufgegeben", "abgebrochen"),
	// "stuck" only as a lived state: bare, it is also the name of a mood
	// value, and a post *about* the mood scale is not a friction report.
	// German "hing" only with the words that make it *hung*: bare, it also
	// reads as "hing an" — *depended on* — and fired on "Hook-Test hing an
	// fremdem Worktree" (2026-08-19), a dependency found and cut, graded
	// good and correctly so. The same homonym as "still" above.
	wordRe("festgefahren", "steckengeblieben", "steckte fest", "hung",
		"hing (?:fest|bei|beim|im|in|auf|ewig|minutenlang|stundenlang)",
		"hängt fest", "haengt fest", "blieb hängen", "blieb haengen",
		"hängengeblieben", "haengengeblieben",
		"(?:got|was|were|still|been) stuck", "stuck (?:at|on|in|for)"),
	wordRe("zurückgerollt", "zurueckgerollt", "rolled back", "reverted", "revert"),
	wordRe("überraschung", "ueberraschung", "surprise", "zurück auf los"),
	wordRe(`\d+\.? (?:anlauf|versuch|attempt|try)`,
		"(?:zweite|dritte|vierte|fünfte|fuenfte)[nrms]* (?:anlauf|versuch)",
		"(?:second|third|fourth|fifth) (?:attempt|try|time)",
		"mehrere (?:anläufe|anlaeufe|versuche)", "several attempts", "many tries"),
}

// CheckTopic rejects a topic that repeats the repo: it carries zero
// information and ruins the topic facet in stats — the feed would show the
// same word twice.
func CheckTopic(topic, repo string) error {
	if topic == "" || !strings.EqualFold(topic, repo) {
		return nil
	}
	return fmt.Errorf("topic %q duplicates the repo — say what kind of work it was: fix, feature, release, ci, deps, docs, security, infra, ops, chore", topic)
}

// DoubtClass names which scale a post's own words contradict. Two classes,
// one per grade; a class exists to be raised as a challenge.
type DoubtClass string

const (
	DoubtLeftover DoubtClass = "leftover" // a leftover marker graded ok
	DoubtFriction DoubtClass = "friction" // a friction marker under mood great/good
)

// Doubt is one disagreement between a post's grade and its message, in the
// two forms it is spoken: Note is the stderr sentence, Ask the challenge
// message. They differ because they do different work — the note explains,
// the ask has to fit MaxMsgRunes and name the cheapest honest answer.
type Doubt struct {
	Class  DoubtClass
	Marker string // the words that fired, as written
	Note   string // the stderr sentence
	Ask    string // ≤ MaxMsgRunes: the challenge message, names the regrade
}

// Doubts reports where a post's grades disagree with the post's own message,
// leftover before friction. Never on dialogue: Validate forbids outcome and
// mood on a react or challenge, so a challenge cannot draw a doubt of its
// own — the lint is structurally inert on what it raises.
//
// The Ask names the regrade as the cheapest answer on purpose. A challenge
// that only asked "really?" would leave the conclusion to the agent, and
// the cheapest one is to drop the word next time. Pointing at the grade —
// "regrade, or say why not" — keeps the gradient on the note, where an
// honest correction costs one flag, and off the message.
func Doubts(e Event) []Doubt {
	var out []Doubt
	if e.Outcome == OutcomeOK {
		if hit := firstMatch(leftoverMarkers, e.Msg); hit != "" {
			out = append(out, Doubt{Class: DoubtLeftover, Marker: hit,
				Note: fmt.Sprintf("the message says %q but the outcome says ok — work with a leftover is partial", hit),
				Ask:  fmt.Sprintf("%q graded ok — work with a leftover is partial. regrade, or say why not", hit)})
		}
	}
	if MoodScore(e.Mood) >= 4 {
		if hit := firstMatch(frictionMarkers, e.Msg); hit != "" {
			out = append(out, Doubt{Class: DoubtFriction, Marker: hit,
				Note: fmt.Sprintf("the message says %q but the mood says %s — that is friction: ok = several attempts, rough = repeatedly stuck", hit, e.Mood),
				Ask:  fmt.Sprintf("%q with mood %s — several attempts is ok. regrade, or say why not", hit, e.Mood)})
		}
	}
	return out
}

// Contradictions reports where a post's grades disagree with the post's own
// message: "still broken" graded ok, "Sackgasse" graded good — every Doubt's
// note, the same lines as before Doubts existed, so tail, stats and dash read
// exactly what they always read.
//
// Never an error, and never a reason to reject the post. Rejecting on words
// found in the message would put a price on exactly the words that make the
// wall worth reading — an agent that learns "saying Sackgasse costs me a
// retry" stops saying Sackgasse, and the wall keeps its clean ratios while
// losing the account of what actually happened. The message is the story;
// the grade is the index. When they disagree the message is almost always
// the truthful one, so the note says so and the post stands as written.
func Contradictions(e Event) []string {
	var out []string
	for _, d := range Doubts(e) {
		out = append(out, d.Note)
	}
	return out
}

// Two guards sit in front of every word marker. Both were paid for: each
// fired on posts that said the opposite of what the marker claims, and a
// lint wrong four times in five stops being read — which is how 27 noted
// contradictions produced zero challenges. Precision is the whole value of
// a note nobody is forced to act on.

// negatedBefore is a negation at most two words ahead of the marker. "5 von
// 5, nichts vertagt" states that nothing was parked; "kein Workaround" that
// none was needed. The window is short on purpose — a "no" three clauses
// back says nothing about this word — and it stops at clause punctuation,
// so "no time left, the remaining two stay parked" still fires.
var negatedBefore = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:nichts|nix|nicht|kein\p{L}*|no|not|nothing|none|zero|ohne|without)(?:\s+[^\s,.;:!?()—–]+){0,2}[\s"'(\[]*$`)

// emptyAfter is an emptiness word at most two words behind the marker:
// "TODO-Backlog leer", "todo list empty" report a leftover count of zero.
// Applied to one-word markers only — "not yet" carries its own negation,
// and "not yet empty" is a leftover.
var emptyAfter = regexp.MustCompile(`(?i)^(?:\s*[^\s,.;:!?()—–]+)?\s+(?:leer|geleert|empty|emptied)(?:[^\p{L}\p{N}]|$)`)

// glue fuses a marker into a path or an identifier: "docs/todo-cutover.md",
// "TODO.md", "TODO+memory+plan". A marker with one of these on either side
// and a word character beyond it is a name, and a file called TODO is not
// the statement that something is left to do.
const glue = "/._+-"

// guarded reports whether the marker at msg[start:end] is a name or a
// negated statement rather than a claim about the work.
func guarded(msg string, start, end int) bool {
	before, after := msg[:start], msg[end:]
	if glued(before, after) || negatedBefore.MatchString(before) {
		return true
	}
	return !strings.ContainsRune(msg[start:end], ' ') && emptyAfter.MatchString(after)
}

func glued(before, after string) bool {
	if r, n := utf8.DecodeLastRuneInString(before); n > 0 && strings.ContainsRune(glue, r) {
		if p, _ := utf8.DecodeLastRuneInString(before[:len(before)-n]); isWordRune(p) {
			return true
		}
	}
	if r, n := utf8.DecodeRuneInString(after); n > 0 && strings.ContainsRune(glue, r) {
		if p, _ := utf8.DecodeRuneInString(after[n:]); isWordRune(p) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// firstMatch returns the first marker in msg that survives the guards, as
// written. Every occurrence is tried: a message may name a file called TODO
// and still leave a real one.
func firstMatch(res []*regexp.Regexp, msg string) string {
	for _, re := range res {
		for _, m := range re.FindAllStringSubmatchIndex(msg, -1) {
			if !guarded(msg, m[2], m[3]) {
				return strings.TrimSpace(msg[m[2]:m[3]])
			}
		}
	}
	return ""
}

// CalibrationRun is the streak length that turns a grade into a reflex. Eight
// consecutive identical values are no longer a report — they are a habit.
const CalibrationRun = 8

// Calibration reports a degenerate telemetry series for one actor, or "".
// prior holds that actor's earlier posts oldest-first; e is the post just
// written and counts towards the streak, otherwise the warning would always
// lag one post behind the behaviour it describes.
func Calibration(prior []Event, e Event) string {
	evs := append(append([]Event{}, prior...), e)
	outStreak, out := 0, ""
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind != "" || evs[i].Outcome == "" {
			continue
		}
		if out == "" {
			out = evs[i].Outcome
		} else if evs[i].Outcome != out {
			break
		}
		outStreak++
	}
	moodStreak := 0
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind != "" || evs[i].Mood == "" {
			continue
		}
		if MoodScore(evs[i].Mood) < 4 {
			break
		}
		moodStreak++
	}

	var parts []string
	if outStreak >= CalibrationRun {
		parts = append(parts, fmt.Sprintf("%d posts in a row with outcome=%s", outStreak, out))
	}
	if moodStreak >= CalibrationRun {
		parts = append(parts, fmt.Sprintf("%d in a row with mood great/good", moodStreak))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("calibration — %s: %s. A wall with one value measures nothing.\n"+
		"  Anchors: several attempts = ok · repeatedly stuck = rough · gave up = stuck ·\n"+
		"  work with a leftover = partial. Grade the next one against those, not against habit.",
		orUnknown(e.Actor), strings.Join(parts, ", "))
}

func orUnknown(actor string) string {
	if actor == "" {
		return "this actor"
	}
	return actor
}
