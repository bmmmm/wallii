// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"testing"
	"time"
)

func at(hour int) time.Time {
	return time.Date(2026, 8, 9, hour, 0, 0, 0, time.UTC)
}

func TestAttachmentsImplicitViaPost(t *testing.T) {
	evs := []Event{
		{TS: at(1), Actor: "bot", Repo: "example-repo", Msg: "one"},
		{TS: at(2), Actor: "bot", Repo: "example-repo", Msg: "two"},
	}
	got := Attachments(evs)
	if len(got) != 1 {
		t.Fatalf("want 1 pair, got %d", len(got))
	}
	p := got[0]
	if !p.Attached || p.Explicit || p.Posts != 2 {
		t.Fatalf("implicit attach broken: %+v", p)
	}
	if !p.FirstPost.Equal(at(1)) || !p.LastPost.Equal(at(2)) {
		t.Fatalf("post timestamps wrong: %+v", p)
	}
}

func TestAttachmentsExplicitNeverPosted(t *testing.T) {
	evs := []Event{
		{TS: at(1), Actor: "new-bot", Repo: "example-repo", Kind: KindAttach, Msg: "attached"},
	}
	p := Attachments(evs)[0]
	if !p.Attached || !p.Explicit || p.Posts != 0 {
		t.Fatalf("explicit attach broken: %+v", p)
	}
}

func TestAttachmentsDetachAndReattachByPosting(t *testing.T) {
	evs := []Event{
		{TS: at(1), Actor: "bot", Repo: "example-repo", Msg: "work"},
		{TS: at(2), Actor: "bot", Repo: "example-repo", Kind: KindDetach, Msg: "detached"},
	}
	p := Attachments(evs)[0]
	if p.Attached || !p.StateAt.Equal(at(2)) {
		t.Fatalf("detach broken: %+v", p)
	}
	// whoever posts is back
	evs = append(evs, Event{TS: at(3), Actor: "bot", Repo: "example-repo", Msg: "more work"})
	p = Attachments(evs)[0]
	if !p.Attached || p.Posts != 2 || !p.StateAt.Equal(at(3)) {
		t.Fatalf("re-attach by posting broken: %+v", p)
	}
}

// Dialogue is not work and not a registration: a challenge from a critic
// must not put the critic on the wall, a react must not count as a post or
// move the last-post clock, and a react after a detach does not re-attach.
// Nothing asserted this before, and every reply quietly fell through to
// the post branch.
func TestAttachmentsIgnoreDialogue(t *testing.T) {
	post := Event{TS: at(1), Actor: "bot", Repo: "example-repo", Msg: "work"}
	evs := []Event{
		post,
		{TS: at(2), Actor: "critic", Repo: "example-repo", Kind: KindChallenge, Parent: post.ID(), Msg: "which gate ran?"},
		{TS: at(3), Actor: "bot", Repo: "example-repo", Kind: KindReact, Parent: post.ID(), Msg: "gate 3"},
	}
	got := Attachments(evs)
	if len(got) != 1 || got[0].Actor != "bot" {
		t.Fatalf("a challenge must not attach the critic, got %+v", got)
	}
	if p := got[0]; p.Posts != 1 || !p.LastPost.Equal(at(1)) {
		t.Fatalf("a react must not count as a post or move the clock: %+v", p)
	}
	evs = append(evs,
		Event{TS: at(4), Actor: "bot", Repo: "example-repo", Kind: KindDetach, Msg: "detached"},
		Event{TS: at(5), Actor: "bot", Repo: "example-repo", Kind: KindReact, Parent: post.ID(), Msg: "one more word"},
	)
	if p := Attachments(evs)[0]; p.Attached || !p.StateAt.Equal(at(4)) {
		t.Fatalf("a react after a detach must not re-attach: %+v", p)
	}
}

func TestAttachmentsSortedPairs(t *testing.T) {
	evs := []Event{
		{TS: at(1), Actor: "z-bot", Repo: "a-repo", Msg: "x"},
		{TS: at(2), Actor: "a-bot", Repo: "b-repo", Msg: "x"},
		{TS: at(3), Actor: "a-bot", Repo: "a-repo", Msg: "x"},
	}
	got := Attachments(evs)
	if len(got) != 3 {
		t.Fatalf("want 3 pairs, got %d", len(got))
	}
	if got[0].Actor != "a-bot" || got[0].Repo != "a-repo" || got[2].Actor != "z-bot" {
		t.Fatalf("sort order wrong: %+v", got)
	}
}
