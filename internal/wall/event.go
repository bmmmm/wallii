// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxMsgRunes keeps every entry one-line scannable; enforced at post time so
// brevity is a store invariant, not a prompt convention.
const MaxMsgRunes = 140

type Event struct {
	TS    time.Time `json:"ts"`
	Repo  string    `json:"repo"`
	Actor string    `json:"actor,omitempty"`
	Topic string    `json:"topic,omitempty"`
	Msg   string    `json:"msg"`
	Refs  []string  `json:"refs,omitempty"`
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.Repo) == "" {
		return errors.New("repo is empty — run inside a git repo or pass -r <name>")
	}
	if strings.TrimSpace(e.Msg) == "" {
		return errors.New("message is empty")
	}
	if strings.ContainsAny(e.Msg, "\r\n") {
		return errors.New("message must be a single line — move detail into --ref")
	}
	if n := utf8.RuneCountInString(e.Msg); n > MaxMsgRunes {
		return fmt.Errorf("message is %d runes, max %d — shorten it or move detail into --ref", n, MaxMsgRunes)
	}
	return nil
}
