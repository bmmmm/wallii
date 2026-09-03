// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rec builds one protocol line in the shape the hook writes it. Invented
// sessions and repos throughout — nothing here comes off the real machine.
func rec(iso, sid, repo, exit, sig, idle, commit string) string {
	return strings.Join([]string{iso, sid, repo, "exit=" + exit, "sig=" + sig, "idle=" + idle, "commit=" + commit}, "\t")
}

// stopLog writes a protocol file into a fresh dir and returns the dir.
func stopLog(t *testing.T, name string, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func counted(counts []NameCount, name string) int {
	for _, c := range counts {
		if c.Name == name {
			return c.Count
		}
	}
	return 0
}

// The number the protocol exists for: how many Stops reached a trigger at
// all. Two of these four never got past a guard, and a firing counter would
// have reported the idle trigger's single firing against all four.
func TestFoldTriggersCountsReachAgainstEveryStop(t *testing.T) {
	dir := stopLog(t, "stops-2026-09.log",
		rec("2026-09-01T08:00:00Z", "s1", "webshop", "loop", "unreached", "unreached", "unreached"),
		rec("2026-09-01T09:00:00Z", "s2", "", "no-wallii", "unreached", "unreached", "unreached"),
		rec("2026-09-01T10:00:00Z", "s3", "webshop", "idle", "clean", "fired", "unreached"),
		rec("2026-09-01T11:00:00Z", "s4", "webshop", "end", "clean", "young", "under"),
	)
	stops, read, err := ReadStops(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	tr := FoldTriggers(stops, read)
	if tr.Stops != 4 || tr.Reached != 2 {
		t.Fatalf("reached %d of %d stops, want 2 of 4", tr.Reached, tr.Stops)
	}
	if counted(tr.Idle, "unreached") != 2 || counted(tr.Idle, "fired") != 1 || counted(tr.Idle, "young") != 1 {
		t.Fatalf("idle states = %v, want 2 unreached, 1 fired, 1 young", tr.Idle)
	}
	if counted(tr.Exit, "loop") != 1 || counted(tr.Exit, "no-wallii") != 1 {
		t.Fatalf("exit states = %v, want the two guards named apart", tr.Exit)
	}
	if !tr.First.Equal(stops[0].TS) || !tr.Last.Equal(stops[3].TS) {
		t.Errorf("window %s → %s does not span the records", tr.First, tr.Last)
	}
	if read.Bad != 0 {
		t.Errorf("clean protocol reported %d bad lines", read.Bad)
	}
}

// A word the hook learned and this reader did not must be counted under its
// own name. Folding it into a known bucket would let a new state read as
// "condition false" — the exact confusion the protocol exists against, turned
// on the reader itself. And an unknown sig word means the block ran, so the
// Stop counts as reached.
func TestFoldTriggersKeepsAnUnknownStateUnderItsOwnName(t *testing.T) {
	dir := stopLog(t, "stops-2026-09.log",
		rec("2026-09-02T08:00:00Z", "s1", "webshop", "sig", "quarantined", "unreached", "unreached"),
		rec("2026-09-02T08:30:00Z", "s2", "webshop", "end", "clean", "off", "under"),
	)
	stops, read, err := ReadStops(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	tr := FoldTriggers(stops, read)
	if counted(tr.Sig, "quarantined") != 1 {
		t.Fatalf("sig states = %v, want quarantined counted under its own name", tr.Sig)
	}
	if counted(tr.Sig, "unreached") != 0 || counted(tr.Sig, "clean") != 1 {
		t.Errorf("an unknown word landed in a known bucket: %v", tr.Sig)
	}
	if tr.Reached != 2 {
		t.Errorf("reached = %d of 2 — an unknown sig word means the block ran", tr.Reached)
	}
	// off is a fact of its own, never the same as never having run
	if counted(tr.Idle, "off") != 1 || counted(tr.Idle, "unreached") != 1 {
		t.Errorf("idle states = %v, want off and unreached kept apart", tr.Idle)
	}
}

// The doctrine of parseLines, one file over: a line that cannot be read is
// skipped and counted, never fatal, and never allowed to take the records
// around it with it. Every shape below is one field of the contract.
func TestReadStopsSkipsAndCountsBrokenLines(t *testing.T) {
	good := rec("2026-09-02T09:00:00Z", "s1", "webshop", "end", "clean", "off", "under")
	dir := stopLog(t, "stops-2026-09.log",
		"garbage without any tabs at all",
		rec("2026-09-02T08:00:00Z", "s0", "webshop", "end", "clean", "off", "under")+"\textra=1",
		"2026-09-02T08:10:00Z\ts0\twebshop\tclean\toff\tunder\tend", // no prefixes
		"not-a-timestamp\ts0\twebshop\texit=end\tsig=clean\tidle=off\tcommit=under",
		"2026-09-02T08:20:00Z\ts0\twebshop\texit=\tsig=clean\tidle=off\tcommit=under",
		"",
		good,
	)
	stops, read, err := ReadStops(dir, time.Time{})
	if err != nil {
		t.Fatalf("a broken protocol must not be fatal: %v", err)
	}
	if len(stops) != 1 || stops[0].SID != "s1" {
		t.Fatalf("got %d records %v, want the one readable line", len(stops), stops)
	}
	if read.Bad != 5 {
		t.Errorf("bad = %d, want 5 — a blank line is not a broken one", read.Bad)
	}
	if read.Files != 1 {
		t.Errorf("files = %d, want 1", read.Files)
	}
}

// No protocol is not zero Stops. The reader has to hand the caller the
// difference — an empty file list — or the command above it prints "0
// firings" for a hook that was never installed.
func TestReadStopsTellsNoProtocolFromNoStops(t *testing.T) {
	empty := t.TempDir()
	stops, read, err := ReadStops(empty, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) != 0 || read.Files != 0 {
		t.Fatalf("empty dir = %d stops from %d files, want nothing at all", len(stops), read.Files)
	}

	dir := stopLog(t, "stops-2026-09.log",
		rec("2026-09-02T09:00:00Z", "s1", "webshop", "end", "clean", "off", "under"))
	stops, read, err = ReadStops(dir, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) != 0 || read.Files != 1 {
		t.Fatalf("windowed-out = %d stops from %d files, want 0 from 1 — a file was read, nothing was in range", len(stops), read.Files)
	}
}

// Months are separate files and the window cuts across them; the records come
// back in time order whatever the file names did.
func TestReadStopsSpansMonthsAndHonoursSince(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, lines ...string) {
		t.Helper()
		body := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stops-2026-09.log",
		rec("2026-09-01T10:00:00Z", "s3", "webshop", "end", "clean", "off", "under"))
	write("stops-2026-08.log",
		rec("2026-08-31T10:00:00Z", "s1", "webshop", "loop", "unreached", "unreached", "unreached"),
		rec("2026-08-31T23:00:00Z", "s2", "api-gateway", "commit", "clean", "posted", "fired"))
	// a marker in the same directory is not a protocol file
	if err := os.WriteFile(filepath.Join(dir, "s1-webshop.shortcut"), []byte("a_test.go\tt.Skip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stops, read, err := ReadStops(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if read.Files != 2 {
		t.Fatalf("files = %d, want 2 — only stops-*.log is protocol", read.Files)
	}
	if len(stops) != 3 || stops[0].SID != "s1" || stops[2].SID != "s3" {
		t.Fatalf("records = %v, want three in time order", stops)
	}
	since := time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)
	stops, _, err = ReadStops(dir, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) != 2 || stops[0].SID != "s2" {
		t.Fatalf("windowed records = %v, want the two at or after %s", stops, since)
	}
}

// The fields carry what the hook wrote, unchanged — the session and the repo
// are how a record is traced back to the work it describes.
func TestParseStopKeepsTheRecordAsWritten(t *testing.T) {
	s, ok := ParseStop(rec("2026-09-02T09:00:00Z", "sess-42", "webshop", "sig", "fired", "unreached", "unreached"))
	if !ok {
		t.Fatal("a well-formed record did not parse")
	}
	if s.SID != "sess-42" || s.Repo != "webshop" || s.Exit != "sig" || s.Sig != "fired" {
		t.Fatalf("record = %+v", s)
	}
	if s.Reached() != true {
		t.Error("a fired signature trigger must count as reached")
	}
	if !s.TS.Equal(time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %s", s.TS)
	}
	// a guard exit leaves every trigger unreached, and that is not a firing
	// of "unreached" — it is the absence of a decision
	g, ok := ParseStop(rec("2026-09-02T09:01:00Z", "sess-42", "", "no-jq", "unreached", "unreached", "unreached"))
	if !ok || g.Reached() {
		t.Errorf("guard record = %+v (ok=%v), want it parsed and unreached", g, ok)
	}
}
