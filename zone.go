// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The timezone the reports are written in. It lives in package main, next to
// gitTimeout(), for the same reason: it reads the environment, and the fold
// in internal/wall must not. There, loc is a parameter and never time.Local
// — that is what keeps a day boundary testable, and naming a machine's zone
// here is exactly the step that would soften it.
//
// Nothing falls back to time.Local, because time.Local cannot be named:
// Local.String() is the literal "Local" and its Zone() is an abbreviation
// like "CEST". Both are rejected by Intl.DateTimeFormat, so a dashboard
// stamped with either ships a script that throws on load. A zone we cannot
// name is an error, loudly, at the point where the name is needed.

// reportZone resolves the zone every report is rendered in, once per process.
//
// Order: WALLII_TZ, then $TZ, then the machine — /etc/localtime's symlink
// target, then /etc/timezone. Every candidate is verified with LoadLocation
// and the resolved Location is the one returned, so the name in the output
// and the offsets behind it are always the same zone.
var reportZone = sync.OnceValues(func() (*time.Location, error) {
	return resolveZone(os.Getenv)
})

// resolveZone is reportZone's body with the environment handed in, so a test
// can walk the whole order without a process restart — sync.OnceValues caches
// for the life of the process, which is right for a command and useless for a
// table test.
func resolveZone(getenv func(string) string) (*time.Location, error) {
	for _, key := range []string{"WALLII_TZ", "TZ"} {
		// A leading colon is allowed in TZ and is not part of the name.
		v := strings.TrimPrefix(strings.TrimSpace(getenv(key)), ":")
		if v == "" || v == "Local" {
			continue
		}
		loc, err := time.LoadLocation(v)
		if err != nil {
			return nil, fmt.Errorf("%s=%q: unknown timezone — an IANA name like Europe/Berlin", key, v)
		}
		return loc, nil
	}
	if name, ok := zoneFromLocaltimeLink(); ok {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, nil
		}
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(b)); name != "" {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc, nil
			}
		}
	}
	return nil, fmt.Errorf("could not name this machine's timezone — set WALLII_TZ=Europe/Berlin")
}

// inZone renders an instant in the report zone — the display counterpart of
// the boundaries the measurements are cut at. Every .Local() in the commands
// went through here, so `wallii tail` and `wallii dash` cannot print two
// different hours for the same post.
//
// A zone that cannot be resolved is fatal where a measurement depends on it,
// but not here: tail runs inside the Stop hook's ten-second budget and a
// dashboard's stamp is not worth a failed command. It falls back to UTC and
// says so once, which is not the same as saying nothing.
func inZone(t time.Time) time.Time {
	loc, err := reportZone()
	if err != nil {
		warnNoZone(err)
		return t.UTC()
	}
	return t.In(loc)
}

// one warning per process, not one per formatted timestamp
var warnedNoZone sync.Once

func warnNoZone(err error) {
	warnedNoZone.Do(func() {
		fmt.Fprintf(os.Stderr, "wallii: %v — showing times in UTC\n", err)
	})
}

// zoneFromLocaltimeLink reads the IANA name out of /etc/localtime's symlink
// target: everything after the last "zoneinfo/" segment. The file's contents
// are the compiled zone and carry no name, which is why the link is read
// instead of the data.
func zoneFromLocaltimeLink() (string, bool) {
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return "", false
	}
	target = filepath.ToSlash(target)
	i := strings.LastIndex(target, "zoneinfo/")
	if i < 0 {
		return "", false
	}
	name := strings.TrimPrefix(target[i+len("zoneinfo/"):], "/")
	if name == "" {
		return "", false
	}
	return name, true
}
