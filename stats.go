// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	sinceS := fs.String("since", "", "window: 2006-01-02, 36h or 3d (default: everything)")
	repoF := fs.String("repo", "", "filter: repo name")
	actorF := fs.String("actor", "", "filter: actor")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.Parse(args)

	since, err := parseSince(*sinceS, time.Now())
	if err != nil {
		return err
	}
	flt := filter{repo: *repoF, actor: *actorF, since: since}

	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	evs, rstats, err := wall.ReadLast(dir, 0, flt.match)
	if err != nil {
		return err
	}
	reportStats(rstats)
	s := wall.Compute(evs)

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(s)
	}
	if s.Posts == 0 {
		fmt.Println("no matching posts — wall dir:", dir)
		return nil
	}

	window := "all time"
	if *sinceS != "" {
		if _, err := time.ParseInLocation("2006-01-02", *sinceS, time.Local); err == nil {
			window = "since " + *sinceS
		} else {
			window = "last " + *sinceS
		}
	}
	fmt.Printf("%d posts · %d repos · %d actors · %s\n", s.Posts, s.Repos, s.Actors, window)

	reported := s.OK + s.Partial + s.Failed
	line := fmt.Sprintf("outcome  ok %d · partial %d · failed %d · unreported %d", s.OK, s.Partial, s.Failed, s.Unreported)
	if reported > 0 {
		line += fmt.Sprintf(" — %d%% of reported landed", pct(s.OK, reported))
	}
	fmt.Println(line)
	if s.MoodCount > 0 {
		fmt.Printf("mood     %s (%.1f) from %d posts — %s\n", moodWord(s.MoodAvg), s.MoodAvg, s.MoodCount, moodSpread(s.ByMood))
	}
	if calib := calibLine(s, *sinceS); calib != "" {
		fmt.Println(calib)
	}
	if s.TookCount > 0 {
		took := fmt.Sprintf("took     %s logged across %d posts", fmtTook(s.TookTotalS), s.TookCount)
		if s.TookAuto > 0 {
			took += fmt.Sprintf(" (%d derived, %d measured)", s.TookAuto, s.TookCount-s.TookAuto)
		}
		fmt.Println(took)
	}
	fmt.Printf("refs     %d/%d posts carry a ref (%d%%)\n", s.WithRefs, s.Posts, pct(s.WithRefs, s.Posts))
	// A wall with zero dialogue is a wall nobody reads — say so instead of
	// hiding an empty line.
	if s.Reactions > 0 || s.Challenges > 0 {
		line := fmt.Sprintf("dialog   %d reaction(s) · %d challenge(s)", s.Reactions, s.Challenges)
		if s.Challenges > 0 {
			line += fmt.Sprintf(" (%d open)", s.ChallengesOpen)
		}
		if len(s.ByChallenged) > 0 {
			line += fmt.Sprintf(" — most challenged: %s (%d)", orDash(s.ByChallenged[0].Name), s.ByChallenged[0].Count)
		}
		fmt.Println(line)
	} else {
		fmt.Println("dialog   none — nobody answered anyone; react with: wallii tail --ids, then wallii react <id> \"…\"")
	}
	// The population's mirror: who leans on which word, who always opens the
	// same way. Convergence here is monoculture even when every grade is fine.
	for i, v := range s.Voice {
		if i == 4 {
			break
		}
		label := "voice"
		if i > 0 {
			label = ""
		}
		fmt.Printf("%-8s %s: favorite %q ×%d · %d%% open with %q · %d distinct words\n",
			label, orDash(v.Actor), v.FavWord, v.FavCount, v.OpeningPct, v.Opening, v.Distinct)
	}
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ACTOR\tPOSTS\tREPOS\tLANDED\tMOOD\tREFS")
	for _, a := range s.ByActor {
		landed, mood := "—", "—"
		if rep := a.OK + a.Partial + a.Failed; rep > 0 {
			landed = fmt.Sprintf("%d%%", pct(a.OK, rep))
		}
		if a.MoodCount > 0 {
			mood = fmt.Sprintf("%.1f", a.MoodAvg)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%d%%\n", orDash(a.Actor), a.Posts, a.Repos, landed, mood, pct(a.WithRefs, a.Posts))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Println()
	for i, r := range s.ByRepo {
		if i == 8 {
			fmt.Printf("… +%d more repos\n", len(s.ByRepo)-8)
			break
		}
		fmt.Printf("%-24s %s %d\n", r.Name, bar(r.Count, s.ByRepo[0].Count, 20), r.Count)
	}
	return nil
}

// moodSpread lists the mood distribution in scale order. The average alone
// hides the shape: 4.2 reads the same whether the wall used every value or
// only ever said good.
func moodSpread(by []wall.NameCount) string {
	parts := make([]string, 0, len(by))
	for _, m := range by {
		parts = append(parts, fmt.Sprintf("%s %d", m.Name, m.Count))
	}
	return strings.Join(parts, " · ")
}

// calibLine asks the question the landed-% above it cannot: does this wall
// have a way to carry bad news? Counting distinct values is not enough —
// both scales have a direction, and one that never points down is a habit
// rather than a measurement. Silent once both ends actually occur.
// sinceS is echoed into the follow-up command so the listing covers exactly
// the window the numbers came from — a hint that silently widens the range is
// worse than none.
func calibLine(s wall.Stats, sinceS string) string {
	outUsed := 0
	for _, n := range []int{s.OK, s.Partial, s.Failed} {
		if n > 0 {
			outUsed++
		}
	}
	if outUsed == 0 && len(s.ByMood) == 0 {
		return ""
	}
	var gaps []string
	if outUsed > 0 && s.Failed == 0 {
		gaps = append(gaps, "nothing ever failed")
	}
	lowMood := false
	for _, m := range s.ByMood {
		if wall.MoodScore(m.Name) <= 2 { // rough, stuck
			lowMood = true
		}
	}
	if len(s.ByMood) > 0 && !lowMood {
		gaps = append(gaps, "mood never went below ok")
	}
	line := fmt.Sprintf("calib    outcome %d of 3 values · mood %d of %d", outUsed, len(s.ByMood), len(wall.Moods))
	if len(gaps) > 0 {
		line += " — " + strings.Join(gaps, ", ") +
			"\n         a scale that never points down measures nothing; check the posts, not the ratio"
	} else {
		line += " — both scales reach their low end"
	}
	// Where the messages already say it and only the grade disagrees, the
	// wall is more honest than its own numbers — worth naming, since nothing
	// stops such a post from being written.
	// Naming a count and then offering no way to reach it is an instruction
	// nobody can follow — the flag is half the message.
	if s.Contradicting > 0 {
		sinceHint := ""
		if sinceS != "" {
			sinceHint = " --since " + sinceS
		}
		line += fmt.Sprintf("\n         %d post(s) tell a rougher story than their grade — they are the honest ones:"+
			"\n         wallii tail --contradicting%s -n 0", s.Contradicting, sinceHint)
	}
	return line
}

func pct(part, whole int) int {
	if whole == 0 {
		return 0
	}
	return int(float64(part)/float64(whole)*100 + 0.5)
}

// moodIndex rounds an average onto an index into wall.Moods (0 great …
// 4 stuck) on the same 5..1 scale MoodScore assigns: ≥4.5 great, ≥3.5 good,
// ≥2.5 ok, ≥1.5 rough, else stuck. Both the word and the mood panel's face
// pick their value with it, so they can never disagree.
func moodIndex(avg float64) int {
	idx := len(wall.Moods) - int(avg+0.5)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(wall.Moods) {
		idx = len(wall.Moods) - 1
	}
	return idx
}

// moodWord names the average.
func moodWord(avg float64) string { return wall.Moods[moodIndex(avg)] }

// fmtTook rounds to whole minutes first, then splits — the same math as the
// dashboard's fmtTook, so terminal and browser never disagree on a duration.
func fmtTook(sec int64) string {
	m := (sec + 30) / 60
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", m/60, m%60)
}

func bar(v, max, width int) string {
	if max <= 0 {
		return ""
	}
	n := v * width / max
	if n == 0 && v > 0 {
		n = 1
	}
	out := make([]rune, n)
	for i := range out {
		out[i] = '█'
	}
	return string(out)
}
