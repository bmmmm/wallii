// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestResolveZonePrefersWalliiTZThenTZ(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"WALLII_TZ wins", map[string]string{"WALLII_TZ": "Asia/Tokyo", "TZ": "Europe/Berlin"}, "Asia/Tokyo"},
		{"TZ when WALLII_TZ is unset", map[string]string{"TZ": "Europe/Berlin"}, "Europe/Berlin"},
		{"TZ when WALLII_TZ is blank", map[string]string{"WALLII_TZ": "  ", "TZ": "UTC"}, "UTC"},
		{"a leading colon is not part of the name", map[string]string{"TZ": ":America/Havana"}, "America/Havana"},
		{`TZ="Local" is not a name and is skipped`, map[string]string{"TZ": "Local"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := resolveZone(envOf(tc.env))
			if tc.want == "" {
				// falls through to the machine; all we assert is that it
				// did not accept the unusable name
				if err == nil && loc.String() == "Local" {
					t.Fatalf("resolved to %q — that name cannot be loaded anywhere else", loc)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveZone: %v", err)
			}
			if loc.String() != tc.want {
				t.Fatalf("resolved %q, want %q", loc, tc.want)
			}
		})
	}
}

// A misspelled WALLII_TZ is an error and never a silent fallback: it was set
// to configure wallii, so a name that does not load is a mistake, and a
// dashboard stamped with the wrong zone reports wrong days without saying so.
func TestResolveZoneRejectsAnUnknownWalliiTZ(t *testing.T) {
	_, err := resolveZone(envOf(map[string]string{"WALLII_TZ": "Europe/Berln"}))
	if err == nil {
		t.Fatal("WALLII_TZ=Europe/Berln resolved without error")
	}
	if !strings.Contains(err.Error(), "Europe/Berlin") || !strings.Contains(err.Error(), "WALLII_TZ") {
		t.Fatalf("the error %q names neither the variable nor a usable example", err)
	}
}

// $TZ is the opposite case and it took a review to see it: TZ is inherited
// rather than chosen for wallii, and its POSIX forms are legal — Go's own
// time.Local reads them, LoadLocation does not. Treated as a hard error,
// every reading command exited 1 on an ordinary machine while `wallii post`
// kept writing: the wall filled and nothing could read it. An unusable TZ
// falls through to the machine.
func TestResolveZoneFallsThroughAnUnusableTZ(t *testing.T) {
	for _, v := range []string{
		"CET-1CEST,M3.5.0,M10.5.0/3", // the POSIX form, entirely legal
		"UTC0",
		"/etc/localtime",
		"Europe/Berln", // and a plain typo in a variable that is not ours
	} {
		loc, err := resolveZone(envOf(map[string]string{"TZ": v}))
		if err != nil {
			t.Errorf("TZ=%q made wallii unusable: %v", v, err)
			continue
		}
		if loc == nil {
			t.Errorf("TZ=%q resolved to no zone at all", v)
		}
	}
}

// The resolved zone must be nameable: this is the property time.Local does
// not have, and the one the browser needs. Intl.DateTimeFormat throws on
// "Local" and on abbreviations like "CEST".
func TestResolveZoneReturnsANameableZone(t *testing.T) {
	loc, err := resolveZone(envOf(nil))
	if err != nil {
		t.Skipf("this machine cannot name its own zone: %v", err)
	}
	name := loc.String()
	if name == "Local" || name == "" || !strings.Contains(name, "/") && name != "UTC" {
		t.Fatalf("resolved %q — not an IANA name", name)
	}
	if _, err := time.LoadLocation(name); err != nil {
		t.Fatalf("resolved %q, which does not load back: %v", name, err)
	}
}

// The machine's own answer must agree with what Go picked as time.Local when
// no TZ is set — if it does not, every report silently moves by an offset.
func TestResolveZoneAgreesWithTimeLocal(t *testing.T) {
	if os.Getenv("TZ") != "" {
		t.Skip("TZ is set in this environment — time.Local is that zone by definition")
	}
	loc, err := resolveZone(envOf(nil))
	if err != nil {
		t.Skipf("this machine cannot name its own zone: %v", err)
	}
	at := time.Now()
	gotName, gotOff := at.In(loc).Zone()
	wantName, wantOff := at.In(time.Local).Zone()
	if gotOff != wantOff || gotName != wantName {
		t.Fatalf("resolved %s → %s %+d, but time.Local is %s %+d", loc, gotName, gotOff, wantName, wantOff)
	}
}
