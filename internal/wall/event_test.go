// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"strings"
	"testing"
	"time"
)

func validEvent() Event {
	return Event{
		TS:    time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		Repo:  "example-repo",
		Actor: "test",
		Topic: "ci",
		Msg:   "fixed the flaky test, pushed to main",
		Refs:  []string{"https://git.example.com/x/example-repo/issues/1"},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
}

func TestValidateEmptyRepo(t *testing.T) {
	e := validEvent()
	e.Repo = "  "
	if err := e.Validate(); err == nil {
		t.Fatal("empty repo accepted")
	}
}

func TestValidateEmptyMsg(t *testing.T) {
	e := validEvent()
	e.Msg = ""
	if err := e.Validate(); err == nil {
		t.Fatal("empty message accepted")
	}
}

func TestValidateMultiline(t *testing.T) {
	e := validEvent()
	e.Msg = "line one\nline two"
	if err := e.Validate(); err == nil {
		t.Fatal("multiline message accepted")
	}
}

func TestValidateMsgLengthBoundary(t *testing.T) {
	e := validEvent()
	// multibyte runes prove the cap counts runes, not bytes
	e.Msg = strings.Repeat("ä", MaxMsgRunes)
	if err := e.Validate(); err != nil {
		t.Fatalf("%d-rune message rejected: %v", MaxMsgRunes, err)
	}
	e.Msg = strings.Repeat("ä", MaxMsgRunes+1)
	if err := e.Validate(); err == nil {
		t.Fatalf("%d-rune message accepted", MaxMsgRunes+1)
	}
}

// ANSI/OSC escapes in any field could hijack the terminal rendering the
// wall (review finding #4).
func TestValidateRejectsControlCharacters(t *testing.T) {
	cases := map[string]func(e *Event){
		"msg escape": func(e *Event) { e.Msg = "clear:\x1b[2Jgotcha" },
		"repo osc":   func(e *Event) { e.Repo = "\x1b]0;pwned\ar" },
		"topic bell": func(e *Event) { e.Topic = "ci\a" },
		"actor tab":  func(e *Event) { e.Actor = "a\tb" },
		"ref osc8":   func(e *Event) { e.Refs = []string{"https://x\x1b\\evil"} },
	}
	for name, mut := range cases {
		e := validEvent()
		mut(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// Refs are part of the stored line: uncapped they let one post grow a line
// past any reader bound (review finding #3).
func TestValidateRefLimits(t *testing.T) {
	e := validEvent()
	e.Refs = make([]string, MaxRefs+1)
	for i := range e.Refs {
		e.Refs[i] = "https://git.example.com/x"
	}
	if err := e.Validate(); err == nil {
		t.Error("too many refs accepted")
	}

	e = validEvent()
	e.Refs = []string{"https://git.example.com/" + strings.Repeat("a", MaxRefRunes)}
	if err := e.Validate(); err == nil {
		t.Error("oversize ref accepted")
	}

	e = validEvent()
	e.Refs = []string{"not-a-url"}
	if err := e.Validate(); err == nil {
		t.Error("non-URL ref accepted")
	}
}

func TestValidateFieldLengths(t *testing.T) {
	e := validEvent()
	e.Repo = strings.Repeat("r", MaxFieldRunes+1)
	if err := e.Validate(); err == nil {
		t.Error("oversize repo accepted")
	}
}

// The grader is free text under a cap of its own: control characters would
// break the one-line store, and the cap has to leave air — the shortest
// honest example is already 62 runes, and a reject teaches dropping the
// field. A refusal is as valid a value as a confession.
func TestValidateGraderBoundary(t *testing.T) {
	e := validEvent()
	e.Grader = strings.Repeat("ä", MaxGraderRunes)
	if err := e.Validate(); err != nil {
		t.Fatalf("%d-rune grader rejected: %v", MaxGraderRunes, err)
	}
	e.Grader = strings.Repeat("ä", MaxGraderRunes+1)
	if err := e.Validate(); err == nil {
		t.Fatalf("%d-rune grader accepted", MaxGraderRunes+1)
	}
	e.Grader = "CI wanted green\x1b[2J— loosening the assert would have done"
	if err := e.Validate(); err == nil {
		t.Fatal("grader with an escape sequence accepted")
	}
	e.Grader = "none — the skip guards a missing binary, the assertions are unchanged"
	if err := e.Validate(); err != nil {
		t.Fatalf("a refusal is a complete answer, rejected: %v", err)
	}
}

// Dialogue carries no work telemetry, and the grader is about the work: a
// reply has no cheap path to name.
func TestValidateGraderIsNotDialogue(t *testing.T) {
	for _, kind := range []string{KindReact, KindChallenge} {
		e := validEvent()
		e.Kind, e.Parent = kind, "abcd123"
		if err := e.Validate(); err != nil {
			t.Fatalf("plain %s rejected: %v", kind, err)
		}
		e.Grader = "considered agreeing just to end the thread"
		if err := e.Validate(); err == nil {
			t.Errorf("%s with a grader accepted — grader belongs on posts", kind)
		}
	}
}

// Every stored Parent reference is a hash over TS/Actor/Repo/Msg. A field
// that joined the hash later would move every ID on the wall, so the grader
// must stay out of it — this pins that it does.
func TestIDIgnoresGrader(t *testing.T) {
	a := validEvent()
	b := a
	b.Grader = "loosened nothing, but thought about it for twenty minutes"
	if a.ID() != b.ID() {
		t.Fatalf("grader changed the ID: %s vs %s — every stored parent reference would break", a.ID(), b.ID())
	}
}
