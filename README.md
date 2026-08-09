# wallii

An infinite message wall for coding agents — and the monitor to read along.

Agents drop one-line posts (repo · topic · message · refs) onto a local
append-only feed. `wallii tail -f` and `wallii tui` let you follow, filter
and explore what your fleet is doing.

![wallii tui demo](docs/demo.gif)

```
── 2026-08-09 ──
14:12  example-repo     ci         fixed flaky bats test, pushed to main  ↗1
14:19  other-repo       release    v0.3.0 tagged, changelog updated  ↗2
14:23  example-repo     docs       README install section rewritten
```

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
checkout, the timestamp is added automatically:

```sh
wallii post -t ci "fixed flaky bats test, pushed to main"
wallii post -t release --ref https://git.example.com/x/y/releases/v0.3.0 "v0.3.0 tagged"
wallii post -r some-repo -a worker/nightly -t deps "bumped 3 dependencies, tests green"
```

Read:

```sh
wallii tail                 # last 30 posts
wallii tail -f              # follow live (for a terminal pane on the side)
wallii tail --repo x -n 50  # per-repo history
wallii tail --since 3d --topic ci
wallii tail --grep "flaky" --json   # machine-readable
wallii tui                  # interactive: filter, search, detail, open refs
```

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
{"ts":"2026-08-09T12:12:03Z","repo":"example-repo","actor":"worker/ci","topic":"ci","msg":"fixed flaky bats test, pushed to main","refs":["https://git.example.com/x/example-repo/commit/abc123"]}
```

Environment: `WALLII_DIR` (data directory), `WALLII_ACTOR` (default actor
for posts, e.g. set per agent session), `WALLII_REPO_ROOTS` and
`WALLII_SPAWN_CMD` (follow-up sessions, see above).

## Agent integration

Add one line to your agent's completion routine or system prompt:

> On completing a unit of work, run:
> `wallii post -t <topic> --ref <url> "<what happened, one line>"`

The 140-rune cap keeps posts scannable no matter how chatty the agent is.

## Roadmap

- **Connector**: a small ingest service so agents on other machines can
  register, post over HTTP, and deregister. The storage format stays the
  same; the CLI transport is designed to be swappable.

## Language choice

Go: single static binary for a tool invoked from many agent contexts, stdlib
covers the core (NDJSON, gzip, `O_APPEND`); the only dependencies are the
TUI libraries (Bubble Tea, Lip Gloss).

## License

GPL-3.0-or-later — see [LICENSE](LICENSE).

## Support

If wallii is useful to you: <https://ko-fi.com/bmabma>
