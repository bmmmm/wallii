#!/usr/bin/env bash
# Red/green proofs for the Stop hook's signature trigger, run under env -i —
# the environment a hook actually gets — with a throwaway HOME and wall under
# $TMPDIR, so the real marker dir and the real wall are never touched. Each
# case is one line changed against the red case: dedup, environment guard,
# off switch, out-of-span commit, tracked edit, classes B and C, no HOME,
# the fold past three findings, comment lines, a Rust attribute, a signature
# inside quotes, a prose file, the real findings that must survive both, a
# threshold above the findings, an unreadable threshold, a fixture that
# writes a skip, an aged session clock in both directions, the marker sweep
# in both directions — and the regression that the commit trigger still
# fires.
# macOS only, and deliberately so: the hook's traps are BSD ones — no `\b` in
# the patterns (a GNU extension), BSD awk's position logic instead of a greedy
# substitution, LC_ALL=C against its abort on non-UTF-8 bytes — and the hook
# runs on nothing but this Mac. A GNU runner would stay green while the real
# hook breaks, so the CI job (`hook` in ci.yml) is macos-latest and the
# `date -v` calls below stay as they are. Runs there on every push, and here
# after every change to the hook:
#
#   go build . && bash hooks/wall-post-remind-proof.sh
#
# Two fixture traps, learned the hard way: the .start file must be backdated
# AND the fixture commit needs an old GIT_COMMITTER_DATE (rev-list --before
# reads the committer date, an author-only backdate leaves it empty), and by
# the later cases the repo holds enough commits for the commit trigger to
# answer instead — raise WALLII_REMIND_AFTER out of the way in green cases.
set -u
here="$(cd "$(dirname "$0")" && pwd)"
HOOK="${HOOK:-$here/wall-post-remind.sh}"
BIN="${BIN:-$here/../wallii}"
T="$(mktemp -d "${TMPDIR:-/tmp}/hookproof.XXXXXX")"
H="$T/home"
mkdir -p "$H/.local/bin" "$H/.claude/wall-post-reminders"
ln -s "$BIN" "$H/.local/bin/wallii"
MD="$H/.claude/wall-post-reminders"
export WALLII_DIR="$T/wall"
R="$T/r"
git init -q "$R"
cd "$R" || exit 1
git config user.email t@t
git config user.name t
old="$(date -u -v-2H +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMITTER_DATE="$old" git commit -qm init --allow-empty --date="$old"
start_epoch=$(( $(date +%s) - 3600 ))
start_iso="$(date -u -v-60M +%Y-%m-%dT%H:%M:%SZ)"
newsid() { printf '%s %s' "$start_epoch" "$start_iso" > "$MD/$1.start"; }
# run <sid> [extra env assignments...]
run() {
    local sid="$1"; shift
    echo "{\"session_id\":\"$sid\",\"stop_hook_active\":false}" \
        | env -i HOME="$H" PATH=/usr/bin:/bin WALLII_DIR="$WALLII_DIR" "$@" "$HOOK"
}
pass=0; fail=0
ok()   { pass=$((pass+1)); echo "PASS $1"; }
bad()  { fail=$((fail+1)); echo "FAIL $1"; }

# Preflight. The hook exits 0 in silence when jq or the wallii binary is
# missing from its PATH, and eleven of the cases below assert silence — on a
# machine without either they pass having tested nothing, and the ten red cases
# fail with output that names the symptom instead of the cause. Check against
# the PATH the hook builds for itself (its own widening on top of run()'s
# /usr/bin:/bin), not the caller's, or the assurance covers a different lookup
# than the test run performs. The substitution keeps PATH inside a subshell.
hook_path="$H/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
jq_bin="$(PATH="$hook_path" command -v jq || true)"
if [ -z "$jq_bin" ]; then
    echo "preflight: no jq on the hook's PATH ($hook_path) — install it: brew install jq" >&2
    exit 1
fi
if [ ! -x "$BIN" ]; then
    echo "preflight: no wallii binary at $BIN — build it: go build ." >&2
    exit 1
fi
echo "jq: $jq_bin"
echo "binary: $BIN"
echo "hook: $HOOK"

# 1 RED — untracked test file with a bare skip
newsid s1
printf 'func TestX(t *testing.T){ t.Skip("flaky") }\n' > a_test.go
out="$(run s1)"; rc=$?
echo "--- red output (rc=$rc):"; printf '%s\n' "$out"
if printf '%s' "$out" | grep -q '"decision": *"block"' && printf '%s' "$out" | grep -q 'a_test.go:1' && printf '%s' "$out" | grep -Fq 't.Skip(\"flaky\")'; then ok "1 red: block names a_test.go:1 and the skip line"; else bad "1 red"; fi
echo "--- markers:"; /bin/ls -la "$MD"; echo "--- marker s1-r.shortcut:"; cat -v "$MD/s1-r.shortcut" 2>/dev/null || echo "(no marker)"

# 2 DEDUP — same tree, same session → silent
out="$(run s1 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "2 dedup: unchanged signature stays quiet"; else bad "2 dedup (rc=$rc): $out"; fi

# 3 ENV GUARD — the reason names the environment → silent
newsid s3
printf 'func TestX(t *testing.T){ t.Skip("requires docker") }\n' > a_test.go
out="$(run s3 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "3 env guard: requires docker stays quiet"; else bad "3 env guard: $out"; fi
newsid s3b
printf 'func TestX(t *testing.T){ t.Skip("set SCREENII_LIVE=1 to run") }\n' > a_test.go
out="$(run s3b WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "3b env guard: env var reason stays quiet"; else bad "3b env guard: $out"; fi
if [ -f "$MD/s3-r.shortcut" ] && [ ! -s "$MD/s3-r.shortcut" ]; then ok "3c marker exists and is empty after a clean scan"; else bad "3c marker after clean scan"; fi

# 4 OFF SWITCH — signature present, trigger off → silent, no marker
newsid s4
printf 'func TestX(t *testing.T){ t.Skip("flaky") }\n' > a_test.go
out="$(run s4 WALLII_REMIND_SHORTCUTS=0 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ] && [ ! -e "$MD/s4-r.shortcut" ]; then ok "4 off switch: quiet and no marker"; else bad "4 off switch: $out"; fi

# 5 OUT OF SPAN — the skip was committed before the session started → silent
newsid s5
git add a_test.go
GIT_COMMITTER_DATE="$old" git commit -qm "old skip" --date="$old"
out="$(run s5 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "5 out of span: pre-session skip stays quiet"; else bad "5 out of span: $out"; fi

# 5b AUTHOR-ONLY BACKDATE would be the wrong fixture: prove rev-list sees the committer date
base="$(git rev-list -1 --before="$start_iso" HEAD)"
if [ -n "$base" ]; then ok "5b rev-list finds a base before the session start"; else bad "5b no base"; fi

# 6 TRACKED EDIT — modify the committed file in the working tree → block
newsid s6
printf 'func TestX(t *testing.T){ t.Skip("golden has no knownSlots, regenerate it") }\n' > a_test.go
out="$(run s6 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if printf '%s' "$out" | grep -q '"decision": *"block"' && printf '%s' "$out" | grep -q 'a_test.go:1'; then ok "6 tracked edit: block on the working-tree change"; else bad "6 tracked edit: $out"; fi
git checkout -q -- a_test.go

# 7 CLASS B and C — CI step allowed to fail, checker overruled
newsid s7
mkdir -p .github/workflows
printf 'steps:\n  - run: go test ./...\n    continue-on-error: true\n' > .github/workflows/ci.yml
printf 'x = foo()  # type: ignore\n' > m.py
out="$(run s7 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if printf '%s' "$out" | grep -q 'ci.yml:3' && printf '%s' "$out" | grep -q 'm.py:1'; then ok "7 class B+C: both lines named"; else bad "7 class B+C: $out"; fi
rm -rf .github m.py

# 8 MINIMAL PATH WITHOUT HOME — silent through the guards, no crash
newsid s8
printf 'func TestX(t *testing.T){ t.Skip("flaky") }\n' > a_test.go
out="$(echo '{"session_id":"s8","stop_hook_active":false}' | env -i PATH=/usr/bin:/bin "$HOOK" 2>&1)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "8 no HOME: quiet exit 0"; else bad "8 no HOME (rc=$rc): $out"; fi
git checkout -q -- a_test.go

# 9 REGRESSION — three plain commits, no signature → the commit trigger still blocks
newsid s9
for i in 1 2 3; do printf 'package r // %s\n' "$i" > "f$i.go"; git add "f$i.go"; git commit -qm "plain $i"; done
out="$(run s9 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if printf '%s' "$out" | grep -q 'commit(s) in `r`'; then ok "9 regression: commit trigger still fires"; else bad "9 regression: $out"; fi

# 10 FOLD — four findings show three and '… and 1 more'
newsid s10
printf 'func TestA(t *testing.T){ t.Skip("flaky") }\nfunc TestB(t *testing.T){ t.Skip("slow") }\nfunc TestC(t *testing.T){ t.Skip("meh") }\nfunc TestD(t *testing.T){ t.Skip("later") }\n' > b_test.go
out="$(run s10 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if printf '%s' "$out" | grep -q 'and 1 more'; then ok "10 fold: three shown, one folded"; else bad "10 fold: $out"; fi
rm -f b_test.go

# 11 COMMENT LINES — prose that names a signature is not a signature
newsid s11
printf '// t.Skip("flaky") is what a reviewer greps for\n# continue-on-error: true would be the cheap fix\n/* type: ignore is the python escape hatch */\n' > note.go
# the fixture repo carries 5 commits by now, so the commit trigger is raised out of the way
out="$(run s11 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "11 comment lines stay quiet"; else bad "11 comment lines: $out"; fi
rm -f note.go

# 12 RUST ATTRIBUTE — #[ignore] starts with # and is still a real skip
newsid s12
printf '#[ignore]\nfn parses_empty_cart() {}\n' > lib.rs
out="$(run s12 WALLII_REMIND_IDLE_MIN=0)"; rc=$?
if printf '%s' "$out" | grep -q 'lib.rs:1'; then ok "12 rust #[ignore] still fires"; else bad "12 rust attribute: $out"; fi
rm -f lib.rs

# 13 QUOTED SIGNATURE — a line that DEFINES or PRINTS a signature is not one.
#    Three shapes, all seen on the live wall on 2026-09-02: a shell variable
#    holding the scanner's own pattern, a printf that names a bypass in its
#    help text, and a config key quoted inside a message.
newsid s13
{
    printf 'bypass_re="(npm test[^\t]*\\|\\| *true|continue-on-error: *true|--no-verify)$"\n'
    printf "printf 'push-gate: refusing git push --no-verify to %%s\\n' \"\$remote\" >&2\n"
    printf 'log_warn "a step with continue-on-error: true hides its own failure"\n'
} > gate.sh
out="$(run s13 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "13 quoted signatures stay quiet"; else bad "13 quoted signatures: $out"; fi
rm -f gate.sh

# 14 PROSE FILE — a signature in Markdown is documentation, never a shortcut.
#    Reproduced against this repo's own README on 2026-09-02.
newsid s14
printf 'The scanner reads `go test ./... || true` and `continue-on-error: true`\nas ways around a check, and `t.Skip(` as a switched-off test.\n' > NOTES.md
out="$(run s14 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "14 prose file stays quiet"; else bad "14 prose file: $out"; fi
rm -f NOTES.md

# 14b THE PROSE FILTER ON ITS OWN. Case 14 above proves the outcome, not the
#     mechanism: every signature in its fixture sits in backticks, so the
#     quote rule holds the line and the extractor's `prose` flag never has to
#     — set it to 0 and the suite still passed 21/21 (measured 2026-09-02,
#     while wiring the suite into CI). A filter no case can turn red is not
#     covered. This line carries the signature in plain prose: no backtick or
#     quote before the anchor, no comment marker, no guard word, no
#     upper-case token — the file suffix is the only thing left that can keep
#     it quiet.
newsid s14b
printf 'Every t.Skip( written down here is documentation, and the scanner has to read it as prose.\n' > NOTES.md
out="$(run s14b WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "14b prose filter: an unquoted signature in Markdown stays quiet"; else bad "14b prose filter: $out"; fi
rm -f NOTES.md

# 15 THE QUOTE RULE MUST NOT SWALLOW REAL FINDINGS — a skip reason is quoted,
#    the skip itself is not, and a gate with `|| true` after a quoted argument
#    is still a gate. This is the case the rule above is most likely to break.
newsid s15
{
    printf 'func TestCheckout(t *testing.T){ t.Skip("flaky under load") }\n'
    printf 'go test -run "TestCheckout" ./... || true\n'
} > run_test.go
out="$(run s15 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
if printf '%s' "$out" | grep -q 'run_test.go:1' && printf '%s' "$out" | grep -q 'run_test.go:2'; then ok "15 real findings survive the quote rule"; else bad "15 real findings: $out"; fi
rm -f run_test.go

# 16 THRESHOLD SPLITS ASKING FROM MEASURING — with the threshold above the
#    number of findings the hook must stay quiet, and still record what the
#    diff showed. An empty marker means "scanned, nothing found"; writing
#    that while holding back a finding is the one lie signal_src exists to
#    prevent.
newsid s16
printf 'func TestOne(t *testing.T){ t.Skip("flaky") }\n' > one_test.go
out="$(run s16 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99 WALLII_REMIND_SHORTCUTS=2)"; rc=$?
if [ -n "$out" ]; then bad "16 threshold: hook asked below its threshold: $out"; else
    if LC_ALL=C grep -Fq 't.Skip("flaky")' "$MD/s16-r.shortcut" 2>/dev/null; then
        ok "16 threshold: quiet, and the finding is still recorded"
    else
        bad "16 threshold: marker says nothing was found ($(wc -c < "$MD/s16-r.shortcut" 2>/dev/null || echo no-file) bytes)"
    fi
fi
rm -f one_test.go

# 17 UNREADABLE THRESHOLD IS NOT AN OFF SWITCH — a non-numeric value made
#    `[ -gt ]` fail and took the whole trigger down without a word. It must
#    say so and fall back to the default instead.
newsid s17
printf 'func TestTwo(t *testing.T){ t.Skip("flaky") }\n' > two_test.go
out="$(run s17 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99 WALLII_REMIND_SHORTCUTS=on 2>&1)"; rc=$?
if printf '%s' "$out" | grep -q 'WALLII_REMIND_SHORTCUTS' && printf '%s' "$out" | grep -q 'two_test.go:1'; then
    ok "17 unreadable threshold: says so and still fires"
else
    bad "17 unreadable threshold: $out"
fi
rm -f two_test.go

# 18 A FIXTURE THAT WRITES A SKIP IS NOT A SKIP — class A inside quotes.
#    Found by the trigger firing on this very file on 2026-09-02: a proof
#    suite for a signature scanner necessarily contains lines that PRODUCE
#    signatures, and they were reported as shortcuts. The exemption class A
#    had was argued backwards — a real `t.Skip("flaky")` carries its anchor
#    BEFORE the quote, so the rule never touches it (case 15 holds that).
newsid s18
{
    printf "printf 'func TestX(t *testing.T){ t.Skip(\"flaky\") }' > fixture_test.go\n"
    printf 'cases+=("pytest.mark.skip is what the scanner greps for")\n'
} > make_fixture.sh
out="$(run s18 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "18 a fixture writing a skip stays quiet"; else bad "18 fixture skip: $out"; fi
rm -f make_fixture.sh

# 19 THE SESSION CLOCK AGES. A session id outlives a pause — Claude Code
#    keeps it across --resume and across a night — and a zero point written
#    exactly once would keep measuring from it. The diff base then sits
#    before work this session never touched, and every signature committed
#    since reads as a finding of this one. Measured on 2026-09-02 over the
#    live markers: 13 of 68 pairs more than 8h apart, the worst 20h.
#
#    Its own repo, because both directions turn on WHICH commit the base
#    lands on, and that needs a history in chronological order rather than
#    the fixture above, whose commits all sit within the last two hours and
#    would send both cases through the empty-tree fallback instead. An
#    anchor at -12h, a committed skip at -6h, working tree clean.
R2="$T/r2"
git init -q "$R2"
cd "$R2" || exit 1
git config user.email t@t
git config user.name t
anchor_date="$(date -u -v-12H +%Y-%m-%dT%H:%M:%SZ)"
skip_date="$(date -u -v-6H +%Y-%m-%dT%H:%M:%SZ)"
printf 'package r2\n' > base.go
git add base.go
GIT_COMMITTER_DATE="$anchor_date" git commit -qm anchor --date="$anchor_date"
printf 'func TestPay(t *testing.T){ t.Skip("flaky") }\n' > pay_test.go
git add pay_test.go
GIT_COMMITTER_DATE="$skip_date" git commit -qm "skip committed 6h ago" --date="$skip_date"
# aged <sid> <hours> — a session clock that already carries age
aged() { printf '%s %s' "$(( $(date +%s) - $2 * 3600 ))" "$(date -u -v-"$2"H +%Y-%m-%dT%H:%M:%SZ)" > "$MD/$1.start"; }

# 19a A STALE CLOCK IS RENEWED — 9h is past the 8h bound, so the zero point
#     moves to now, the base lands on the -6h commit, and the skip below it
#     is not this session's business. Turns red the moment the renewal is
#     taken out: the base falls back to the -12h anchor and the hook reports
#     a skip committed before the session it is measuring.
aged s19 9
before="$(cut -d' ' -f1 "$MD/s19.start")"
out="$(run s19 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
after="$(cut -d' ' -f1 "$MD/s19.start")"
if [ -z "$out" ] && [ "$rc" -eq 0 ] && [ "$after" -gt "$before" ]; then
    ok "19a stale clock: 9h zero point renewed, the pre-session skip stays quiet"
else
    bad "19a stale clock (rc=$rc, $before -> $after): $out"
fi

# 19b A CLOCK INSIDE THE WINDOW IS KEPT — 7h is below the bound, the zero
#     point stands, the base stays at the -12h anchor and the skip is inside
#     the span it measures. This is the direction 19a cannot cover: it turns
#     red if the renewal is made unconditional, which would silence every
#     finding a real session ever makes.
aged s19b 7
before="$(cut -d' ' -f1 "$MD/s19b.start")"
out="$(run s19b WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
after="$(cut -d' ' -f1 "$MD/s19b.start")"
if printf '%s' "$out" | grep -q 'pay_test.go:1' && [ "$after" = "$before" ]; then
    ok "19b clock inside the window: 7h zero point kept, the skip in its span fires"
else
    bad "19b clock inside the window ($before -> $after): $out"
fi

# 19c AN UNREADABLE CLOCK IS NOT A FROZEN ONE — the same failure mode as the
#     unreadable threshold in 17: a value no comparison can read must not
#     leave the base wherever it was. It counts as absent and is rewritten.
printf 'not-a-number x\n' > "$MD/s19c.start"
out="$(run s19c WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"; rc=$?
now_epoch="$(cut -d' ' -f1 "$MD/s19c.start")"
case "${now_epoch:-x}" in
    ''|*[!0-9]*) bad "19c unreadable clock: still unreadable ($now_epoch)" ;;
    *) if [ -z "$out" ] && [ "$rc" -eq 0 ]; then ok "19c unreadable clock: rewritten, hook quiet"; else bad "19c unreadable clock (rc=$rc): $out"; fi ;;
esac

# 20 THE SWEEP TAKES THE OLD AND LEAVES THE LIVE. Markers were written once
#    and never removed — 186 files in the 12 days since the hook went live.
#    On real data the sweep is a no-op today (the oldest marker is 12 days
#    old, so `-mtime +30` matches nothing until late September), which is
#    exactly why the proof cannot come from watching the live directory: it
#    would be green having deleted nothing. These two files carry a real
#    mtime, backdated with touch, and the second direction is the one that
#    matters — a sweep that also took live markers would silently reset
#    every running session's dedup.
touch -t "$(date -v-40d +%Y%m%d%H%M)" "$MD/ancient-session-r.shortcut"
touch "$MD/live-session-r.shortcut"
newsid s20
out="$(run s20 WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"
if [ ! -e "$MD/ancient-session-r.shortcut" ] && [ -e "$MD/live-session-r.shortcut" ]; then
    ok "20 sweep: the 40-day-old marker is gone, today's is untouched"
else
    bad "20 sweep: ancient=$([ -e "$MD/ancient-session-r.shortcut" ] && echo present || echo gone) live=$([ -e "$MD/live-session-r.shortcut" ] && echo present || echo gone)"
fi

# 20b THE SWEEP MUST NOT OUTLIVE THE CLOCK — an ancient .start of the
#     session now running is swept, and the aging block three lines later
#     rewrites it. The session keeps a zero point either way; what it must
#     never end up with is none.
touch -t "$(date -v-40d +%Y%m%d%H%M)" "$MD/s20b.start"
out="$(run s20b WALLII_REMIND_IDLE_MIN=0 WALLII_REMIND_AFTER=99)"
zero="$(cut -d' ' -f1 "$MD/s20b.start" 2>/dev/null || true)"
case "${zero:-x}" in
    ''|*[!0-9]*) bad "20b sweep vs clock: no readable zero point left ($zero)" ;;
    *) if [ $(( $(date +%s) - zero )) -lt 300 ]; then ok "20b sweep vs clock: swept .start rewritten to now"; else bad "20b sweep vs clock: zero point is $(( ($(date +%s) - zero) / 3600 ))h old"; fi ;;
esac

echo "=== $pass passed, $fail failed (tmp: $T)"
[ "$fail" -eq 0 ]
