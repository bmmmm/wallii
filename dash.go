// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

//go:embed dash.html
var dashTemplate string

// dashEvent is the compact per-post tuple inlined into the dashboard; the
// browser does all aggregation so the range filter works offline.
type dashEvent struct {
	T     int64    `json:"t"` // unix ms
	Repo  string   `json:"repo"`
	Actor string   `json:"actor,omitempty"`
	Topic string   `json:"topic,omitempty"`
	Out   string   `json:"out,omitempty"`
	Mood  string   `json:"mood,omitempty"`
	Took  int64    `json:"took,omitempty"`
	Refs  []string `json:"refs,omitempty"`
	Msg   string   `json:"msg"`
}

func cmdDash(args []string) error {
	fs := flag.NewFlagSet("dash", flag.ExitOnError)
	outPath := fs.String("o", "", "output file (default: <wall dir>/dashboard.html)")
	sinceS := fs.String("since", "", "only inline posts newer than 2006-01-02, 36h or 3d (default: everything)")
	openIt := fs.Bool("open", false, "open the dashboard in the browser")
	fs.Parse(args)

	since, err := parseSince(*sinceS, time.Now())
	if err != nil {
		return err
	}
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	evs, rstats, err := wall.ReadLast(dir, 0, func(e wall.Event) bool {
		return e.Kind == "" && (since.IsZero() || !e.TS.Before(since))
	})
	if err != nil {
		return err
	}
	reportStats(rstats)

	out := make([]dashEvent, 0, len(evs))
	for _, e := range evs {
		out = append(out, dashEvent{
			T: e.TS.UnixMilli(), Repo: e.Repo, Actor: e.Actor, Topic: e.Topic,
			Out: e.Outcome, Mood: e.Mood, Took: e.TookS, Refs: e.Refs, Msg: e.Msg,
		})
	}
	// json.Marshal HTML-escapes < > & — safe to inline in a <script> block
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	stamp := time.Now().Format("2006-01-02 15:04")
	if *sinceS != "" {
		// the range buttons cannot reach past what was inlined — say so
		stamp += " · only posts since " + *sinceS + " included"
	}
	// substitute the stamp BEFORE the data: once user-controlled message text
	// is in the string, a literal "__GENERATED__" inside a post could be hit
	html := strings.Replace(dashTemplate, "__GENERATED__", stamp, 1)
	html = strings.Replace(html, "__WALLII_DATA__", string(data), 1)

	path := *outPath
	if path == "" {
		path = filepath.Join(dir, "dashboard.html")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		return err
	}
	fmt.Printf("dashboard: %s (%d posts inlined)\n", path, len(out))
	if *openIt {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, path).Start(); err != nil {
			return fmt.Errorf("could not open browser (%w) — open %s yourself", err, path)
		}
	}
	return nil
}
