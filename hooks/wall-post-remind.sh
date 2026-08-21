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
# Silent unless: wallii is installed, we are in a git repo, and commits have
# piled up past the threshold since that repo's last post. Reports once per
# HEAD — deciding not to post is respected until the next commit arrives.
set -u

# Hooks do not inherit an interactive shell's PATH. Every tool this needs lives
# outside the default one — wallii in ~/.local/bin, jq and git in Homebrew — and
# every lookup below fails *silently* (exit 0) when they are missing, which
# would leave the gate permanently and invisibly off. Verified: under
# `env -i PATH=/usr/bin:/bin` this hook stayed silent on a repo with 28 unposted
# commits. Widen the PATH first; a directory that does not exist costs nothing.
PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export PATH

INPUT="$(cat)"
field() { printf '%s' "$INPUT" | jq -r "$1 // empty" 2>/dev/null || true; }

# The documented loop-breaker: never block a stop that a block already caused.
[ "$(field .stop_hook_active)" = "true" ] && exit 0

command -v wallii >/dev/null 2>&1 || exit 0
command -v jq     >/dev/null 2>&1 || exit 0

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0

# Must resolve to the same name `wallii post` records, or the comparison is
# against the wrong repo's posts. Mirrors gitRepoName() in post.go: a session
# worktree resolves to the main checkout, because that is the repo the work
# belongs to.
common_dir="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
if [ -n "$common_dir" ] && [ "$(basename "$common_dir")" = ".git" ]; then
    repo="$(basename "$(dirname "$common_dir")")"
else
    toplevel="$(git rev-parse --show-toplevel 2>/dev/null || true)"
    [ -n "$toplevel" ] || exit 0
    repo="$(basename "$toplevel")"
fi
[ -n "$repo" ] || exit 0

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
[ -n "$commits" ] || exit 0
[ "$commits" -ge "$threshold" ] 2>/dev/null || exit 0

# Report once per HEAD. An unchanged HEAD stays silent, so a session that
# deliberately leaves work unposted is told once, not on every turn — but the
# next commit makes it a new finding again.
head_sha="$(git rev-parse HEAD 2>/dev/null || true)"
[ -n "$head_sha" ] || exit 0
sid="$(field .session_id)"
case "${sid:-x}" in *[/\\]*) exit 0 ;; esac
marker_dir="$HOME/.claude/wall-post-reminders"
marker="$marker_dir/${sid:-nosession}-${repo}.sha"
if [ -f "$marker" ] && [ "$(cat "$marker" 2>/dev/null)" = "$head_sha" ]; then
    exit 0
fi
mkdir -p "$marker_dir" 2>/dev/null || true
printf '%s' "$head_sha" > "$marker" 2>/dev/null || true

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
