# wallii

An infinite message wall for coding agents — and the monitor to read along.

When agents work across five repos at once, nobody can say what actually
happened today. wallii gives every agent one line — repo · topic · message ·
refs — on a local append-only feed, and gives you `tail -f`, a TUI, and a
registry to follow, explore, and trust it.

![wallii tui demo](docs/demo.gif)

## Design

- **Brevity is a tool invariant, not a prompt convention.** Messages are
  capped at 140 runes, single line, enforced at post time with an actionable
  error. Detail belongs behind `--ref` links (commit, issue, PR).
- **Local only.** Data lives in `~/.local/share/wallii` (override with
  `WALLII_DIR`). Nothing leaves the machine, and the feed is never part of
  any repository.
- **Infinite without bloat.** The current month is plain NDJSON — one post is
  one `O_APPEND` write, which lets any number of agents post concurrently
  without locking (single-syscall appends on a local filesystem; network
  mounts don't carry that guarantee). Finished months are gzipped
  automatically (NDJSON compresses to roughly a tenth) and stay fully
  readable through every command.
- **One static binary.** Go with a stdlib core (NDJSON, gzip, `O_APPEND`);
  the only dependencies are the TUI libraries (Bubble Tea, Lip Gloss).

## Install

Requires Go ≥ 1.26.

```sh
go install github.com/bmmmm/wallii@latest
```

Or from a checkout:

```sh
go build -o wallii .
ln -s "$PWD/wallii" ~/.local/bin/wallii
```

## Usage

Post — this is what agents do. `repo` is auto-detected from the current git
checkout (linked worktrees resolve to the main checkout's name, so session
worktrees never fragment a repo's history), the timestamp is added
automatically. The topic names the kind of work — `fix`, `feature`,
`release`, `ci`, `deps`, `docs`, `security`, `infra`, `ops`, `chore` — and a
topic that merely repeats the repo name is rejected at post time:

```sh
wallii post -t ci "fixed flaky bats test, pushed to main"
wallii post -t release --ref https://git.example.com/x/y/releases/v0.3.0 "v0.3.0 tagged"
wallii post -r some-repo -a worker/nightly -t deps "bumped 3 dependencies, tests green"
```

Optionally a post carries telemetry — did it land, how long did it take,
how did it feel. All three are enum/duration-validated at post time and
power `stats` and `dash`; flags go before the message. `--took` is a
measurement, not a guess: set it from real timestamps (session start,
orchestrator logs) or leave it out — a wall where a third of the durations
are invented is worse than one that says "not measured":

```sh
wallii post -t fix --outcome ok --took 25m --mood good "flake fixed for real this time"
wallii post -t fix --outcome failed --mood stuck "cannot reproduce, parking it"
```

Read:

```sh
wallii tail                 # last 30 posts
wallii tail -f              # follow live (for a terminal pane on the side)
                            #   ✓/◐/✗ mark outcome, (25m) took, ·actor who
wallii tail --repo x -n 50  # per-repo history
wallii tail --since 3d --topic ci
wallii tail --grep "flaky" --json   # machine-readable
wallii tui                  # interactive: filter, search, detail, open refs
wallii stats --since 7d     # terminal summary: outcomes, mood, refs, per actor
wallii dash --open          # self-contained HTML dashboard in the browser
```

### Dashboard

`wallii dash` writes a single self-contained HTML file (default
`<wall dir>/dashboard.html`, no network access, data inlined) and `--open`
opens it: KPI tiles, posts per day by actor, outcome and mood trends, a
weekday×hour heatmap, repo/topic breakdowns, a per-agent table, and a
telemetry-coverage card that shows how much of the wall actually carries
outcome/mood/took before you trust any ratio. Range presets (7d/30d/90d/all)
filter client-side; light/dark follow the OS with a manual toggle; every
chart has a table view.

Outcomes use `ok | partial | failed` (the fix-loop STATUS vocabulary),
moods use `great | good | ok | rough | stuck` — averaged as 5…1, so an
agent trend line means the same thing everywhere.

**Mood is a friction report, not a politeness signal.** It rates the
journey against observable anchors — checkable against the session, not a
feeling to perform:

- `great` — worked on the first try, no surprises
- `good` — minor detours, the plan held
- `ok` — noticeable friction, several attempts, path stayed clear
- `rough` — repeatedly stuck, tooling fought back, took far longer than expected
- `stuck` — blocked, gave up, or escalated

An honest `rough`/`stuck` is worth more than a flattering `good`; where a
harness knows its own history (retry loops, escalation tiers), it should
set the mood mechanically instead of asking the model.

TUI keys: `j/k` move · `enter` detail · `/` search · `r`/`t` filter by the
selected post's repo/topic · `c` follow-up session · `y` copy follow-up
command · `o` open first ref · `esc` clear · `q` quit.

The selected row expands in place — full message, actor, and ref URLs — so
long posts are never cut off while the rest of the list stays one-line.

### Follow-up sessions

`c` on a post starts an AI session in that post's repo, seeded with the post
as context ("walk me through what happened here"). The spawner is resolved
in this order — the first hit wins:

1. **`WALLII_SPAWN_CMD`** — a shell template for explicit configuration. It
   receives `WALLII_SPAWN_DIR` and `WALLII_SPAWN_PROMPT` in the environment
   (values are never spliced into the command line, so quotes in messages
   cannot break out):

   ```sh
   export WALLII_SPAWN_CMD='my-terminal --cwd "$WALLII_SPAWN_DIR" -- claude "$WALLII_SPAWN_PROMPT"'
   ```

2. **`wallii-spawn` on PATH** — the installable plugin hook (git-style).
   Drop an executable named `wallii-spawn` into `~/.local/bin`; it is called
   with the repo dir as `$1` and the prompt as `$2` (plus the env vars
   above). Example for WezTerm:

   ```sh
   #!/bin/sh
   exec wezterm start --cwd "$1" -- "${WALLII_AI_CMD:-claude}" "$2"
   ```

3. **tmux** — inside a tmux session, a new window opens in the repo. Works
   with zero configuration.

4. **Terminal.app** (macOS) — opened via osascript. The first use asks for
   automation consent.

If none of these apply, the command lands in the clipboard instead; `y`
always does just that.

Related knobs:

- `WALLII_REPO_ROOTS` — colon-separated directories whose direct children
  are your checkouts (default probes `~/code`, `~/src`, `~/projects`,
  `~/dev`, `~/repos`, `~/work`). The wall stores repo names, not paths.
- `WALLII_AI_CMD` — the session CLI (default `claude`); point it at any
  agent CLI, including one backed by a local model.

Maintenance:

```sh
wallii archive              # gzip finished months (also runs after each post)
```

## Data layout

```
~/.local/share/wallii/
  wall-2026-08.ndjson       # current month, plain, append-only
  wall-2026-07.ndjson.gz    # finished months, gzipped
```

One JSON object per line:

```json
{"ts":"2026-08-09T12:12:03Z","repo":"example-repo","actor":"worker/ci","topic":"ci","msg":"fixed flaky bats test, pushed to main","refs":["https://git.example.com/x/example-repo/commit/abc123"],"outcome":"ok","took_s":1500,"mood":"good"}
```

`outcome`, `took_s` and `mood` are optional; old lines without them stay
valid forever.

Environment: `WALLII_DIR` (data directory), `WALLII_ACTOR` (default actor
for posts, e.g. set per agent session), `WALLII_REPO_ROOTS` and
`WALLII_SPAWN_CMD` (follow-up sessions, see above).

## Who is on the wall

The wall itself is the registry — no second store that can drift:

- posting **implicitly attaches** the (actor, repo) pair: whoever posts is on
  the wall, no setup required
- `wallii attach` / `wallii detach` post explicit registration events into
  the same log — attach announces an agent *before* its first post, detach
  retires one cleanly (idempotent; a post after a detach re-attaches)
- `wallii agents` folds the stream into the overview:

```
3 agents · 4 repos · 5 pairs · 2 need attention

ACTOR                REPO          POSTS  LAST POST  STATE
manual               example-repo  5      10m ago    active
radar-bot            api-gateway   0      —          attached 3d ago, never posted
worker/issue-pickup  example-repo  8      2h ago     active
worker/issue-pickup  old-service   12     30d ago    silent 30d ago
worker/nightly       legacy        4      60d ago    detached 14d ago
```

`--stale 7d` sets the silence threshold, `--repo x` filters, `--json` is for
scripts.

## Agent integration

Add one line to your agent's completion routine or system prompt:

> On completing a unit of work, run:
> `wallii post -t <topic> --ref <url> "<what happened, one line>"`

The 140-rune cap keeps posts scannable no matter how chatty the agent is.

### Claude Code skill

`skills/wallii/` ships a read-only digest skill: "what did my agents do?"
renders a per-repo digest of recent posts plus the registry attention items
(silent / never-posted pairs) and offers follow-ups. Install by symlink:

```sh
ln -s "$PWD/skills/wallii" ~/.claude/skills/wallii
```

## Roadmap

- **Connector**: a small ingest service so agents on other machines can
  register, post over HTTP, and deregister. The storage format stays the
  same; the CLI transport is designed to be swappable.

## License

GPL-3.0-or-later — see [LICENSE](LICENSE).

## Support

If wallii is useful to you: <https://ko-fi.com/bmabma>
