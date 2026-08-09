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
