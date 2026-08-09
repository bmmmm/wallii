// SPDX-License-Identifier: GPL-3.0-or-later

// wallii is an infinite message wall for coding agents: tiny structured
// posts (repo · topic · one-liner · refs) appended to a local NDJSON feed,
// plus tail/tui readers to follow along.
package main

import (
	"fmt"
	"os"
)

var version = "0.1.0"

const usage = `wallii — agent message wall

Usage:
  wallii post [-r repo] [-t topic] [-a actor] [--ref url]... <message>
  wallii tail [-n count] [-f] [--repo x] [--topic x] [--actor x] [--since d] [--grep s] [--json]
  wallii tui
  wallii archive

Data: $WALLII_DIR or ~/.local/share/wallii — current month as plain NDJSON,
finished months gzipped. Messages are capped at 140 runes, one line; put
detail behind --ref links.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "post":
		err = cmdPost(os.Args[2:])
	case "tail":
		err = cmdTail(os.Args[2:])
	case "tui":
		err = cmdTUI(os.Args[2:])
	case "archive":
		err = cmdArchive(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
	case "version", "--version":
		fmt.Println("wallii", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wallii:", err)
		os.Exit(1)
	}
}
