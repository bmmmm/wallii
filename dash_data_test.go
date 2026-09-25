// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// inlinedConst reads `const NAME = <json> || …;` back out of a written
// dashboard — the value Go handed the page, before the page's fallback.
func inlinedConst(t *testing.T, html, name string, into any) {
	t.Helper()
	line := firstLineWith(html, "const "+name+" = ")
	if len(line) > 300 { // firstLineWith truncates for messages; read the real line
		for _, l := range strings.Split(html, "\n") {
			if strings.HasPrefix(l, "const "+name+" = ") {
				line = l
				break
			}
		}
	}
	v := strings.TrimPrefix(line, "const "+name+" = ")
	if i := strings.Index(v, " || "); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSuffix(v, ";")
	if err := json.Unmarshal([]byte(v), into); err != nil {
		t.Fatalf("cannot read %s back: %v\n%.300s", name, err, v)
	}
}

// runDashJS runs dash.html's script under node with the fixture inlined,
// then tail, and returns what tail printed after "RESULT ". The page's own
// top-level rendering runs first against a stub DOM, so a render that
// throws on this fixture fails here too.
func runDashJS(t *testing.T, node string, f dashFixture, families, tail string) string {
	t.Helper()
	start, end := strings.Index(dashTemplate, "<script>"), strings.LastIndex(dashTemplate, "</script>")
	if start < 0 || end < 0 {
		t.Fatal("dash.html has no <script> block to run")
	}
	script := strings.NewReplacer(
		"__GENERATED__", "fixture",
		"__WALLII_CAL__", f.Cal,
		"__WALLII_COMMITS__", f.Cov,
		"__WALLII_FAMILIES__", families,
		"__WALLII_DOUBT__", f.doubt(),
		"__WALLII_DATA__", f.Evs,
	).Replace(dashTemplate[start+len("<script>") : end])
	harness := `const stub = new Proxy(function () {}, {
  get: (_, k) => k === Symbol.toPrimitive ? () => 0 : k === Symbol.iterator ? function* () {} : k === "then" ? undefined : stub,
  set: () => true, apply: () => stub, construct: () => stub, has: () => true,
});
for (const g of ["document", "window", "localStorage", "navigator", "matchMedia", "location", "requestAnimationFrame"]) globalThis[g] = stub;
` + script + "\n" + tail
	path := filepath.Join(t.TempDir(), "dash-js.js")
	if err := os.WriteFile(path, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("the dashboard script did not run under node: %v\n%s", err, out)
	}
	_, payload, ok := strings.Cut(string(out), "RESULT ")
	if !ok {
		t.Fatalf("no result line from node:\n%s", out)
	}
	return strings.TrimSpace(payload)
}

func usd(v float64) *float64 { return &v }

// The page's half of cost and doubt. Cost has the same contract as the
// commits card: a day before the first reading is a gap, never $0, and a
// post without a reading adds nothing — not even to the count. Doubt
// narrows like everything else: a haunted pair by its ok's range and
// family, a challenge by the family it waits on.
func TestDashAggregatesCostAndDoubt(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half cannot be executed without it")
	}
	loc := mustLoc(t, "Europe/Berlin")
	today := wall.DayStart(time.Date(2026, time.April, 12, 15, 0, 0, 0, loc), loc)
	at := func(daysBack, hour int) int64 {
		return today.AddDate(0, 0, -daysBack).Add(time.Duration(hour) * time.Hour).UnixMilli()
	}
	evs := []dashEvent{
		{ID: "a000001", T: at(6, 10), Repo: "webshop", Actor: "claude/main", Msg: "unmeasured, before any reading", Out: "ok"},
		{ID: "a000002", T: at(3, 10), Repo: "webshop", Actor: "claude/main", Msg: "first reading", Out: "partial", Mood: "rough", Usd: usd(4), Sq7: 40, Sq5: 9},
		{ID: "a000003", T: at(3, 11), Repo: "webshop", Actor: "claude/main", Msg: "unmeasured after it", Out: "ok"},
		{ID: "a000004", T: at(2, 10), Repo: "garden", Actor: "codex/main", Msg: "second reading", Out: "failed", Usd: usd(0.5), Vs: 1, Sq7: 35, Sq5: 3},
		{ID: "a000005", T: at(1, 10), Repo: "webshop", Actor: "claude/main", Msg: "the fix", Out: "ok", Topic: "fix"},
	}
	f := makeDashFixture(t, loc, today.Add(15*time.Hour), nil, evs)
	f.Doubt = `{"challenges":[` +
		`{"t":` + strconv.FormatInt(at(20, 9), 10) + `,"actor":"wallii/lint","msg":"old, still waiting","target":"zzz","repo":"garden","who":"codex/main","tmsg":"gone"}],` +
		`"haunted":[{"ok":"a000001","fix":"a000005","shared":["x","y"]},{"ok":"a000003","fix":"a000005","shared":["x","y"]}]}`
	fam := `{"claude/main":"claude","codex/main":"codex"}`
	tail := `
const pick = agg => ({
  usd: agg.usd, usdN: agg.usdN,
  usdCov: agg.buckets.map(b => b.usdCov),
  first: agg.buckets.findIndex(b => b.usdCov),
  open: agg.open.map(e => e.id), contra: agg.contraPosts.map(e => e.id), rough: agg.rough.map(e => e.id),
  haunted: agg.haunted.map(h => h.ok.id + ">" + (h.fix ? h.fix.id : "?")), oks: agg.oks,
  challenges: agg.challenges.length,
  sq: agg.sq ? [agg.sq.id, agg.sqMax] : null,
  repoUsd: Object.fromEntries(Object.entries(agg.repos).map(([k, r]) => [k, [r.usd, r.usdN, r.open, r.rough]])),
});
const all = pick(aggregate(5));
currentFamily = "claude";
const claude = pick(aggregate(5));
currentFamily = "codex";
const codex = pick(aggregate(5));
// the axis floor: whole counts keep their 4, a spend scales below it
const axis = [niceMax(3), niceMax(0.43, 0.01), niceMax(0.004, 0.01)];
console.log("RESULT " + JSON.stringify({ all, claude, codex, axis }));`
	var res struct {
		All, Claude, Codex struct {
			Usd                 float64
			UsdN                int
			UsdCov              []bool
			First               int
			Open, Contra, Rough []string
			Haunted             []string
			Oks, Challenges     int
			RepoUsd             map[string][]float64
			Sq                  []any
		}
		Axis []float64
	}
	if err := json.Unmarshal([]byte(runDashJS(t, node, f, fam, tail)), &res); err != nil {
		t.Fatal(err)
	}
	a := res.All
	// 5-day range: days 4..0 back. The first reading is 3 days back, so
	// bucket 0 (4 back) is a gap and every later one is measured.
	if want := []bool{false, true, true, true, true}; fmt.Sprint(a.UsdCov) != fmt.Sprint(want) {
		t.Errorf("cost coverage per bucket %v, want %v — a day before the first reading is a gap, never $0", a.UsdCov, want)
	}
	if a.Usd != 4.5 || a.UsdN != 2 {
		t.Errorf("range cost $%v over %d measured, want $4.5 over 2 — an unmeasured post adds nothing, not even to the count", a.Usd, a.UsdN)
	}
	if strings.Join(a.Open, ",") != "a000002,a000004" || strings.Join(a.Contra, ",") != "a000004" || strings.Join(a.Rough, ",") != "a000002" {
		t.Errorf("open %v, contradicting %v, rough %v", a.Open, a.Contra, a.Rough)
	}
	// a000001 is outside the 5-day range: its pair drops out with it
	if strings.Join(a.Haunted, ",") != "a000003>a000005" || a.Oks != 2 {
		t.Errorf("haunted %v of %d oks, want only the pair whose ok is in range, of 2", a.Haunted, a.Oks)
	}
	if a.Challenges != 1 {
		t.Errorf("a challenge older than the range is still open and must be listed, got %d", a.Challenges)
	}
	if r := a.RepoUsd["webshop"]; len(r) != 4 || r[0] != 4 || r[1] != 1 || r[2] != 1 || r[3] != 1 {
		t.Errorf("webshop row [usd usdN open rough] = %v, want [4 1 1 1]", r)
	}
	if c := res.Claude; c.Challenges != 0 || len(c.Haunted) != 1 || c.Usd != 4 {
		t.Errorf("claude chip: %d challenges (the one open waits on codex), haunted %v, $%v", c.Challenges, c.Haunted, c.Usd)
	}
	if c := res.Codex; c.Challenges != 1 || len(c.Haunted) != 0 || c.Usd != 0.5 {
		t.Errorf("codex chip: %d challenges, haunted %v (its oks are claude's), $%v", c.Challenges, c.Haunted, c.Usd)
	}
	// codex's first reading is two days back, claude's three: under the
	// codex chip the day before its own first reading is a gap, not $0
	if want := []bool{false, false, true, true, true}; fmt.Sprint(res.Codex.UsdCov) != fmt.Sprint(want) {
		t.Errorf("codex chip cost coverage %v, want %v — the gap follows the family's own first reading", res.Codex.UsdCov, want)
	}
	// the Limit tile reads the newest reading and the peak
	if fmt.Sprint(a.Sq) != "[a000004 40]" {
		t.Errorf("limit reading %v, want the newest post a000004 and the peak 40", a.Sq)
	}
	if fmt.Sprint(res.Axis) != "[4 0.5 0.01]" {
		t.Errorf("axis tops %v, want [4 0.5 0.01] — counts keep their floor of 4, a spend scales down to a cent", res.Axis)
	}
}

// The open card's grouping and caps, run as data: one item per post however
// many kinds it carries, repos ordered by their newest item, each group cut
// to perRepo shown items with the rest counted as older, and the list cut to
// the first groups with the rest folded. A challenge without a target files
// under "(no target)" rather than disappearing.
func TestDashOpenGroupsMergeOrderAndCap(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half cannot be executed without it")
	}
	loc := mustLoc(t, "Europe/Berlin")
	today := wall.DayStart(time.Date(2026, time.April, 12, 15, 0, 0, 0, loc), loc)
	h := func(hour int) int64 { return today.Add(time.Duration(hour-24) * time.Hour).UnixMilli() }
	evs := []dashEvent{
		{ID: "d000001", T: h(1), Repo: "webshop", Actor: "claude/main", Msg: "w1", Out: "partial"},
		{ID: "d000002", T: h(2), Repo: "webshop", Actor: "claude/main", Msg: "w2", Out: "partial", Vs: 1},
		{ID: "d000003", T: h(3), Repo: "webshop", Actor: "claude/main", Msg: "w3", Out: "failed"},
		{ID: "d000004", T: h(4), Repo: "garden", Actor: "claude/main", Msg: "g1", Out: "ok"},
		{ID: "d000005", T: h(5), Repo: "garden", Actor: "claude/main", Msg: "g2 fix", Out: "ok", Topic: "fix"},
		{ID: "d000006", T: h(6), Repo: "attic", Actor: "claude/main", Msg: "a1", Out: "partial"},
	}
	f := makeDashFixture(t, loc, today.Add(15*time.Hour), nil, evs)
	f.Doubt = `{"challenges":[{"t":` + strconv.FormatInt(h(0), 10) + `,"actor":"wallii/lint","msg":"orphan"}],` +
		`"haunted":[{"ok":"d000004","fix":"d000005","shared":["a","b"]}]}`
	out := runDashJS(t, node, f, `{"claude/main":"claude"}`, `
const g = openGroups(aggregate(7), 2, 2);
const view = x => ({ repo: x.repo, head: x.head.map(i => i.key + ":" + [...i.kinds].sort().join("+") + (i.fixes.length ? ">" + i.fixes.map(f => f && f.id).join() : "")), more: x.more.map(i => i.key) });
console.log("RESULT " + JSON.stringify({ total: g.total, repos: g.repos, shown: g.shown.map(view), rest: g.rest.map(view) }));`)
	var res struct {
		Total, Repos int
		Shown, Rest  []struct {
			Repo       string
			Head, More []string
		}
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Total != 6 || res.Repos != 4 {
		t.Errorf("%d items in %d repos, want 6 in 4 — the partial post that also contradicts is one item", res.Total, res.Repos)
	}
	got := fmt.Sprint(res.Shown)
	want := "[{attic [d000006:partial] []} {garden [d000004:haunted>d000005] []}]"
	if got != want {
		t.Errorf("shown groups\n got %s\nwant %s — repos by newest item, the haunted ok carries its fix", got, want)
	}
	if got := fmt.Sprint(res.Rest); got != "[{webshop [d000003:failed d000002:contradicts+partial] [d000001]} {(no target) [c:"+strconv.FormatInt(h(0), 10)+"orphan:challenge] []}]" {
		t.Errorf("folded groups %s — webshop capped at 2 with its oldest as older, the orphan challenge under (no target)", got)
	}
}

// A repo is a name off the wall, and a name can be "constructor" or
// "__proto__". Kept in a plain {} those read Object.prototype, the page's
// aggregate threw, and one such post blanked the whole dashboard (found in
// review, 2026-09-25). The top-level render runs inside runDashJS, so a
// throw anywhere — the open card's grouping of a target-only repo included —
// fails here.
func TestDashSurvivesReposNamedLikeObjectBuiltins(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half cannot be executed without it")
	}
	loc := mustLoc(t, "Europe/Berlin")
	today := wall.DayStart(time.Date(2026, time.April, 12, 15, 0, 0, 0, loc), loc)
	t0 := today.Add(-20 * time.Hour).UnixMilli()
	var evs []dashEvent
	for i, name := range []string{"constructor", "__proto__", "toString", "hasOwnProperty"} {
		evs = append(evs, dashEvent{ID: fmt.Sprintf("b%06d", i), T: t0 + int64(i)*60000, Repo: name, Actor: "claude/main",
			Topic: "constructor", Msg: "shipped", Out: "partial", Mood: "rough", Usd: usd(1)})
	}
	f := makeDashFixture(t, loc, today.Add(15*time.Hour), nil, evs)
	f.Doubt = `{"challenges":[{"t":` + strconv.FormatInt(t0, 10) + `,"actor":"wallii/lint","msg":"why","repo":"valueOf","who":"claude/main"}],"haunted":[]}`
	out := runDashJS(t, node, f, `{"claude/main":"claude"}`, `
const agg = aggregate(7);
console.log("RESULT " + JSON.stringify({ repos: Object.keys(agg.repos).sort(), posts: Object.values(agg.repos).map(r => r.posts), usd: agg.usd, polluted: ({}).posts !== undefined }));`)
	var res struct {
		Repos    []string
		Posts    []int
		Usd      float64
		Polluted bool
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Repos) != "[__proto__ constructor hasOwnProperty toString]" || fmt.Sprint(res.Posts) != "[1 1 1 1]" || res.Usd != 4 {
		t.Errorf("repos %v with posts %v and $%v — every name is an ordinary repo", res.Repos, res.Posts, res.Usd)
	}
	if res.Polluted {
		t.Error("a repo named __proto__ wrote onto Object.prototype")
	}
}

// Weekly buckets (a range over 60 days) are cut from the range's start, not
// from the first cost reading. A week straddling that reading still holds
// every reading there is — before it there are none — so it is measured and
// drawn; gating it on all seven days hid a week the headline counted.
func TestDashCostWeekStraddlingTheFirstReadingIsDrawn(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — the browser half cannot be executed without it")
	}
	loc := mustLoc(t, "Europe/Berlin")
	today := wall.DayStart(time.Date(2026, time.April, 12, 15, 0, 0, 0, loc), loc)
	start := today.AddDate(0, 0, -99)
	at := func(day int) int64 { return start.AddDate(0, 0, day).Add(12 * time.Hour).UnixMilli() }
	evs := []dashEvent{
		{ID: "c000001", T: at(0), Repo: "webshop", Actor: "claude/main", Msg: "the first day, unmeasured"},
		{ID: "c000002", T: at(59), Repo: "webshop", Actor: "claude/main", Msg: "first reading", Usd: usd(1)},
		{ID: "c000003", T: at(60), Repo: "webshop", Actor: "claude/main", Msg: "second reading", Usd: usd(2)},
	}
	f := makeDashFixture(t, loc, today.Add(15*time.Hour), nil, evs)
	out := runDashJS(t, node, f, `{"claude/main":"claude"}`, `
const agg = aggregate(0);
const drawn = agg.buckets.filter(b => b.usdCov).reduce((s, b) => s + Object.values(b.usdByRepo).reduce((a, v) => a + v, 0), 0);
console.log("RESULT " + JSON.stringify({ weekly: agg.weekly, usd: agg.usd, drawn, gaps: agg.buckets.filter(b => !b.usdCov).length }));`)
	var res struct {
		Weekly     bool
		Usd, Drawn float64
		Gaps       int
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Weekly {
		t.Fatal("the fixture did not reach weekly buckets — the straddle this test is about is not there")
	}
	if res.Usd != 3 || res.Drawn != 3 {
		t.Errorf("headline $%v, drawn $%v — every reading the headline counts must be in a drawn bucket", res.Usd, res.Drawn)
	}
	if res.Gaps != 8 {
		t.Errorf("%d gap weeks, want the 8 wholly before the first reading", res.Gaps)
	}
}

// The dashboard's cost, doubt and limit data, read the way the page gets
// it. The window cut is the point: a unit is the delta to the session's
// previous post, and that post may lie before --since.
func TestDashInlinesCostsDoubtAndLimits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WALLII_DIR", dir)
	t.Setenv("WALLII_REPO_ROOTS", t.TempDir()) // no git: this test is about the wall side
	now := time.Now()
	day := 24 * time.Hour
	early := now.Add(-5 * day) // before the --since 2d window
	late := now.Add(-day)      // inside it

	ok := wall.Event{TS: late.Add(2 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "feature",
		Msg: "retry loop waits for the fsync before rename", Outcome: wall.OutcomeOK}
	fix := wall.Event{TS: late.Add(3 * time.Hour), Repo: "webshop", Actor: "bot/builder", Topic: "fix",
		Msg: "fsync in the retry loop was skipped on close", Outcome: wall.OutcomeOK}
	earlyOK := wall.Event{TS: early.Add(time.Hour), Repo: "garden", Actor: "bot/builder", Topic: "feature",
		Msg: "sprinkler schedule honours the frost guard", Outcome: wall.OutcomeOK}
	earlyFix := wall.Event{TS: early.Add(2 * time.Hour), Repo: "garden", Actor: "bot/builder", Topic: "fix",
		Msg: "frost guard skipped the sprinkler schedule at dawn", Outcome: wall.OutcomeOK}
	first := wall.Event{TS: early, Repo: "webshop", Actor: "bot/builder", Msg: "first unit of the session",
		Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 1.00, TokCum: 1000}
	second := wall.Event{TS: late, Repo: "webshop", Actor: "bot/builder", Msg: "second unit of the session",
		Sess: "aaaaaaaa", CostSrc: wall.CostSession, CostCum: 1.25, TokCum: 1400,
		SqueezeP: 61, Squeeze5h: 12, SqueezeSrc: "session"}
	unmeasured := wall.Event{TS: late.Add(time.Hour), Repo: "webshop", Actor: "bot/builder", Msg: "nobody read the meter"}

	openC := wall.Event{TS: early.Add(3 * time.Hour), Repo: first.Repo, Actor: "wallii/lint", Kind: wall.KindChallenge,
		Parent: first.ID(), Msg: "graded ok with a leftover — regrade or say why not"}
	closedC := wall.Event{TS: early.Add(4 * time.Hour), Repo: first.Repo, Actor: "bot/critic", Kind: wall.KindChallenge,
		Parent: first.ID(), Msg: "which commit is this"}
	answer := wall.Event{TS: early.Add(5 * time.Hour), Repo: first.Repo, Actor: "bot/builder", Kind: wall.KindReact,
		Parent: closedC.ID(), Msg: "the one in the ref"}

	// an actor who posted only before the window, challenged there: the chip
	// must still file the challenge under its family
	gone := wall.Event{TS: early.Add(6 * time.Hour), Repo: "garden", Actor: "bot/retired", Msg: "last word before the cut"}
	goneC := wall.Event{TS: early.Add(7 * time.Hour), Repo: "garden", Actor: "wallii/lint", Kind: wall.KindChallenge,
		Parent: gone.ID(), Msg: "which commit"}
	for _, e := range []wall.Event{first, earlyOK, earlyFix, openC, closedC, answer, gone, goneC, second, unmeasured, ok, fix} {
		if err := wall.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "d.html")
	if err := cmdDash([]string{"-o", out, "--since", "2d"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)

	var raw []dashEvent
	inlinedConst(t, html, "RAW", &raw)
	byMsg := map[string]dashEvent{}
	for _, e := range raw {
		byMsg[e.Msg] = e
	}
	if _, in := byMsg[first.Msg]; in {
		t.Fatal("the fixture's first post is inside the window — the edge this test is about is not there")
	}
	switch s := byMsg[second.Msg]; {
	case s.Usd == nil:
		t.Errorf("the second unit carries no cost")
	case *s.Usd < 0.2499 || *s.Usd > 0.2501:
		t.Errorf("the second unit reads $%.2f, want the $0.25 delta — a unit read over the window counts from session start", *s.Usd)
	}
	if s := byMsg[second.Msg]; s.Sq7 != 61 || s.Sq5 != 12 || s.Sess != "aaaaaaaa" {
		t.Errorf("limits or session lost on the way: %+v", s)
	}
	if u := byMsg[unmeasured.Msg]; u.Usd != nil {
		t.Errorf("an unmeasured post arrives as $%.2f — absent means nobody measured, never free", *u.Usd)
	}
	for _, e := range raw {
		if e.ID == "" {
			t.Errorf("post %q has no id — challenges and haunted pairs point at it", e.Msg)
		}
	}

	var fams map[string]string
	inlinedConst(t, html, "FAMILIES", &fams)
	if fams["bot/retired"] != "bot" {
		t.Errorf("an actor with an open challenge but no post in the window maps to %q, want its family \"bot\" — the chip would drop the challenge", fams["bot/retired"])
	}
	var doubt dashDoubt
	inlinedConst(t, html, "DOUBT", &doubt)
	if len(doubt.Challenges) != 2 || doubt.Challenges[0].Msg != openC.Msg {
		t.Fatalf("want the two unanswered challenges, oldest first, from before the window as they are — got %+v", doubt.Challenges)
	}
	if c := doubt.Challenges[0]; c.Target != first.ID() || c.Repo != "webshop" || c.TMsg != first.Msg {
		t.Errorf("the challenge must name its target, which is not inlined: %+v", c)
	}
	if len(doubt.Haunted) != 1 || doubt.Haunted[0].OK != ok.ID() || doubt.Haunted[0].Fix != fix.ID() {
		t.Errorf("want the one haunted pair whose ok is inside the window, got %+v", doubt.Haunted)
	}
	// Positive control for the exclusion: the early pair is a haunting too,
	// or dropping it proves nothing.
	if n := len(wall.Hauntings([]wall.Event{earlyOK, earlyFix})); n != 1 {
		t.Fatalf("the early pair is not a haunting (%d) — the window cut above is untested", n)
	}
}
