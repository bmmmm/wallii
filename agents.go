// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

func cmdAgents(args []string) error {
	fs := flag.NewFlagSet("agents", flag.ExitOnError)
	staleS := fs.String("stale", "7d", "flag attached pairs with no post for this long (36h, 7d)")
	repoF := fs.String("repo", "", "filter: repo name")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)

	stale, err := parseDur(*staleS)
	if err != nil {
		return err
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	evs, stats, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		return err
	}
	reportStats(stats)

	pairs := wall.Attachments(evs)
	if *repoF != "" {
		kept := pairs[:0]
		for _, p := range pairs {
			if strings.EqualFold(p.Repo, *repoF) {
				kept = append(kept, p)
			}
		}
		pairs = kept
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(pairs)
	}
	if len(pairs) == 0 {
		fmt.Println("no agents on the wall yet — pairs appear with their first post or `wallii attach`")
		return nil
	}

	now := time.Now()
	actors, families, repos := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	silent := 0
	for _, p := range pairs {
		actors[p.Actor] = struct{}{}
		families[p.Family] = struct{}{}
		repos[p.Repo] = struct{}{}
		if s := pairState(p, now, stale); strings.HasPrefix(s, "silent") || strings.Contains(s, "never posted") {
			silent++
		}
	}
	fmt.Printf("%d agents in %d families · %d repos · %d pairs · %d need attention\n\n", len(actors), len(families), len(repos), len(pairs), silent)

	// grouped by family, then actor, then repo — an actor sort alone would
	// split a family whose members use different separators (cron/x, cron:z)
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].Family != pairs[j].Family {
			return pairs[i].Family < pairs[j].Family
		}
		if pairs[i].Actor != pairs[j].Actor {
			return pairs[i].Actor < pairs[j].Actor
		}
		return pairs[i].Repo < pairs[j].Repo
	})
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FAMILY\tACTOR\tREPO\tPOSTS\tLAST POST\tSTATE\tPERSONA")
	for _, p := range pairs {
		last := "—"
		if !p.LastPost.IsZero() {
			last = ago(now.Sub(p.LastPost))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n", orDash(p.Family), orDash(p.Actor), p.Repo, p.Posts, last, pairState(p, now, stale), p.Persona)
	}
	return w.Flush()
}

func pairState(p wall.PairState, now time.Time, stale time.Duration) string {
	switch {
	case !p.Attached:
		return "detached " + ago(now.Sub(p.StateAt))
	case p.Posts == 0:
		return "attached " + ago(now.Sub(p.StateAt)) + ", never posted"
	case now.Sub(p.LastPost) > stale:
		return "silent " + ago(now.Sub(p.LastPost))
	default:
		return "active"
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func parseDur(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		if days, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64); err == nil && days >= 0 {
			return time.Duration(days * 24 * float64(time.Hour)), nil
		}
	} else if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return d, nil
	}
	return 0, fmt.Errorf("cannot parse duration %q — use 36h or 7d", s)
}

func cmdAttach(args []string) error { return registerCmd(args, wall.KindAttach) }
func cmdDetach(args []string) error { return registerCmd(args, wall.KindDetach) }

// registerCmd posts an attach/detach event — registration lives in the same
// append-only log as everything else. Idempotent: a pair already in the
// target state posts nothing — unless attach carries a new persona, which
// is worth a line of its own.
func registerCmd(args []string, kind string) error {
	fs := flag.NewFlagSet(kind, flag.ExitOnError)
	repo := fs.String("r", "", "repo name (default: current git repo)")
	actor := fs.String("a", "", `who registers (default: $WALLII_ACTOR or "manual")`)
	persona := ""
	if kind == wall.KindAttach {
		fs.StringVar(&persona, "persona", "", `voice line rendered next to the pair ("the grumbler")`)
	}
	fs.Parse(args)

	msg := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if msg == "" {
		msg = kind + "ed"
	}
	if *repo == "" {
		*repo = gitRepoName()
	}
	who := resolveActor(*actor)

	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	evs, _, err := wall.ReadLast(dir, 0, nil)
	if err != nil {
		return err
	}
	want := kind == wall.KindAttach
	for _, p := range wall.Attachments(evs) {
		if p.Actor == who && strings.EqualFold(p.Repo, *repo) && p.Attached == want {
			// a fresh persona is a state change even on an attached pair
			if persona == "" || persona == p.Persona {
				fmt.Printf("%s ↔ %s is already %sed — nothing posted\n", who, p.Repo, kind)
				return nil
			}
		}
	}
	e := wall.Event{TS: time.Now().UTC(), Repo: *repo, Actor: who, Topic: "wall", Kind: kind, Msg: msg, Persona: persona}
	if err := wall.Append(dir, e); err != nil {
		return err
	}
	fmt.Printf("%sed: %s ↔ %s\n", kind, who, *repo)
	return nil
}
