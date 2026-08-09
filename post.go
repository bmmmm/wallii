// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func cmdPost(args []string) error {
	fs := flag.NewFlagSet("post", flag.ExitOnError)
	repo := fs.String("r", "", "repo name (default: current git repo)")
	topic := fs.String("t", "", "short topic tag, e.g. ci, release, fix")
	actor := fs.String("a", "", `who posts (default: $WALLII_ACTOR or "manual")`)
	var refs multiFlag
	fs.Var(&refs, "ref", "commit/issue/PR URL (repeatable)")
	fs.Parse(args)

	msg := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *repo == "" {
		*repo = gitRepoName()
	}
	if *actor == "" {
		*actor = os.Getenv("WALLII_ACTOR")
	}
	if *actor == "" {
		*actor = "manual"
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	e := wall.Event{TS: time.Now().UTC(), Repo: *repo, Actor: *actor, Topic: *topic, Msg: msg, Refs: refs}
	if err := wall.Append(dir, e); err != nil {
		return err
	}
	// opportunistic housekeeping: gzip finished months without a cron job
	if _, err := wall.Archive(dir, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "wallii: archive (non-fatal):", err)
	}
	return nil
}

func gitRepoName() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(string(out)))
}
