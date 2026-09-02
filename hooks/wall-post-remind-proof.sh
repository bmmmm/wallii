#!/usr/bin/env bash
# Red/green proofs for the Stop hook's signature trigger, run under env -i —
# the environment a hook actually gets — with a throwaway HOME and wall under
# $TMPDIR, so the real marker dir and the real wall are never touched. Each
# case is one line changed against the red case: dedup, environment guard,
# off switch, out-of-span commit, tracked edit, classes B and C, no HOME,
# the fold past three findings, comment lines, a Rust attribute, a signature
# inside quotes, a prose file, the real findings that must survive both, a
# threshold above the findings, an unreadable threshold — and the regression
# that the commit trigger still fires. macOS only (`date -v`);
# not part of CI, run it by hand after touching the hook:
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

echo "jq: $(PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin command -v jq || echo missing)"
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

echo "=== $pass passed, $fail failed (tmp: $T)"
[ "$fail" -eq 0 ]
