// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"crypto/sha256"
	"encoding/hex"
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
// React and challenge events answer another event (Parent holds its ID): a
// react is any reply, a challenge doubts a post and stays open until the
// challenged actor reacts. Both are dialogue, not work — stats and the
// post-time lints skip them like the registry events.
const (
	KindAttach    = "attach"
	KindDetach    = "detach"
	KindReact     = "react"
	KindChallenge = "challenge"
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

// Pulse sources: where a post's API round trip came from. PulseProbe is
// wallii's own measurement at post time, PulseSession a number the harness
// handed over — it knows what its turns actually cost, which an unauthenticated
// GET can only approximate — and PulseNone says the API was asked and did not
// answer. The last one is the point of storing a source at all: an absent
// field means nobody measured, and that must never read as an outage.
const (
	PulseProbe   = "probe"
	PulseSession = "session"
	PulseNone    = "none"
)

// MaxPulseMS bounds a stored round trip at an hour. Past that it is a typo or
// a stopped clock, not a wait anybody sat through.
const MaxPulseMS = 3600_000

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
	Parent  string    `json:"parent,omitempty"`  // ID of the event a react/challenge answers
	Persona string    `json:"persona,omitempty"` // attach only: a voice line for the pair ("the grumbler")
	Msg     string    `json:"msg"`
	Refs    []string  `json:"refs,omitempty"`
	Outcome string    `json:"outcome,omitempty"` // ok | partial | failed
	TookS   int64     `json:"took_s,omitempty"`  // wall-clock duration of the work, seconds
	TookSrc string    `json:"took_src,omitempty"`
	Mood    string    `json:"mood,omitempty"` // great | good | ok | rough | stuck
	// PulseMS is what the API answered in while this post was written — the
	// working conditions the grade above it was earned under. PulseSrc says
	// who measured it, and carries PulseNone when nothing answered at all.
	PulseMS  int64  `json:"pulse_ms,omitempty"`
	PulseSrc string `json:"pulse_src,omitempty"`
}

// ID derives a short stable address for an event from fields that never
// change after Append. Derived, not stored: the NDJSON lines stay exactly
// what they were, and every reader computes the same handle. Seven hex chars
// keep it typeable; FindByID accepts unique prefixes anyway.
func (e Event) ID() string {
	h := sha256.Sum256([]byte(e.TS.UTC().Format(time.RFC3339Nano) + "\x00" + e.Actor + "\x00" + e.Repo + "\x00" + e.Msg))
	return hex.EncodeToString(h[:])[:7]
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
	if e.Persona != "" {
		if e.Kind != KindAttach {
			return fmt.Errorf("persona is set with `wallii attach --persona`, not on %s events", orPost(e.Kind))
		}
		if hasControl(e.Persona) {
			return errors.New("persona contains control characters — plain text only")
		}
		if n := utf8.RuneCountInString(e.Persona); n > MaxFieldRunes {
			return fmt.Errorf("persona is %d runes, max %d — it is a voice line, not a biography", n, MaxFieldRunes)
		}
	}
	switch e.Kind {
	case "", KindAttach, KindDetach:
		if e.Parent != "" {
			return fmt.Errorf("parent is only valid on react/challenge events, not %q", orPost(e.Kind))
		}
	case KindReact, KindChallenge:
		if e.Parent == "" {
			return fmt.Errorf("a %s needs a parent event ID — pick one from `wallii tail --ids`", e.Kind)
		}
		if len(e.Parent) < 4 || len(e.Parent) > 64 || strings.TrimLeft(e.Parent, "0123456789abcdef") != "" {
			return fmt.Errorf("parent %q is not an event ID (lowercase hex, ≥4 chars)", e.Parent)
		}
		// dialogue carries no work telemetry: a grade on a reply would leak
		// into nothing (stats skips kinds) and only invite confusion
		if e.Outcome != "" || e.Mood != "" || e.TookS != 0 || e.PulseSrc != "" {
			return fmt.Errorf("a %s is dialogue — outcome/mood/took/pulse belong on posts", e.Kind)
		}
	default:
		return fmt.Errorf("unknown kind %q — one of attach, detach, react, challenge, or empty", e.Kind)
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
	switch e.PulseSrc {
	case "", PulseProbe, PulseSession, PulseNone:
	default:
		return fmt.Errorf("unknown pulse_src %q — one of %s, %s, %s, or empty (nobody measured)", e.PulseSrc, PulseProbe, PulseSession, PulseNone)
	}
	if e.PulseMS < 0 {
		return fmt.Errorf("pulse is negative (%dms) — round trips only", e.PulseMS)
	}
	if e.PulseMS > MaxPulseMS {
		return fmt.Errorf("pulse is %dms, over an hour — that is a stopped clock, not a wait", e.PulseMS)
	}
	// The reverse (a source with no value) is legal: PulseNone is a reading
	// whose value is that there was none.
	if e.PulseMS != 0 && e.PulseSrc == "" {
		return errors.New("pulse_ms is set without a source — a round trip nobody claims cannot be told from a guess")
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

func orPost(kind string) string {
	if kind == "" {
		return "a post"
	}
	return kind
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
