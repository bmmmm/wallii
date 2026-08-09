// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
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

// spawnSession runs the WALLII_SPAWN_CMD template with the resolved dir and
// prompt passed via environment — no string splicing, so quotes in the
// message cannot break out of the command.
func spawnSession(dir, prompt string) error {
	tmpl := os.Getenv("WALLII_SPAWN_CMD")
	if tmpl == "" {
		return fmt.Errorf("WALLII_SPAWN_CMD is not set")
	}
	cmd := exec.Command("/bin/sh", "-c", tmpl)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"WALLII_SPAWN_DIR="+dir,
		"WALLII_SPAWN_PROMPT="+prompt,
	)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap — the spawner runs detached from the TUI
	return nil
}

// sessionCmd renders a paste-ready shell command for the post's repo.
func sessionCmd(dir, prompt string) string {
	c := "claude " + shellQuote(prompt)
	if dir == "" {
		return c
	}
	return "cd " + shellQuote(dir) + " && " + c
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
