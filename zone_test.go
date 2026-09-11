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

// A misspelled zone is an error and never a silent fallback: a dashboard
// stamped with the wrong zone reports wrong days and says nothing about it.
func TestResolveZoneRejectsAnUnknownName(t *testing.T) {
	for _, key := range []string{"WALLII_TZ", "TZ"} {
		_, err := resolveZone(envOf(map[string]string{key: "Europe/Berln"}))
		if err == nil {
			t.Fatalf("%s=Europe/Berln resolved without error", key)
		}
		if !strings.Contains(err.Error(), "Europe/Berlin") || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s error %q names neither the variable nor a usable example", key, err)
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
