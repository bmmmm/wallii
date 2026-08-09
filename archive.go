// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

func cmdArchive(args []string) error {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	fs.Parse(args)
	dir, err := wall.Dir()
	if err != nil {
		return err
	}
	done, err := wall.Archive(dir, time.Now())
	for _, f := range done {
		fmt.Println("archived:", f)
	}
	if err != nil {
		return err
	}
	if len(done) == 0 {
		fmt.Println("nothing to archive")
	}
	return nil
}
