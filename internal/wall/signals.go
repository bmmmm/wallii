// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// The hook asks; the post measures. The Stop hook scans the session's diff
// for lines that read like a way around a check rather than through it — a
// t.Skip, a gate command with `|| true` behind it, continue-on-error — and
// asks the poster for a --grader sentence about them. Then it deduplicates,
// so it never asks again. If the poster writes nothing, or writes something
// friendly, the finding is gone with the session: the wall would hold only
// the report, from the one source the research says omits things. The same
// doctrine that derives took from the timeline instead of asking for it
// applies here: what the diff showed is a measurement, what --grader says is
// a report, and the wall keeps both.
//
// Nothing is re-derived. The hook leaves what it found in a marker file,
// one finding per line, and SessionSignals reads that file — no second
// scanner, no diff run in the post path. The signals attach to every post of
// the session in that repo, not to the one post that happens to follow the
// finding: they are a property of the session's diff, which every post of
// the session was written on top of. The alternative — consuming the file on
// the first post — would break the hook's own promise that a line already
// answered stays quiet for the rest of the session, because the hook keeps
// its dedup in the same file.
//
// Presence, never content. Nothing here or downstream reads what the
// grader says about a signal, and no ratio is built from the two: a signal
// without a grader is often entirely fine — the hook's environment-guard
// filter is good, not perfect — and nobody owes a counter an explanation.
// stats reports the difference; it does not compute with it.

// SignalHook is the one source a signal can have: the Stop hook's scan of
// the session's diff. It is stored so that an absent field reads as "nobody
// measured" and a source with no signals as "measured, nothing found" — the
// same distinction pulse_src draws, and the reason a source is stored at all.
const SignalHook = "hook"

// MaxSignals caps what a post carries. The rest stays in the marker file:
// the post is a pointer, not a catalogue.
const MaxSignals = 3

// signalsDir is where the Stop hook keeps its markers, under the home
// directory rather than a configurable path: the hook has no config file
// either, and one more environment variable would be one more way for the
// two to disagree about where the file is.
const signalsDir = ".claude/wall-post-reminders"

// SessionSignals reads the shortcut signatures this session's Stop hook
// already recorded for repo — the same marker file, read instead of
// re-derived. values is what the diff showed, `path: line`, at most
// MaxSignals of them and each cut to MaxFieldRunes; src is SignalHook when
// the file exists, even empty, and "" when nobody measured: no session id,
// no file, or a file that could not be read. The last one is deliberate — a
// permission error is not a clean diff, and inventing "measured, nothing
// found" from it would be exactly the lie the source field exists to
// prevent. There is no error return: a post is worth more than the
// telemetry around it, and the only thing an error could change is whether
// the post lands.
func SessionSignals(repo string) (values []string, src string) {
	path := signalsFile(repo)
	if path == "" {
		return nil, ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(values) == MaxSignals {
			break // first lines win, the rest stays in the file
		}
		values = append(values, signalText(line))
	}
	return values, SignalHook
}

// signalsFile names the marker for this session and repo, or "" when there
// is no session to name it by. The session id is the one Claude Code exports
// and the Stop hook receives as session_id, so the two name the same file.
// A path separator in either part would name a file outside the marker
// directory — the hook refuses such an id, and so does this.
func signalsFile(repo string) string {
	sid := strings.TrimSpace(os.Getenv("CLAUDE_CODE_SESSION_ID"))
	if sid == "" || repo == "" || strings.ContainsAny(sid+repo, `/\`) {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, signalsDir, sid+"-"+repo+".shortcut")
}

// signalText renders one marker line, `<path>\t<content>`, as `path:
// content` — a line the hook wrote in some other shape is kept as it is,
// because it is still what the diff showed. Control characters become
// spaces (an indented line inside the content would otherwise fail
// Validate, and losing the signal to its own whitespace is the wrong
// trade), and the cap leaves the ellipsis to say that something was cut.
func signalText(line string) string {
	path, content, found := strings.Cut(line, "\t")
	s := strings.TrimSpace(line)
	if found {
		s = strings.TrimSpace(path) + ": " + strings.TrimSpace(content)
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > MaxFieldRunes {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:MaxFieldRunes-1])) + "…"
	}
	return s
}
