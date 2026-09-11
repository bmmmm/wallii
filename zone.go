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
	// WALLII_TZ is ours: it is set to configure wallii, so a name that does
	// not load is a mistake and says so.
	if v := strings.TrimPrefix(strings.TrimSpace(getenv("WALLII_TZ")), ":"); v != "" && v != "Local" {
		loc, err := time.LoadLocation(v)
		if err != nil {
			return nil, fmt.Errorf("WALLII_TZ=%q: unknown timezone — an IANA name like Europe/Berlin", v)
		}
		return loc, nil
	}
	// TZ is inherited, not chosen for us, and its POSIX forms are legal:
	// TZ="CET-1CEST,M3.5.0,M10.5.0/3", TZ=UTC0, TZ=/etc/localtime. Go's own
	// time.Local handles them and LoadLocation does not. Refusing them made
	// every reading command exit 1 on a perfectly ordinary machine — and
	// `wallii post` kept writing, so the wall filled while nothing could
	// read it. An unloadable TZ falls through to the machine instead.
	if v := strings.TrimPrefix(strings.TrimSpace(getenv("TZ")), ":"); v != "" && v != "Local" {
		if loc, err := time.LoadLocation(v); err == nil {
			return loc, nil
		}
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
// Every command resolves the zone up front and returns the error, so this
// lenient path is unreachable from the CLI. The TUI is the exception: it
// never returns to a caller who could print an error, and dying mid-screen
// over a timezone is worse than a banner. reportLoc() is what it uses, and
// it is the one caller that sees the fallback.
func inZone(t time.Time) time.Time {
	return t.In(reportLoc())
}

// reportLoc is the zone without the error, for the TUI and for the folds it
// hands a loc to. It warns once and falls back to UTC — never to time.Local,
// which is the thing with no name.
func reportLoc() *time.Location {
	loc, err := reportZone()
	if err != nil {
		warnNoZone(err)
		return time.UTC
	}
	return loc
}

// zoneBanner is what the TUI puts on screen when the zone could not be
// named: stderr is behind the alt-screen and would never be seen.
func zoneBanner() string {
	if _, err := reportZone(); err != nil {
		return "timezone: " + err.Error() + " — times shown in UTC"
	}
	return ""
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
