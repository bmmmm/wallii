// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ts(day, hour int) time.Time {
	return time.Date(2026, 8, day, hour, 0, 0, 0, time.UTC)
}

func post(t *testing.T, dir string, at time.Time, repo, topic, msg string) {
	t.Helper()
	e := Event{TS: at, Repo: repo, Actor: "test", Topic: topic, Msg: msg}
	if err := Append(dir, e); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestAppendReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	post(t, dir, ts(9, 10), "example-repo", "ci", "first")
	post(t, dir, ts(9, 11), "other-repo", "fix", "second")
	post(t, dir, ts(9, 12), "example-repo", "release", "third")

	got, bad, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if bad != 0 {
		t.Fatalf("unexpected malformed count %d", bad)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	if got[0].Msg != "first" || got[2].Msg != "third" {
		t.Fatalf("wrong order: %+v", got)
	}
	if got[1].Repo != "other-repo" || got[1].Topic != "fix" {
		t.Fatalf("fields lost: %+v", got[1])
	}
}

func TestReadLastLimitAndMatch(t *testing.T) {
	dir := t.TempDir()
	post(t, dir, ts(9, 10), "example-repo", "ci", "ci one")
	post(t, dir, ts(9, 11), "example-repo", "docs", "docs one")
	post(t, dir, ts(9, 12), "example-repo", "ci", "ci two")

	got, _, err := ReadLast(dir, 1, func(e Event) bool { return e.Topic == "ci" })
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].Msg != "ci two" {
		t.Fatalf("want newest ci event only, got %+v", got)
	}
}

func TestMalformedLinesAreCountedNotFatal(t *testing.T) {
	dir := t.TempDir()
	post(t, dir, ts(9, 10), "example-repo", "ci", "good")
	f, err := os.OpenFile(CurrentFile(dir, ts(9, 10)), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{not json}\n")
	f.Close()

	got, bad, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || bad != 1 {
		t.Fatalf("want 1 event + 1 bad line, got %d events, %d bad", len(got), bad)
	}
}

func TestArchiveCompressesFinishedMonths(t *testing.T) {
	dir := t.TempDir()
	july := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	post(t, dir, july, "example-repo", "ci", "from july")
	post(t, dir, ts(9, 10), "example-repo", "ci", "from august")

	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	done, err := Archive(dir, now)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(done) != 1 || done[0] != "wall-2026-07.ndjson.gz" {
		t.Fatalf("unexpected archive result: %v", done)
	}
	if _, err := os.Stat(filepath.Join(dir, "wall-2026-07.ndjson")); !os.IsNotExist(err) {
		t.Fatal("plain july file still present after archive")
	}

	got, _, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatalf("read after archive: %v", err)
	}
	if len(got) != 2 || got[0].Msg != "from july" || got[1].Msg != "from august" {
		t.Fatalf("archive lost data: %+v", got)
	}
}

func TestArchiveLeavesCurrentAndFreshMonths(t *testing.T) {
	dir := t.TempDir()
	july := time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC)
	post(t, dir, july, "example-repo", "ci", "late july")
	post(t, dir, ts(9, 10), "example-repo", "ci", "august")

	// 00:30 on Aug 1st: July is over but not by >1h — nothing may be touched
	early := time.Date(2026, 8, 1, 0, 30, 0, 0, time.UTC)
	done, err := Archive(dir, early)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(done) != 0 {
		t.Fatalf("archived too eagerly: %v", done)
	}
}

func TestAppendRefusesArchivedMonth(t *testing.T) {
	dir := t.TempDir()
	july := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	post(t, dir, july, "example-repo", "ci", "from july")
	if _, err := Archive(dir, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("archive: %v", err)
	}
	e := Event{TS: july, Repo: "example-repo", Msg: "backdated"}
	if err := Append(dir, e); err == nil {
		t.Fatal("backdated append into archived month accepted")
	}
}

func TestFilesPrefersPlainOverGz(t *testing.T) {
	dir := t.TempDir()
	june := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	post(t, dir, june, "example-repo", "ci", "june")
	plain := CurrentFile(dir, june)
	if err := gzipFile(plain); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	// both plain and .gz now exist — simulates an interrupted archive run
	files, err := Files(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], "wall-2026-06.ndjson") {
		t.Fatalf("dedupe failed: %v", files)
	}
	got, _, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("duplicate month read: %d events", len(got))
	}
}
