#!/usr/bin/env bash
# Stop hook: report work in this repo that never reached the wall.
#
# wallii's own design note says "whatever a convention file merely asks for
# decays", and applies that to the fields: brevity is enforced at post time,
# took is derived, contradictions are counted. But the trigger — post an
# finished unit of work as it lands — stayed prose in a convention file, with
# nothing that fires. It decayed exactly as predicted:
#
#   2026-08-14   106 commits    2 posts
#   2026-08-18    87 commits    2 posts
#   2026-08-20   151 commits   46 posts
#
# 193 of 827 commits (23%) landed on days the wall was effectively blind. The
# problem is not the rate — one post is a unit of work, not a commit — it is
# the spread: same workload, wildly different coverage.
#
# Why Stop and not SessionEnd: SessionEnd output is ignored by contract (see
# session-cleanup.sh and foreign-write-remind.sh). Stop is the last moment
# where the session can still act on the reminder — and posting is precisely
# something it can still do.
#
# What it does NOT do: check what the post says. A gate on the message buys
# clean ratios by making the writing duller (wallii, 2026-08-18: grade lints
# report and count contradictions, they never reject). This only asks whether
# the work is visible at all.
#
# Those two triggers fire on silence — N commits without a post, an hour
# without a commit — and can, by construction, never catch the interesting
# moment. The third fires on an occurrence: an added line in this session's
# diff that reads like a way around a check rather than through it — a test
# switched off, a gate told to pass, a checker overruled. Calibrated on
# 1,975 commits across 10 local repos: ~14 hits, one per ~141 commits, 50×
# rarer than the commit trigger — an incident trigger, not a rhythm trigger.
# Class A (test disabled) had 12 raw hits, 9 of them
# t.Skip("set SCREENII_LIVE=1 …") environment guards, hence the env-guard
# filter (12 → 4); B (gate silenced) 9, nearly all real; C (checker
# overruled, soundness only) 1; D (test commented out) 0.
#
# Struck, each with its measurement: 2>/dev/null (23 hits in wallii alone,
# every one a deliberate optional-lookup idiom — this hook carries 15 of
# them); bare `|| true` (19 in wallii, all deliberate under set -u; narrowed
# to a NAMED gate command + `|| true` it is 0 here and 9 corpus-wide, nearly
# all real, so only that form stays); `# noqa` (104 of 108 hits in one repo
# are E402/F401 import bookkeeping); eslint-disable (7/7 no-await-in-loop, a
# style rule); assert/expect in class D (4/4 English prose — and `#` +
# `expect(` collides with Swift Testing's #expect macro, 397 hits in one
# repo, which is why D demands whitespace after the comment char and matches
# declarations only); raised timeouts and retries (5s→30s is
# indistinguishable from a correct fix); loosened asserts, regenerated
# goldens and lint removed from CI (the evidence is a REMOVED line). The
# rule behind every strike: a signature must be showable as one ADDED line,
# because the block shows the line instead of claiming an omission — and a
# claim invites a defence. `git commit --no-verify` typed at the prompt is a
# known gap: it never appears in a diff and belongs to a PreToolUse hook.
#
# Caveat, so that no later session reads a zero as an all-clear: the trigger
# catches the KNOWN forms. An agent that knows the Stop hook looks for
# t.Skip( does not cut fewer corners, it cuts them differently (evaluation
# awareness, alignment.anthropic.com/2026/reward-seeker). A clean signal
# count is not proof that nothing was cut short.
#
# Silent unless: wallii is installed, we are in a git repo, and either the
# session's diff carries a shortcut signature not yet reported, or the
# session sat idle without commit or post, or commits have piled up past the
# threshold since that repo's last post. Each trigger reports a finding once
# — deciding not to post is respected until the next finding arrives.
set -u

# Hooks do not inherit an interactive shell's PATH. Every tool this needs lives
# outside the default one — wallii in ~/.local/bin, jq and git in Homebrew — and
# every lookup below fails *silently* (exit 0) when they are missing, which
# would leave the gate permanently and invisibly off. Verified: under
# `env -i PATH=/usr/bin:/bin` this hook stayed silent on a repo with 28 unposted
# commits. Widen the PATH first; a directory that does not exist costs nothing.
# HOME itself is not guaranteed either: under set -u an unset HOME used to
# abort the hook with "unbound variable" instead of letting the guards below
# keep it silent — so it is read with a default everywhere.
PATH="${HOME:-}/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export PATH

INPUT="$(cat)"
field() { printf '%s' "$INPUT" | jq -r "$1 // empty" 2>/dev/null || true; }

# ── The record of this Stop ──────────────────────────────────────────────
# One line per Stop, whatever this hook decides — because a counter of
# firings cannot answer the question the firings raise. "Idle: 0 firings"
# reads as "the condition was never true", and the marker directory says
# something else: 107 session markers in 12 days against roughly 60 sessions
# a day, with the .start file written after every guard below. If that holds,
# most Stops never reach a trigger at all, and "never ran" and "condition
# false" are indistinguishable from any count of firings. Only a line written
# on the way out can tell them apart.
#
# The clock is read once, here, in the shape every reader of it needs: epoch
# for the session clock, ISO for the diff base, for the record and for the
# log's name. On a Stop that reaches its triggers it replaces the two to
# three `date` forks this hook used to make, so the record costs less than
# nothing there — every other value in the line is a shell variable already,
# and the append is one printf. A Stop that dies at a guard pays for it: the
# clock, the mkdir and the jq for the session id used to sit below the
# guards, and now run before them — three forks, for the one record that
# explains an empty wall.
#
# The clock, the marker dir, its mkdir and the session id sit ABOVE the guards
# on purpose. The mkdir used to come after every one of them, so
# exit=no-wallii — a wall that is empty because the tool is not installed,
# which is the most valuable thing this record can say — had nowhere to write.
# HOME is checked before the mkdir, or an unset HOME has the hook create
# /.claude on the way past.
now="$(date -u '+%s %Y-%m-%dT%H:%M:%SZ')"
now_epoch="${now%% *}"
now_iso="${now#* }"
sid="$(field .session_id)"
marker_dir="${HOME:-}/.claude/wall-post-reminders"
[ -n "${HOME:-}" ] && mkdir -p "$marker_dir" 2>/dev/null || true

hook_exit="end"
hook_sig="unreached"
hook_idle="unreached"
hook_commit="unreached"

# hook_record writes the record on the way out, from an EXIT trap, so that
# every exit above is covered by one line instead of by a printf at each of a
# dozen returns. It never writes to stdout — Claude Code parses that, and a
# stray line there is a broken hook. Every variable is read with a default,
# because the trap can fire before any of them exists (the hook runs under
# `set -u`). And nothing but the decisions is recorded: no found lines, no
# commit subjects, no message text. Which trigger decided what, and nothing
# about what it saw.
#
# The gap, named rather than papered over: a hook killed by its 10s budget
# never runs this trap, so the count is of Stops this hook finished, not of
# Stops Claude Code started. A TERM trap would be a gate nobody here could
# turn red without a fixture that races the timeout, and this repo does not
# believe a gate it has not seen fail.
hook_record() {
    [ -n "${HOME:-}" ] || return 0
    local iso="${now_iso:-}"
    local day="${iso%%T*}"   # 2026-09-03T21:14:07Z → 2026-09-03
    local month="${day%-*}"  #                      → 2026-09, no fork
    [ -n "$month" ] || return 0
    # A tab or a newline in the session id or the repo name would make this
    # record two, or eight fields; both come from outside this hook. The
    # reader skips and counts what it cannot parse, and a line it has to skip
    # is one this hook could have written whole.
    local s="${sid:-}"
    local r="${repo:-}"
    # The whole group is silenced, not the printf alone: a redirection that
    # fails — the marker dir replaced by a plain file, say — is reported by
    # the shell before a later `2>/dev/null` on the same command could
    # apply, and the trap that promised silence would be the one thing on
    # stderr (found in review, 2026-09-03). One printf, one write, O_APPEND:
    # two Stops ending in the same instant do not tear each other's line on
    # a local filesystem, and the reader skips and counts a torn one should
    # it ever see it.
    {
        printf '%s\t%s\t%s\texit=%s\tsig=%s\tidle=%s\tcommit=%s\n' \
            "$iso" "${s//[$'\t\n']/ }" "${r//[$'\t\n']/ }" \
            "${hook_exit:-end}" "${hook_sig:-unreached}" \
            "${hook_idle:-unreached}" "${hook_commit:-unreached}" \
            >> "${marker_dir:-}/stops-$month.log"
    } 2>/dev/null || true
    return 0
}
trap hook_record EXIT

# The documented loop-breaker: never block a stop that a block already caused.
[ "$(field .stop_hook_active)" = "true" ] && { hook_exit="loop"; exit 0; }

command -v wallii >/dev/null 2>&1 || { hook_exit="no-wallii"; exit 0; }
command -v jq     >/dev/null 2>&1 || { hook_exit="no-jq"; exit 0; }

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { hook_exit="no-git"; exit 0; }

# Must resolve to the same name `wallii post` records, or the comparison is
# against the wrong repo's posts. Mirrors gitRepoName() in post.go: a session
# worktree resolves to the main checkout, because that is the repo the work
# belongs to.
common_dir="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
if [ -n "$common_dir" ] && [ "$(basename "$common_dir")" = ".git" ]; then
    repo="$(basename "$(dirname "$common_dir")")"
else
    toplevel="$(git rev-parse --show-toplevel 2>/dev/null || true)"
    [ -n "$toplevel" ] || { hook_exit="no-repo"; exit 0; }
    repo="$(basename "$toplevel")"
fi
[ -n "$repo" ] || { hook_exit="no-repo"; exit 0; }

# ── Session bookkeeping ──────────────────────────────────────────────────
# Shared by every trigger below. The session id names the per-session
# markers, so a path separator in it would let hook input write outside the
# marker dir — refused outright. The .start file is the session clock's
# zero point, written at the first Stop, which is the earliest moment this
# hook exists: the idle trigger measures elapsed time from it, the signature
# trigger takes the last commit before it as the diff base.
#
# The zero point has to age, or it stops measuring this session. A session id
# outlives a pause — Claude Code keeps it across --resume and across a night
# — and the first version wrote .start exactly once, so a session taken up
# again measured from a zero point hours or days old. Two consequences, and
# the first is the expensive one: the signature trigger would take the last
# commit before that ancient point as its diff base and report every
# signature committed in between, work this session never touched, written
# into the marker `wallii post` reads for its signals field. A wrong
# measurement that can no longer be subtracted from the post — the class the
# P0 of 2026-09-02 was fixed against. The idle trigger merely fires on its
# first Stop with an inflated duration.
#
# Measured over 101 live session markers on 2026-09-02: 13 pairs of .start
# and a later marker of the SAME session more than 8h apart, the worst 20h
# (zero point 00:11, hook still writing at 20:13), and 7 sessions with a
# pause over 8h in their transcript that kept firing afterwards.
#
# 8h is a decision, not a derivation: the bound `wallii post` already uses to
# discard a derived took (maxAutoTook, post.go) — above it a night sits in
# the gap, so it is no longer one stretch of work. An unreadable value counts
# as absent and is rewritten; the alternative is a clock nobody can read
# silently freezing the base. Deliberately NOT renewed with it: the -idle.done
# and .shortcut markers. Both mean "already asked, and the answer was
# respected", and renewing them would ask again for an answer already given —
# silence is the safe direction here, a wrong number is not.
# sid, marker_dir and its mkdir come from the record block at the top — the
# record needs them before the first guard. What stays here is the refusal.
case "${sid:-x}" in *[/\\]*) hook_exit="bad-sid"; exit 0 ;; esac

# Nothing ever removed a marker. 186 files, 540 KB in the 12 days since the
# hook went live (measured 2026-09-02), ~15 MB a year. That is untidiness
# rather than a defect — every marker is opened by its exact name, nothing
# here ever lists or scans the directory, so the count costs no time
# anywhere — and it is swept at Stop rather than by a cron job because this
# hook already runs then (3 ms over 186 files), and a second mechanism is a
# second thing that can break on its own without saying so.
#
# 30 days: a marker is only ever read inside its own session, and the effect
# review works in 14-day windows. The price, named rather than discovered
# later: a session left open longer than 30 days loses its zero point and
# its dedup. After the aging below that is free — the zero point is renewed
# past 8h in any case, and the sweep runs first, so a swept .start is
# rewritten three lines further down — but before it, it would have been a
# regression. Which is why the aging landed first, and this second.
#
# The monthly protocol is the exception, and it is kept a year: a marker is
# read only inside its own session, but the record is a series, and a series
# with a hole in it answers a different question than the one it was asked.
# Deleted here it would never be written again — the month is gone — so the
# loss would be silent and total.
#
# The outer parentheses carry that exception. Implicit AND binds tighter than
# -o in find, so `A -o B -delete` means `A -o ( B -delete )`: without the
# grouping the sweep quietly stops deleting markers and takes only the old
# protocol, which is precisely backwards. Both directions are proven (cases
# 23a and 23b) because a wrong grouping turns one of them green.
#
# Absolute path. This hook widens its own PATH for wallii and jq, but a find
# that is not found sweeps nothing and says nothing about it, and a gate
# that switches itself off in silence is the failure mode this repo has
# already been bitten by.
[ -d "$marker_dir" ] && /usr/bin/find "$marker_dir" -type f \
    \( \( ! -name 'stops-*' -mtime +30 \) -o \( -name 'stops-*' -mtime +365 \) \) \
    -delete 2>/dev/null || true

startfile="$marker_dir/${sid:-nosession}.start"
start_max=$((8 * 3600))
fresh=""
if [ -f "$startfile" ]; then
    prev="$(cut -d' ' -f1 "$startfile" 2>/dev/null || true)"
    case "${prev:-x}" in
        ''|*[!0-9]*) ;;
        *) [ $(( now_epoch - prev )) -lt "$start_max" ] && fresh=1 ;;
    esac
fi
if [ -z "$fresh" ]; then
    # `$now` is already `<epoch> <iso>`, which is what this file has always
    # held — the same two values, read once instead of forked for twice.
    printf '%s' "$now" > "$startfile" 2>/dev/null || true
fi

# ── Signature trigger ────────────────────────────────────────────────────
# Fires on an occurrence, not on absence: an added line since this session
# started that reads like a way around a check. It goes first — not out of
# priority, but because answering it answers the other two as well: the post
# it asks for moves the repo's last post (silences the commit trigger) and
# the actor's (silences the idle trigger). The reverse does not hold — a
# commit-trigger post leaves the shortcut unrecorded, and once the session
# ends the diff base is gone for good. The other two are absences and can
# wait a Stop; at one hit per ~141 commits that costs them almost nothing.
#
# Range: the last commit whose COMMITTER date precedes the .start time (the
# same trap as --since: an author-only backdate is invisible to --before),
# up to the working tree — the shortcut lives there before it is committed.
# Untracked files count too; the index is never touched. Commits made before
# a session's first Stop sit below the base: that is the .start file's
# resolution, and it is accepted. Paths are excluded, never included — an
# inclusion list silently misses a language.
#
# Mechanics, each chosen against a measured trap: rename detection stays on,
# or a moved test file renders as a wall of added lines and fires on every
# skip it already had. awk keeps position only — the hunk header's $3, not a
# regex, because `sub(/^.*\+/,"")` is greedy and breaks on
# `@@ -10,0 +11,2 @@ func add(a + b)` — and grep does the matching, so no
# pattern is ever assembled from data. No \b: a GNU extension, BSD spells it
# [[:<:]]. No head cap: it would blind the gate in exactly the repos where
# it matters; whoever wants it off switches it off loudly. Cost measured on a
# week of history: rev-list ≈ 5 ms, the -U0 diff 48–54 ms.
#
# The marker holds LINE CONTENTS — not SHAs, not line numbers — so a
# signature that survives an edit above it keeps its identity instead of
# nagging again under a new number. It is touched whenever the scan ran to
# completion, findings or not: "exists, empty" later reads as measured and
# nothing found, "no file" as nobody measured.
#
# WALLII_REMIND_SHORTCUTS is how many signature lines the diff must hold
# before the hook asks (default 1); 0 switches the trigger off. It gates the
# asking, never the measuring: findings are recorded at any threshold.
shortcuts="${WALLII_REMIND_SHORTCUTS:-1}"
# An unreadable value used to take the whole trigger down without a word:
# `[ on -gt 0 ]` fails, base stays empty, nothing is scanned and no marker
# is written, so every post of that session reads as "nobody measured". A
# gate that switches itself off in silence is the failure mode this repo
# has already been bitten by — say it and fall back, 0 is the off switch.
case "$shortcuts" in
    ''|*[!0-9]*)
        printf 'wall-post-remind: WALLII_REMIND_SHORTCUTS=%s is not a number — using 1. Set it to 0 to switch the signature trigger off.\n' "$shortcuts" >&2
        shortcuts=1
        ;;
esac
top="$(git rev-parse --show-toplevel 2>/dev/null || true)"
start_iso="$(cut -d' ' -f2 "$startfile" 2>/dev/null || true)"
base=""
if [ "$shortcuts" -gt 0 ] && [ -n "$top" ] && [ -n "$start_iso" ]; then
    base="$(git rev-list -1 --before="$start_iso" HEAD 2>/dev/null || true)"
    # a repo born in this session has no such commit: everything in it is new
    [ -n "$base" ] || base="$(git hash-object -t tree /dev/null 2>/dev/null || true)"
fi
# Switched off is a different fact from never reached, and a scan with no
# base is a third: the record keeps all three apart, or a quiet week of
# `sig=off` would read as a quiet week of clean diffs.
if [ "$shortcuts" -eq 0 ]; then
    hook_sig="off"
elif [ -z "$base" ]; then
    hook_sig="nobase"
fi
if [ -n "$base" ]; then
    tab=$'\t'
    excl=(':(exclude,glob)**/vendor/**' ':(exclude,glob)**/node_modules/**'
          ':(exclude,glob)**/third_party/**' ':(exclude,glob)**/*.lock'
          ':(exclude,glob)**/go.sum' ':(exclude,glob)**/*-lock.json')
    # Every added line as `path<TAB>number<TAB>content`, content trimmed and
    # inner tabs squeezed to spaces so a record is always three fields.
    # Untracked files go through the same parser as one-sided --no-index
    # diffs — which also skips binaries for free. LC_ALL=C for the text
    # stages: under the UTF-8 locale a hook inherits, BSD awk aborts on the
    # first Latin-1 byte and the scan ends early — measured, one repo's
    # history stopped at 76k of 170k lines and a real skip went unreported.
    added="$( export LC_ALL=C; {
        git -C "$top" diff -U0 -M --no-prefix --no-color --no-ext-diff "$base" -- . "${excl[@]}" 2>/dev/null
        git -C "$top" ls-files --others --exclude-standard -z -- . "${excl[@]}" 2>/dev/null \
            | while IFS= read -r -d '' f; do
                git -C "$top" diff -U0 --no-prefix --no-color --no-ext-diff --no-index /dev/null "$f" 2>/dev/null
              done
    } | awk '
        /^diff --git / { inhunk = 0; next }
        !inhunk && /^\+\+\+ / {
            path = substr($0, 5)
            # Prose names signatures for a living. A `t.Skip(` in a README
            # is documentation, and the first firing inside the harness
            # proved it twice: this repo README and the hook mirrored into
            # dotfiles. Line numbers keep counting, only the finding drops.
            prose = (path ~ /\.(md|markdown|txt|rst|adoc)$/)
            next
        }
        /^@@ / { split($3, a, ","); n = substr(a[1], 2) + 0; inhunk = 1; next }
        inhunk && /^\+/ {
            line = substr($0, 2)
            gsub(/\t/, " ", line); sub(/^ +/, "", line); sub(/[ \r]+$/, "", line)
            if (line != "" && !prose) print path "\t" n "\t" line
            n++; next
        }
        inhunk && /^ / { n++ }
    ')"

    # ── signature patterns ── BSD-safe ERE over the three-field records; the
    # calibration script sources this block verbatim, keep it self-contained.
    # Every pattern is pinned to the last field with [^<tab>]*$ — otherwise a
    # path supplies the match: tests/fish.bats … || true read as a bats gate
    # (20 of 60 class-B hits in calibration), README.md as an env var.
    last="[^$tab]*\$"
    # A · a test switched off. The env guard is the key: a reason that names
    #     the ENVIRONMENT (an env var, testing.Short, "requires docker", the
    #     sandbox) is a guard; one that names the test itself is a dodge.
    sig_a="(t\.Skip(f|Now)?\(|pytest\.(mark\.)?(skip|xfail)|unittest\.skip|(it|test|describe)\.(skip|only)\(|(^|[^A-Za-z0-9_])x(it|test|describe)\(|#\[ignore)$last"
    guard_env="([A-Z][A-Z0-9_]{2,}|testing\.Short\(\)|-short)$last"
    guard_words="(requires|needs|missing|not installed|unavailable|no network|offline|not available|no docker|no browser|in CI|on CI|sandbox)$last"
    # B · a gate told to pass: a NAMED build/test/lint command with || true
    #     on the same line (bare || true is idiom under set -u), a CI step
    #     allowed to fail, a hook bypassed.
    gates='go (test|vet|build)|golangci-lint|staticcheck|npm (test|run (test|lint|build))|pnpm (test|lint|build)|yarn (test|lint)|pytest|python -m pytest|ruff|mypy|cargo (test|clippy|build)|make (test|lint|check)|shellcheck|bats|swift test|xcodebuild|eslint|tsc|prettier --check'
    sig_b="(($gates)[^$tab]*\\|\\| *true|continue-on-error: *true|--no-verify)$last"
    # C · a checker overruled — soundness checkers only, never style
    sig_c="(type: *ignore|@ts-ignore|@ts-expect-error|//nolint)$last"
    # D · a test commented out: comment char, WHITESPACE, a test declaration
    sig_d="$tab(//|#|/\\*|--) +(func Test|def test_|(it|test|describe)\\(|#\\[test\\]|@Test)$last"
    # ── end of signature patterns ──

    # A line that IS a comment carries no active skip, gate or override —
    # classes A–C skip content that starts with a comment marker. The first
    # firing inside the harness (2026-09-02) was this file's own header,
    # mirrored into dotfiles: `t.Skip(` in prose read as a skipped test.
    # `#[` stays in (Rust's #[ignore] is an attribute, not a comment) and
    # class D is exempt by definition — a commented-out test IS the finding.
    comment="$tab(#([^[!]|\$)|//|/\\*|\\*( |\$))[^$tab]*\$"

    # A signature INSIDE a quote or a backtick is a line that NAMES one, not
    # one that runs it: this scanner's own pattern definitions, a push gate
    # printing `--no-verify` in its refusal, a log line about a step with
    # continue-on-error. All three sat on the live wall on 2026-09-02, and
    # checks repeats — and class A too, once the trigger fired on this
    # repo's own proof suite: a suite for a signature scanner necessarily
    # holds lines that PRODUCE signatures. The first version exempted class
    # A with an argument that ran backwards ("t.Skip( carries its anchor
    # outside the quotes, so the rule would blind it") — the rule tests
    # where the ANCHOR sits, and in `t.Skip("flaky")` it sits before the
    # quote, so a real skip is never touched. Only class D stays out: a
    # commented-out test is the finding, and it has no anchor to place.
    # The price is a gate hidden in a quoted value (`run: "npm test || true"`),
    # which now goes unseen — cheaper than a finding that cannot be taken
    # back off a post. The class travels as a prefix field and is stripped
    # again before the dedup, which compares path and content only.
    found="$( export LC_ALL=C SIG_A="$sig_a" SIG_B="$sig_b" SIG_C="$sig_c"; {
        printf '%s\n' "$added" | grep -E "$sig_a" | grep -Ev "$comment" | grep -Ev "$guard_env" | grep -iEv "$guard_words" | sed "s/^/A$tab/"
        printf '%s\n' "$added" | grep -E "$sig_b" | grep -Ev "$comment" | sed "s/^/B$tab/"
        printf '%s\n' "$added" | grep -E "$sig_c" | grep -Ev "$comment" | sed "s/^/C$tab/"
        printf '%s\n' "$added" | grep -E "$sig_d"
    } 2>/dev/null | awk -F"$tab" '
        # \047 \042 \140 are the quote, the double quote and the backtick —
        # spelled in octal so this stays inside single quotes in the shell.
        function quoted(s, p,   i, ch, sq, dq, bt) {
            for (i = 1; i < p; i++) {
                ch = substr(s, i, 1)
                if (ch == "\047") { if (!dq && !bt) sq = !sq }
                else if (ch == "\042") { if (!sq && !bt) dq = !dq }
                else if (ch == "\140") { if (!sq && !dq) bt = !bt }
            }
            return (sq || dq || bt)
        }
        NF == 4 && ($1 == "A" || $1 == "B" || $1 == "C") {
            re = ($1 == "A") ? ENVIRON["SIG_A"] : ($1 == "B") ? ENVIRON["SIG_B"] : ENVIRON["SIG_C"]
            # no match here means awk read the pattern differently than grep
            # did — keep the finding, the scanner is not the place to guess
            if (match($4, re) && quoted($4, RSTART)) next
            print $2 "\t" $3 "\t" $4
            next
        }
        { print }
    ' | awk '!seen[$0]++')"

    marker="$marker_dir/${sid:-nosession}-${repo}.shortcut"
    new=""
    n_new=0
    n_found=0
    while IFS="$tab" read -r p n c; do
        [ -n "$p" ] || continue
        n_found=$((n_found + 1))
        if [ -s "$marker" ] && LC_ALL=C grep -Fxq -e "$p$tab$c" "$marker" 2>/dev/null; then
            continue
        fi
        new="$new$p$tab$n$tab$c
"
        n_new=$((n_new + 1))
    done <<< "$found"
    touch "$marker" 2>/dev/null || true

    # The measurement is not the question. What the diff showed goes into the
    # marker whatever the threshold does: an existing empty marker says
    # "scanned, found nothing", and writing that while holding a finding back
    # is exactly the lie signal_src exists to prevent — the post would carry
    # a clean scan over a diff that was not clean.
    if [ "$n_new" -gt 0 ]; then
        printf '%s' "$new" | awk -F"$tab" '{ print $1 "\t" $3 }' >> "$marker" 2>/dev/null || true
    fi

    # The same three answers the marker gives, in the record: the scan ran
    # and found nothing, it found only lines already answered, or it found
    # something and the threshold below held the asking back.
    if [ "$n_found" -eq 0 ]; then
        hook_sig="clean"
    elif [ "$n_new" -eq 0 ]; then
        hook_sig="dedup"
    else
        hook_sig="held"
    fi

    # The threshold governs the asking alone, and it counts what the diff
    # holds rather than what is still unanswered — otherwise recording a
    # finding would deduplicate it out of its own count, and a threshold
    # above 1 could never be reached. The cost at >1: a finding recorded
    # while the hook stayed quiet is not repeated in the block that a later
    # one triggers. It is measured either way; only the asking is once.
    if [ "$n_new" -gt 0 ] && [ "$n_found" -ge "$shortcuts" ]; then
        hook_sig="fired"
        hook_exit="sig"
        list="$(printf '%s' "$new" | awk -F"$tab" 'NR <= 3 { printf "  %s:%s\n      %s\n", $1, $2, $3 }')"
        more=""
        [ "$n_new" -gt 3 ] && more="
  … and $((n_new - 3)) more"
        # The block asks for a field value, not for a verdict: three example
        # values, one of them "none — …", so a negative answer is prescribed,
        # equally short and equally acceptable. And the command template
        # carries no default grade — "--outcome ok --mood good" in a block
        # about shortcuts would pull the grade upward, the very degeneration
        # lint.go exists against.
        reason="The diff in \`$repo\` since this session started carries a line that reads like a way around a check rather than through it:

$list$more

That may well have been the right call. An environment guard, a check that was itself wrong, a trade made deliberately and for a reason — all of them look exactly like this from the outside, and none of them is a problem. Nothing here says the change is wrong.

What the diff cannot carry is the sentence beside it. It shows the skip; it never shows whether the test was skipped instead of fixed, or because fixing it was not the job. That sentence is what the next session needs — it inherits the code and reads the wall to find out what it must not rely on.

  wallii post -t <topic> --outcome <ok|partial|failed> --mood <great|good|ok|rough|stuck> \\
    --grader \"<the cheap path, taken or not>\" \"<what happened>\"

Any of these is a complete answer:
  --grader \"skipped the flaky auth test instead of fixing the race\"
  --grader \"considered raising the timeout, fixed the retry loop instead\"
  --grader \"none — the skip guards a missing binary, the assertions are unchanged\"

The field takes a refusal as readily as an admission; both are information, and \"none\" is a real answer rather than an empty one. Each signature is reported once — a line already answered stays quiet for the rest of the session."
        jq -n --arg r "$reason" '{decision:"block", reason:$r}'
        exit 0
    fi
fi

# ── Fail trigger ─────────────────────────────────────────────────────────
# The commit trigger below has a blind spot that guarantees survivor bias:
# failures rarely produce commits, so a wall fed only by commits can never
# learn the word "failed" (21 days, 347 posts, 0×failed). This asks the
# other question: the session has run for a while, produced zero commits,
# and put nothing on the wall — where did the time go? A dead end IS a unit
# of work. Fires once per session, and only asks — deciding "still mid-work"
# is respected like everywhere else in this hook.
#
# One chain, not a nest: every step is the reason the next one was not
# reached, and the record names exactly the step that ended it. The nesting
# this replaces said the same thing in three shapes and could report none of
# them.
idle_min="${WALLII_REMIND_IDLE_MIN:-45}"
idle_marker="$marker_dir/${sid:-nosession}-idle.done"
# The clock is read only where a step below could use it: switched off and
# already asked are decided without it, and two `cut` forks on every Stop
# after the question was answered would be the record costing what it
# promised not to.
start_epoch=""
start_iso=""
if [ "$idle_min" -gt 0 ] 2>/dev/null && [ ! -f "$idle_marker" ]; then
    start_epoch="$(cut -d' ' -f1 "$startfile" 2>/dev/null || true)"
    start_iso="$(cut -d' ' -f2 "$startfile" 2>/dev/null || true)"
    # A zero point no comparison can read is an absent one, and `$(( ))` on
    # it would abort the arithmetic and say so on stderr. The aging block
    # above rewrites such a file; this is what happens in the Stop where it
    # could not.
    case "${start_epoch:-x}" in ''|*[!0-9]*) start_epoch="" ;; esac
fi
if ! [ "$idle_min" -gt 0 ] 2>/dev/null; then
    hook_idle="off"
elif [ -f "$idle_marker" ]; then
    hook_idle="asked"
elif [ -z "$start_epoch" ] || [ -z "$start_iso" ]; then
    hook_idle="noclock"
elif [ $(( (now_epoch - start_epoch) / 60 )) -lt "$idle_min" ]; then
    hook_idle="young"
else
    commits_since_start="$(git log --since="$start_iso" --oneline 2>/dev/null | grep -c . || true)"
    actor="${WALLII_ACTOR:-manual}"
    if [ -n "${WALLII_ROLE:-}" ]; then
        case "$actor" in */"${WALLII_ROLE}") ;; *) actor="$actor/$WALLII_ROLE" ;; esac
    fi
    last_post_ts="$(wallii tail --actor "$actor" -n 1 --json 2>/dev/null | jq -r '.ts // empty' 2>/dev/null || true)"
    posted_since_start=0
    if [ -n "$last_post_ts" ] && [[ "$last_post_ts" > "$start_iso" ]]; then
        posted_since_start=1
    fi
    if ! [ "${commits_since_start:-1}" -eq 0 ] 2>/dev/null; then
        hook_idle="committed"
    elif [ "$posted_since_start" -eq 1 ]; then
        hook_idle="posted"
    else
        hook_idle="fired"
        hook_exit="idle"
        printf '%s' 'done' > "$idle_marker" 2>/dev/null || true
        mins=$(( (now_epoch - start_epoch) / 60 ))
        reason="This session has run ${mins}m in \`$repo\` with zero commits and nothing on the wall from $actor.

If an approach died in that time — a dead end, a rabbit hole, a rollback — that IS a finished unit of work, and it belongs on the wall exactly because no commit will ever tell the story:
  wallii post --topic fix --outcome failed --mood rough \"<what was tried, what killed it>\"
(or --topic obituary for a proper eulogy — failures with dignity are the posts worth rereading.)

If this is still mid-work, research, or a planning session, say so and stop — this asks once per session and never again."
        jq -n --arg r "$reason" '{decision:"block", reason:$r}'
        exit 0
    fi
fi

# How many commits may accumulate before silence becomes a finding. A unit of
# work is routinely several commits, so 1 would nag on every normal turn; on
# the days above the gap ran to dozens. 3 is the smallest number that cannot
# be a single unit still in progress.
threshold="${WALLII_REMIND_AFTER:-3}"

last_ts="$(wallii tail --repo "$repo" -n 1 --json 2>/dev/null | jq -r '.ts // empty' 2>/dev/null || true)"
if [ -n "$last_ts" ]; then
    since="$last_ts"
    window="since the last post for $repo ($last_ts)"
else
    # Never posted: a fresh clone must not report its entire history, so only
    # today's work counts. This is the blind-repo case the reminder exists for.
    since="24 hours ago"
    window="in the last 24h ($repo has never been posted to the wall)"
fi

commits="$(git log --since="$since" --oneline 2>/dev/null | grep -c . || true)"
[ -n "$commits" ] || { hook_commit="nocount"; exit 0; }
[ "$commits" -ge "$threshold" ] 2>/dev/null || { hook_commit="under"; exit 0; }

# Report once per HEAD. An unchanged HEAD stays silent, so a session that
# deliberately leaves work unposted is told once, not on every turn — but the
# next commit makes it a new finding again.
head_sha="$(git rev-parse HEAD 2>/dev/null || true)"
[ -n "$head_sha" ] || { hook_commit="nohead"; exit 0; }
# sid and marker_dir come from the record block at the top of the file
marker="$marker_dir/${sid:-nosession}-${repo}.sha"
if [ -f "$marker" ] && [ "$(cat "$marker" 2>/dev/null)" = "$head_sha" ]; then
    hook_commit="dedup"
    exit 0
fi
printf '%s' "$head_sha" > "$marker" 2>/dev/null || true
hook_commit="fired"
hook_exit="commit"

subjects="$(git log --since="$since" --pretty=format:'  %h %s' 2>/dev/null | head -8)"
more=""
[ "$commits" -gt 8 ] && more="
  … and $((commits - 8)) more"

reason="$commits commit(s) in \`$repo\` $window, and nothing went on the wall:

$subjects$more

The wall is how anyone reconstructs what happened across repos; work that is not on it did not happen as far as any later session, digest or stats run can tell. This is the gap wallii's own design note predicts for anything a convention file merely asks for — 23% of commits landed on days the wall was blind.

Post the finished unit(s) now — one line each, failures included:
  wallii post --repo $repo --topic <topic> --outcome <ok|partial|failed> --ref <commit-url> \"<what actually happened>\"

Judge it by the work, not by the commit count: several commits are often one unit, and one unit is one post. If this work genuinely does not belong on the wall (mechanical bumps, someone else's commits, still mid-unit), say so and stop — this will not repeat until the next commit."

jq -n --arg r "$reason" '{decision:"block", reason:$r}'
exit 0
