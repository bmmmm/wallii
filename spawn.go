// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bmmmm/wallii/internal/wall"
)

// defaultRepoRoots are common code directories probed when WALLII_REPO_ROOTS
// is unset.
var defaultRepoRoots = []string{"~/code", "~/src", "~/projects", "~/dev", "~/repos", "~/work"}

func repoRoots() []string {
	if v := os.Getenv("WALLII_REPO_ROOTS"); v != "" {
		return filepath.SplitList(v)
	}
	return defaultRepoRoots
}

// resolveRepoDir maps a wall repo name to a local checkout by probing each
// root for a direct child of that name. The wall stores only repo names —
// paths would not survive the planned multi-machine connector.
func resolveRepoDir(repo string) (string, bool) {
	if repo == "" {
		return "", false
	}
	home, _ := os.UserHomeDir()
	for _, root := range repoRoots() {
		if strings.HasPrefix(root, "~/") && home != "" {
			root = filepath.Join(home, root[2:])
		}
		dir := filepath.Join(root, repo)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir, true
		}
	}
	return "", false
}

// aiCmd is the CLI that runs a follow-up session — configurable so wallii
// works with any local or hosted agent CLI, not just one vendor.
func aiCmd() string {
	if v := os.Getenv("WALLII_AI_CMD"); v != "" {
		return v
	}
	return "claude"
}

// followUpPrompt is the first prompt for a session opened from a wall post.
func followUpPrompt(e wall.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Context: wall post from %s, repo %s", e.TS.Local().Format("2006-01-02 15:04"), e.Repo)
	if e.Topic != "" {
		fmt.Fprintf(&b, ", topic %s", e.Topic)
	}
	if e.Actor != "" {
		fmt.Fprintf(&b, ", by %s", e.Actor)
	}
	fmt.Fprintf(&b, ": %q.", e.Msg)
	for _, r := range e.Refs {
		fmt.Fprintf(&b, " Ref: %s", r)
	}
	b.WriteString(" Walk me through what happened here and show the relevant changes.")
	return b.String()
}

// errNoSpawner tells the caller to fall back to the clipboard.
var errNoSpawner = errors.New("no spawner available")

// spawnSession opens a follow-up session in dir, trying in order:
//  1. the WALLII_SPAWN_CMD shell template (explicit configuration)
//  2. a `wallii-spawn` executable on PATH (installable plugin, git-style)
//  3. a new tmux window when running inside tmux
//  4. Terminal.app via osascript on macOS
//
// Dir and prompt are passed as arguments/environment, never spliced into a
// command line, so quotes in messages cannot break out. Returns a short
// description of the path taken.
func spawnSession(dir, prompt string) (string, error) {
	env := append(os.Environ(),
		"WALLII_SPAWN_DIR="+dir,
		"WALLII_SPAWN_PROMPT="+prompt,
	)
	if tmpl := os.Getenv("WALLII_SPAWN_CMD"); tmpl != "" {
		cmd := exec.Command("/bin/sh", "-c", tmpl)
		cmd.Dir, cmd.Env = dir, env
		return "spawned via WALLII_SPAWN_CMD", start(cmd)
	}
	if plugin, err := exec.LookPath("wallii-spawn"); err == nil {
		cmd := exec.Command(plugin, dir, prompt)
		cmd.Dir, cmd.Env = dir, env
		return "spawned via wallii-spawn", start(cmd)
	}
	if os.Getenv("TMUX") != "" {
		if _, err := exec.LookPath("tmux"); err == nil {
			cmd := exec.Command("tmux", "new-window", "-c", dir, aiCmd()+" "+shellQuote(prompt))
			cmd.Env = env
			return "opened tmux window", start(cmd)
		}
	}
	if runtime.GOOS == "darwin" {
		// first use prompts for Terminal.app automation consent (macOS TCC)
		script := fmt.Sprintf("tell application \"Terminal\"\nactivate\ndo script %s\nend tell",
			appleScriptQuote(sessionCmd(dir, prompt)))
		return "opened Terminal.app", start(exec.Command("osascript", "-e", script))
	}
	return "", errNoSpawner
}

func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap — the spawner runs detached from the TUI
	return nil
}

// sessionCmd renders a paste-ready shell command for the post's repo.
func sessionCmd(dir, prompt string) string {
	c := aiCmd() + " " + shellQuote(prompt)
	if dir == "" {
		return c
	}
	return "cd " + shellQuote(dir) + " && " + c
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("pbcopy")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
