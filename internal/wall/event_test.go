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
