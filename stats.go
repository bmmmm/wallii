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
	actorF := fs.String("actor", "", "filter: actor, or a family (claude, codex) — the part before / or :")
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
	fmt.Printf("%d posts · %d repos · %d actors in %d families · %s\n", s.Posts, s.Repos, s.Actors, s.Families, window)

	reported := s.OK + s.Partial + s.Failed
	line := fmt.Sprintf("outcome  ok %d · partial %d · failed %d · unreported %d", s.OK, s.Partial, s.Failed, s.Unreported)
	if reported > 0 {
		line += fmt.Sprintf(" — %d%% of reported landed", pct(s.OK, reported))
	}
	fmt.Println(line)
	if s.MoodCount > 0 {
		fmt.Printf("mood     %s (%.1f) from %d posts — %s\n", moodWord(s.MoodAvg), s.MoodAvg, s.MoodCount, moodSpread(s.ByMood))
	}
	if line := apiLine(s); line != "" {
		fmt.Println(line)
	}
	if line := squeezeLine(s); line != "" {
		fmt.Println(line)
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
	fmt.Println(graderLine(s))
	if line := signalsLine(s); line != "" {
		fmt.Println(line)
	}
	for _, line := range dialogLines(s) {
		fmt.Println(line)
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

	// Families — claude against codex against cron — only when there is
	// more than one to compare; a single family would only repeat itself.
	// What lands and how it feels may be compared here; the coverage ratio
	// is never split this way.
	if len(s.ByFamily) > 1 {
		for i, v := range s.VoiceFamily {
			if i == 4 {
				break
			}
			label := "family"
			if i > 0 {
				label = ""
			}
			fmt.Printf("%-8s %s: favorite %q ×%d · %d%% open with %q · %d distinct words\n",
				label, orDash(v.Actor), v.FavWord, v.FavCount, v.OpeningPct, v.Opening, v.Distinct)
		}
		if len(s.VoiceFamily) > 0 {
			fmt.Println()
		}
		fw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(fw, "FAMILY\tACTORS\tPOSTS\tREPOS\tLANDED\tMOOD\tREFS")
		for _, f := range s.ByFamily {
			landed, mood := "—", "—"
			if rep := f.OK + f.Partial + f.Failed; rep > 0 {
				landed = fmt.Sprintf("%d%%", pct(f.OK, rep))
			}
			if f.MoodCount > 0 {
				mood = fmt.Sprintf("%.1f", f.MoodAvg)
			}
			fmt.Fprintf(fw, "%s\t%d\t%d\t%d\t%s\t%s\t%d%%\n", orDash(f.Family), f.Actors, f.Posts, f.Repos, landed, mood, pct(f.WithRefs, f.Posts))
		}
		if err := fw.Flush(); err != nil {
			return err
		}
		fmt.Println()
	}

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

// dialogLines renders the dialog line — or two. A wall with zero dialogue is
// a wall nobody reads, and the line says so instead of hiding. The lint's
// challenges are counted apart: they are the wall talking to itself, and a
// window where only the lint spoke still gets the nudge, not the credit —
// otherwise the auto-challenge would silence the very line that says nobody
// answers anyone, by answering nobody.
func dialogLines(s wall.Stats) []string {
	var out []string
	if s.Reactions > 0 || s.Challenges > 0 {
		line := fmt.Sprintf("dialog   %d reaction(s) · %d challenge(s)", s.Reactions, s.Challenges)
		if s.Challenges > 0 {
			line += fmt.Sprintf(" (%d open", s.ChallengesOpen)
			if s.ChallengesAuto > 0 {
				line += fmt.Sprintf(" · %d raised by the lint, %d by an agent", s.ChallengesAuto, s.Challenges-s.ChallengesAuto)
			}
			line += ")"
		}
		if len(s.ByChallenged) > 0 {
			line += fmt.Sprintf(" — most challenged: %s (%d)", orDash(s.ByChallenged[0].Name), s.ByChallenged[0].Count)
		}
		out = append(out, line)
	}
	if s.Reactions == 0 && s.Challenges-s.ChallengesAuto == 0 {
		out = append(out, "dialog   none — nobody answered anyone; react with: wallii tail --ids, then wallii react <id> \"…\"")
	}
	return out
}

// apiLine reports the conditions the grades in this window were earned under:
// what the API typically answered in, how much that takes off the scale, and
// how many posts were written while it answered nothing at all. Silent when
// no post in the window carries a reading — most of the wall predates the
// field, and a coverage of zero is not a fast API.
func apiLine(s wall.Stats) string {
	if s.PulseTurns+s.PulsePings+s.PulseDown == 0 {
		return ""
	}
	line := "api      "
	switch {
	case s.PulseTurns > 0:
		avg := time.Duration(s.PulseTurnTotalMS/int64(s.PulseTurns)) * time.Millisecond
		line += fmt.Sprintf("%s per turn across %s", pulseDur(avg), plural(s.PulseTurns, "post"))
		if drag := wall.PulseDrag(avg); drag > 0 {
			line += fmt.Sprintf(" — that pace takes %.1f off a mood", drag)
		}
	case s.PulsePings > 0:
		// reachability is not response time, and a line that let the two look
		// alike is the reason this counter exists separately at all
		line += fmt.Sprintf("reachable on %s, but no turn time was measured", plural(s.PulsePings, "post"))
	default:
		line += fmt.Sprintf("nothing answered across %s", plural(s.PulseDown, "post"))
	}
	if s.PulseDown > 0 && s.PulseTurns+s.PulsePings > 0 {
		line += fmt.Sprintf(" · %d written with no api at all", s.PulseDown)
	}
	return line
}

// squeezeLine reports the other half of those conditions: how much of the
// account's five-hour and seven-day budget was already spent while these
// grades were earned. Silent when no post in the window carries a reading,
// for the same reason the api line is — zero coverage is not an empty budget.
//
// It reports and stops there. No coverage percentage, no verdict, and above
// all no arithmetic against the mood line above it: whether a full budget
// makes for a worse day is the question these numbers are being stored to
// answer, and a line that already assumed the answer would be the fastest
// possible way to lose it.
func squeezeLine(s wall.Stats) string {
	if s.SqueezePosts == 0 {
		return ""
	}
	n := float64(s.SqueezePosts)
	return fmt.Sprintf("squeeze  5h %.0f%% · 7d %.0f%% of the limits spent, on average across %s — recorded beside the grades, never in them",
		s.Squeeze5hTotal/n, s.SqueezePTotal/n, plural(s.SqueezePosts, "post"))
}

// graderLine counts the posts that name the cheap path they saw, without
// grading them: a fraction with the distinct count beside it, and no
// percentage — unlike the refs line above it. A percentage is a dial, and
// this field must not have one: refs cost a URL, a sentence costs nothing,
// so "--grader none" on every post would push a coverage figure to 100.
// The distinct count is what indicts that move — 483/483 · 1 distinct.
// When nothing carries the field, the absence is named the way the dialog
// line names silence, with the command that ends it.
func graderLine(s wall.Stats) string {
	if s.WithGrader == 0 {
		return `grader   none — no post names the cheap path it saw; wallii post --grader "<the cheap path, taken or not>" …`
	}
	return fmt.Sprintf("grader   %d/%d posts name a grader moment · %d distinct", s.WithGrader, s.Posts, s.GraderDistinct)
}

// signalsLine puts the measurement beside the report: how many posts the
// hook scanned, how many distinct shortcuts their diffs showed, and how
// many of those nobody named a grader moment for. That last number is the
// whole reason the line exists, and it is reported, not computed with: no
// percentage, no per-actor split, no challenge raised from it. A measured
// shortcut without a grader is often entirely fine — the hook's
// environment-guard filter is good, not perfect — and nobody owes a counter
// an explanation. The shortcuts are counted distinctly and the posts are
// not, because they answer different questions: coverage is a property of
// posts, a shortcut is not, and one named once in a three-post session
// would otherwise report itself as two that went unnamed. Silent when no
// post carries a source: most of the wall predates the hook, and zero
// coverage is not a clean diff.
func signalsLine(s wall.Stats) string {
	if s.SignalsMeasured == 0 {
		return ""
	}
	if s.SignalsShown == 0 {
		return fmt.Sprintf("signals  measured on %s, none carried a shortcut", plural(s.SignalsMeasured, "post"))
	}
	return fmt.Sprintf("signals  %s across %s · %d named a grader moment, %d did not",
		plural(s.SignalsShown, "distinct shortcut"), plural(s.SignalsMeasured, "measured post"),
		s.SignalsNamed, s.SignalsShown-s.SignalsNamed)
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
