// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Field caps keep every entry one-line scannable and bound the size of a
// stored line; enforced at post time so brevity is a store invariant, not a
// prompt convention.
const (
	MaxMsgRunes   = 140
	MaxFieldRunes = 64 // repo, topic, actor
	MaxRefs       = 8
	MaxRefRunes   = 512
)

// Registration kinds: an empty Kind is a regular post; attach/detach events
// drive the registry view (Attachments) but live in the same append-only log.
const (
	KindAttach = "attach"
	KindDetach = "detach"
)

// Outcome values: did the reported work land? Matching the fix-loop STATUS
// vocabulary keeps agent reports and wall telemetry one language.
const (
	OutcomeOK      = "ok"
	OutcomePartial = "partial"
	OutcomeFailed  = "failed"
)

// TookAuto marks a duration wallii derived itself (time since the actor's
// previous post or session start) instead of one the poster measured. Every
// took value on the wall before this existed was rounded to five minutes —
// guesses, all of them. Keeping the source on the event lets stats and dash
// separate derived from measured rather than averaging both into one number
// nobody can check.
const TookAuto = "auto"

// Mood values, best → worst. MoodScore maps them onto 5..1 so trends can be
// averaged; unknown or absent moods score 0 (excluded from averages).
var Moods = []string{"great", "good", "ok", "rough", "stuck"}

func MoodScore(mood string) int {
	for i, m := range Moods {
		if m == mood {
			return len(Moods) - i
		}
	}
	return 0
}

type Event struct {
	TS      time.Time `json:"ts"`
	Repo    string    `json:"repo"`
	Actor   string    `json:"actor,omitempty"`
	Topic   string    `json:"topic,omitempty"`
	Kind    string    `json:"kind,omitempty"`
	Msg     string    `json:"msg"`
	Refs    []string  `json:"refs,omitempty"`
	Outcome string    `json:"outcome,omitempty"` // ok | partial | failed
	TookS   int64     `json:"took_s,omitempty"`  // wall-clock duration of the work, seconds
	TookSrc string    `json:"took_src,omitempty"`
	Mood    string    `json:"mood,omitempty"` // great | good | ok | rough | stuck
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.Repo) == "" {
		return errors.New("repo is empty — run inside a git repo or pass -r <name>")
	}
	for name, v := range map[string]string{"repo": e.Repo, "topic": e.Topic, "actor": e.Actor} {
		if hasControl(v) {
			return fmt.Errorf("%s contains control characters — plain text only", name)
		}
		if n := utf8.RuneCountInString(v); n > MaxFieldRunes {
			return fmt.Errorf("%s is %d runes, max %d", name, n, MaxFieldRunes)
		}
	}
	switch e.Kind {
	case "", KindAttach, KindDetach:
	default:
		return fmt.Errorf("unknown kind %q — one of attach, detach, or empty", e.Kind)
	}
	switch e.Outcome {
	case "", OutcomeOK, OutcomePartial, OutcomeFailed:
	default:
		return fmt.Errorf("unknown outcome %q — one of ok, partial, failed", e.Outcome)
	}
	if e.Mood != "" && MoodScore(e.Mood) == 0 {
		return fmt.Errorf("unknown mood %q — one of %s", e.Mood, strings.Join(Moods, ", "))
	}
	if e.TookS < 0 {
		return fmt.Errorf("took is negative (%ds) — durations only", e.TookS)
	}
	if e.TookS > 366*24*3600 {
		return fmt.Errorf("took is %ds, over a year — that is a typo, not a work item", e.TookS)
	}
	if e.TookSrc != "" && e.TookSrc != TookAuto {
		return fmt.Errorf("unknown took_src %q — %q or empty (measured)", e.TookSrc, TookAuto)
	}
	if e.TookSrc != "" && e.TookS == 0 {
		return errors.New("took_src is set without a duration — a source without a value says nothing")
	}
	if strings.TrimSpace(e.Msg) == "" {
		return errors.New("message is empty")
	}
	if hasControl(e.Msg) {
		return errors.New("message must be a single line of plain text (no newlines or control characters) — move detail into --ref")
	}
	if n := utf8.RuneCountInString(e.Msg); n > MaxMsgRunes {
		return fmt.Errorf("message is %d runes, max %d — shorten it or move detail into --ref", n, MaxMsgRunes)
	}
	if len(e.Refs) > MaxRefs {
		return fmt.Errorf("%d refs, max %d — link an overview page instead", len(e.Refs), MaxRefs)
	}
	for _, r := range e.Refs {
		if !strings.HasPrefix(r, "https://") && !strings.HasPrefix(r, "http://") {
			return fmt.Errorf("ref %q is not an http(s) URL", r)
		}
		if hasControl(r) || strings.ContainsRune(r, ' ') {
			return fmt.Errorf("ref %q contains spaces or control characters", r)
		}
		if n := utf8.RuneCountInString(r); n > MaxRefRunes {
			return fmt.Errorf("ref is %d runes, max %d", n, MaxRefRunes)
		}
	}
	return nil
}

// hasControl reports C0 control characters or DEL — they would corrupt the
// one-line NDJSON invariant or smuggle ANSI/OSC sequences into terminals
// that render the wall.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
