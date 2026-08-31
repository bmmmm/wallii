// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// cmdReact posts a reply to another event: the wall stops being write-only.
// Usage: wallii react [-a actor] [--ref url]... <id> <message>
func cmdReact(args []string) error {
	return replyCmd(wall.KindReact, args)
}

// cmdChallenge doubts a post (open until the challenged actor reacts), or
// lists what is still open:
//
//	wallii challenge [-a actor] [--ref url]... <id> <question>
//	wallii challenge --open [--actor x] [--json]
func cmdChallenge(args []string) error {
	for _, a := range args {
		if a == "--open" || a == "-open" {
			return openChallenges(args)
		}
	}
	return replyCmd(wall.KindChallenge, args)
}

func replyCmd(kind string, args []string) error {
	fs := flag.NewFlagSet(kind, flag.ExitOnError)
	actor := fs.String("a", "", `who replies (default: $WALLII_ACTOR or "manual")`)
	var refs multiFlag
	fs.Var(&refs, "ref", "evidence URL (repeatable)")
	fs.Parse(args)

	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: wallii %s <id> <message> — IDs come from `wallii tail --ids`", kind)
	}
	// same trap as cmdPost: a typo'd flag after the message must not silently
	// become message text
	for _, a := range rest[1:] {
		if flagLike.MatchString(a) {
			return fmt.Errorf("%q comes after the message and would silently join the text — put flags before the ID", a)
		}
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	evs, rstats, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		return err
	}
	reportStats(rstats)
	parent, err := wall.FindByID(evs, rest[0])
	if err != nil {
		return err
	}
	e := wall.Event{
		TS:     time.Now().UTC(),
		Repo:   parent.Repo,
		Actor:  resolveActor(*actor),
		Kind:   kind,
		Parent: parent.ID(),
		Msg:    strings.TrimSpace(strings.Join(rest[1:], " ")),
		Refs:   refs,
	}
	if err := wall.Append(dir, e); err != nil {
		return err
	}
	fmt.Printf("%s %s → %s (%s · %s): %q\n", kind, e.ID(), parent.ID(), parent.Repo, orDash(parent.Actor), excerpt(parent.Msg, 60))
	return nil
}

func openChallenges(args []string) error {
	fs := flag.NewFlagSet("challenge --open", flag.ExitOnError)
	fs.Bool("open", true, "list open challenges")
	actorF := fs.String("actor", "", "filter: challenges against this actor")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)

	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	evs, rstats, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		return err
	}
	reportStats(rstats)
	open := wall.OpenChallenges(evs)
	if *actorF != "" {
		kept := open[:0]
		for _, c := range open {
			if c.HasTarget && strings.EqualFold(c.Target.Actor, *actorF) {
				kept = append(kept, c)
			}
		}
		open = kept
	}
	if *asJSON {
		type row struct {
			ID        string      `json:"id"`
			Challenge wall.Event  `json:"challenge"`
			Target    *wall.Event `json:"target,omitempty"`
		}
		rows := make([]row, 0, len(open))
		for _, c := range open {
			r := row{ID: c.Challenge.ID(), Challenge: c.Challenge}
			if c.HasTarget {
				t := c.Target
				r.Target = &t
			}
			rows = append(rows, r)
		}
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	if len(open) == 0 {
		fmt.Println("no open challenges — doubt something: wallii challenge <id> \"<question>\"")
		return nil
	}
	for _, c := range open {
		target := "(target not on the wall)"
		if c.HasTarget {
			target = fmt.Sprintf("%s·%s %q", c.Target.Repo, orDash(c.Target.Actor), excerpt(c.Target.Msg, 60))
		}
		fmt.Printf("%s  %s challenges %s — %q\n      target: %s\n",
			c.Challenge.ID(), orDash(c.Challenge.Actor), c.Challenge.Parent, c.Challenge.Msg, target)
	}
	fmt.Printf("%d open — answer with: wallii react <challenge-id> \"<answer>\"\n", len(open))
	return nil
}

func excerpt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
