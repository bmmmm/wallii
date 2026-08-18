// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	wordRe("still (?:broken|failing|open|offen|tot|dead|red|missing|fehlt)"),
	wordRe("nur (?:der|die|das|den) erste", "only the first"),
}

// countRe catches "12 von 13" / "8 of 10" — a count against a larger total
// is a leftover by arithmetic, not by wording.
var countRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(\d+)\s+(?:von|of)\s+(\d+)(?:[^\p{L}\p{N}]|$)`)

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
	// value, and a post *about* the mood scale is not a friction report
	wordRe("festgefahren", "steckengeblieben", "steckte fest", "hing", "hung",
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

// Contradictions reports where a post's grades disagree with the post's own
// message: "12 von 13" graded ok, "Sackgasse" graded good.
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
	if e.Outcome == OutcomeOK {
		if m := countRe.FindStringSubmatch(e.Msg); m != nil {
			done, _ := strconv.Atoi(m[1])
			total, _ := strconv.Atoi(m[2])
			if done < total {
				out = append(out, fmt.Sprintf("the message says %q but the outcome says ok — partial matches what you wrote", strings.TrimSpace(m[0])))
			}
		}
		if hit := firstMatch(leftoverMarkers, e.Msg); hit != "" {
			out = append(out, fmt.Sprintf("the message says %q but the outcome says ok — work with a leftover is partial", hit))
		}
	}
	if MoodScore(e.Mood) >= 4 {
		if hit := firstMatch(frictionMarkers, e.Msg); hit != "" {
			out = append(out, fmt.Sprintf("the message says %q but the mood says %s — that is friction: ok = several attempts, rough = repeatedly stuck", hit, e.Mood))
		}
	}
	return out
}

func firstMatch(res []*regexp.Regexp, msg string) string {
	for _, re := range res {
		if m := re.FindStringSubmatch(msg); m != nil {
			return strings.TrimSpace(m[1])
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
