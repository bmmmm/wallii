// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"strings"
	"testing"
	"time"
)

func clonePosts(n int, msg string) []Event {
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Event{TS: ts.Add(time.Duration(i) * time.Hour),
			Repo: "demo", Actor: "bot/clone", Msg: msg})
	}
	return out
}

// Red proof: eight near-identical posts must trip the note.
func TestSamenessFiresOnClones(t *testing.T) {
	prior := clonePosts(7, "gates green: module aligned, tests pinned, coverage stable")
	e := clonePosts(1, "gates green: module aligned, tests pinned, coverage happy")[0]
	got := Sameness(prior, e)
	if got == "" {
		t.Fatal("eight clone posts must trip the sameness note")
	}
	if !strings.Contains(got, "sameness") {
		t.Fatalf("note must name itself, got %q", got)
	}
}

func TestSamenessStaysQuietOnVariedPosts(t *testing.T) {
	msgs := []string{
		"obituary for the sshfs route: macFUSE never came back",
		"queue drained after the retry fix, nine workers idle again",
		"docs now explain the rollback dance instead of pretending",
		"third attempt landed: parser survives empty headers",
		"release cut, changelog honest about the skipped migration",
		"benchmarks say the cache buys nothing, removed it",
		"login flow rewired to tokens, sessions stay put",
	}
	ts := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	var prior []Event
	for i, m := range msgs {
		prior = append(prior, Event{TS: ts.Add(time.Duration(i) * time.Hour), Repo: "demo", Actor: "bot/varied", Msg: m})
	}
	e := Event{TS: ts.Add(8 * time.Hour), Repo: "demo", Actor: "bot/varied",
		Msg: "wal segment pruning was eating the disk, capped it"}
	if got := Sameness(prior, e); got != "" {
		t.Fatalf("varied posts must not trip the note, got %q", got)
	}
}

func TestSamenessNeedsAFullWindow(t *testing.T) {
	prior := clonePosts(3, "gates green: module aligned, tests pinned")
	e := clonePosts(1, "gates green: module aligned, tests pinned")[0]
	if got := Sameness(prior, e); got != "" {
		t.Fatalf("three posts are no habit yet, got %q", got)
	}
}

func TestSamenessSkipsDialogueInTheWindow(t *testing.T) {
	// seven clones + a pile of reacts: the reacts must not push the clones
	// out of the window
	prior := clonePosts(7, "gates green: module aligned, tests pinned, coverage stable")
	for i := 0; i < 5; i++ {
		prior = append(prior, Event{TS: prior[len(prior)-1].TS.Add(time.Minute),
			Repo: "demo", Actor: "bot/clone", Kind: KindReact, Parent: "abcd123", Msg: "noted"})
	}
	e := clonePosts(1, "gates green: module aligned, tests pinned, coverage happy")[0]
	if Sameness(prior, e) == "" {
		t.Fatal("reacts between clone posts must not reset the sameness window")
	}
}

func TestVoicesFingerprint(t *testing.T) {
	var evs []Event
	evs = append(evs, clonePosts(9, "gates green: pipeline aligned again")...)
	// an actor below the window threshold must not appear
	evs = append(evs, Event{TS: evs[0].TS, Repo: "demo", Actor: "bot/rare", Msg: "one lonely post"})

	vs := Voices(evs)
	if len(vs) != 1 || vs[0].Actor != "bot/clone" {
		t.Fatalf("want exactly bot/clone, got %+v", vs)
	}
	v := vs[0]
	if v.FavWord != "aligned" && v.FavWord != "gates" && v.FavWord != "green" && v.FavWord != "pipeline" && v.FavWord != "again" {
		t.Fatalf("favorite word must come from the posts, got %q", v.FavWord)
	}
	if v.FavCount != 9 {
		t.Fatalf("favorite word appears once per post ×9, got %d", v.FavCount)
	}
	if v.Opening != "gates" || v.OpeningPct != 100 {
		t.Fatalf("every post opens with gates, got %q %d%%", v.Opening, v.OpeningPct)
	}
}

func TestTokenizeHandlesGermanAndPunctuation(t *testing.T) {
	got := tokenize("Sackgasse! über-Möglichkeit, 12 von 13 (ok)")
	want := map[string]bool{"sackgasse": true, "über": true, "möglichkeit": true, "von": true}
	for _, tk := range got {
		if !want[tk] {
			t.Fatalf("unexpected token %q in %v", tk, got)
		}
		delete(want, tk)
	}
	if len(want) > 0 {
		t.Fatalf("missing tokens %v from %v", want, got)
	}
}
