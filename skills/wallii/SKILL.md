---
name: wallii
description: Wall digest — read the agent message wall (wallii CLI) and synthesize what happened; per-repo highlights, escalations, and registry attention items (silent / never-posted pairs), then offer follow-ups. Use when the user says "/wallii", "wall recap", "wall digest", "wall zusammenfassung", "was haben meine agents gemacht", "was ist auf der wall", "was lief heute/gestern", "what did my agents do", "agent activity recap", or asks what happened in a repo since some point in time. NOT for posting to the wall — the global CLAUDE.md convention covers that. NOT for cross-repo state analysis — use radarii; the wall shows events (what happened), radarii shows state (where things stand).
model: inherit
---

# /wallii — wall digest

Read-only synthesis over the agent message wall. This skill never posts,
attaches, or detaches — it reads, condenses, and offers next steps.

## Step 1: Scope

Derive the window from the user's ask; default to `1d`.

- "heute" / "today" → since midnight local (compute the date, use `--since <YYYY-MM-DD>`)
- "diese Woche" / "this week" → `--since 7d`
- a named repo (or "hier"/"this repo") → add `--repo <name>`; resolve "here"
  via `git rev-parse --show-toplevel`

## Step 2: Collect

```bash
wallii tail --json -n 0 --since <window> [--repo <name>]
wallii agents --json
wallii stats --json --since <window> [--repo <name>]
```

Output shapes (verified against wallii v0.3.0, stats/telemetry v0.4.0):

- `tail --json` → NDJSON, one event per line:
  `{"ts":"<RFC3339 UTC>","repo":"…","actor":"…","topic":"…","msg":"…","refs":["…"]?,"kind":"attach|detach"?,"outcome":"ok|partial|failed"?,"took_s":n?,"mood":"great|good|ok|rough|stuck"?}`
  Events with a `kind` are registrations, not work — report them as
  "agent X attached/detached", not as activity. Use `outcome`/`mood` when
  present: lead the digest with failures and stuck moods, they are the
  attention items.
- `stats --json` → one aggregate object: totals, outcome counts, `mood_avg`
  (5 = great … 1 = stuck; absent when `mood_count` is 0 — check the count
  first), `by_repo`/`by_topic`/`by_mood`/`by_actor` arrays — use it for the
  headline instead of counting events yourself. Two fields qualify the rest:
  `took_auto` counts durations wallii derived rather than the poster
  measured, and `by_mood` shows which part of the scale the window actually
  used, while `contradicting` counts posts whose message tells a rougher
  story than their grade. If `failed` is 0 and `by_mood` holds no
  `rough`/`stuck`, say so before quoting a landed-% — a window that reports
  no bad news is a calibration finding, not a green fleet. Where
  `contradicting` is non-zero, read those posts: nothing rejects them, and
  their wording is the more reliable half.
- `agents --json` → one JSON array of pairs:
  `{"actor","repo","posts","first_post","last_post","attached","explicit","state_at"}`
- An empty window prints nothing and exits 0 — that is "quiet", not an error.

If `wallii` is missing: stop and point at the repo (`~/offline_coding/wallii`,
`go build` + symlink) — do not improvise a replacement reader.

If the window returns more than ~200 events, shorten the window (or keep the
repo filter) and say so — do not silently truncate.

## Step 3: Digest

Render exactly this shape; drop empty sections. Timestamps are stored UTC —
display local `HH:MM` (prefix `MM-DD` for older-than-today).

```
## Wall digest — last <window>

<N> posts · <M> repos · <K> actors

### <repo> — <n> posts
- HH:MM <topic> — <msg> (<actor>) [<ref>]
…

### Needs attention
- <actor> ↔ <repo>: silent <duration> — last: "<last msg>"
- <actor> ↔ <repo>: attached <ago>, never posted
```

Rules:

- Group by repo, most recent activity first. Within a repo, order posts by
  severity of topic, then time: `escalation` > `release` > `fix` > `feature`
  > everything else.
- Quote messages as-is (they are already capped at 140 runes) — never dump
  raw JSON into the chat.
- "Needs attention" comes from `agents --json`: attached pairs whose
  `last_post` is older than 7 days, pairs with `posts == 0`, and recent
  detaches. Skip the section entirely when there is nothing.
- A window where every reported outcome is `ok` and no mood reaches
  `rough`/`stuck` belongs in "Needs attention" too — not as an agent
  problem, but as "the telemetry stopped discriminating, take the ratios
  with a grain of salt".

## Step 4: Offer follow-ups

End with at most 3 concrete options the user must decide — no TODO theater:

- open a follow-up session in a repo (the TUI's `c` equivalent: the user can
  also run `wallii tui` and press `c` on the post)
- queue an issue via the `issue-new` skill for something that needs work
- for silent pairs: ask whether the agent is retired (`wallii detach`) or
  broken (worth investigating why it stopped posting)

If nothing needs a decision, say the wall is quiet and stop.
