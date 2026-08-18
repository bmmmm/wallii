// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"encoding/json"
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

	got, stats, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if stats.BadLines != 0 || len(stats.SkippedFiles) != 0 {
		t.Fatalf("unexpected stats %+v", stats)
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

func TestAppendPermissions(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "wall") // fresh so Append creates it itself
	post(t, dir, ts(9, 10), "example-repo", "ci", "private")

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 700", perm)
	}
	fi, err := os.Stat(CurrentFile(dir, ts(9, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
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
	f, err := os.OpenFile(CurrentFile(dir, ts(9, 10)), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{not json}\n")
	f.Close()

	got, stats, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || stats.BadLines != 1 {
		t.Fatalf("want 1 event + 1 bad line, got %d events, %+v", len(got), stats)
	}
}

// An unreadable file (corrupt gzip here) must be skipped and reported —
// never blank the whole wall (review finding #2).
func TestReadLastSkipsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	post(t, dir, ts(9, 10), "example-repo", "ci", "intact august")
	if err := os.WriteFile(filepath.Join(dir, "wall-2026-07.ndjson.gz"), []byte("garbage-not-gzip"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, stats, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].Msg != "intact august" {
		t.Fatalf("intact month lost: %+v", got)
	}
	if len(stats.SkippedFiles) != 1 || stats.SkippedFiles[0] != "wall-2026-07.ndjson.gz" {
		t.Fatalf("corrupt file not reported: %+v", stats)
	}
}

// One oversized line must cost exactly that line, not the month (review
// finding #3 — the old scanner died with "token too long").
func TestOversizeLineDoesNotBrickFile(t *testing.T) {
	dir := t.TempDir()
	post(t, dir, ts(9, 10), "example-repo", "ci", "before")
	f, err := os.OpenFile(CurrentFile(dir, ts(9, 10)), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"msg":"` + strings.Repeat("a", 2<<20) + `"}` + "\n")
	f.Close()
	post(t, dir, ts(9, 11), "example-repo", "ci", "after")

	got, stats, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || got[0].Msg != "before" || got[1].Msg != "after" {
		t.Fatalf("neighbor events lost: %+v", got)
	}
	if stats.BadLines != 1 {
		t.Fatalf("oversize line not counted: %+v", stats)
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
	if _, err := os.Stat(filepath.Join(dir, ".archive.lock")); !os.IsNotExist(err) {
		t.Fatal("lock file left behind")
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

// A held lock means another archiver is active: skip silently, touch
// nothing (review finding #1 — concurrent archive runs corrupted the .gz).
// Uses real time throughout: lock-staleness compares against the actual
// mtime the filesystem wrote.
func TestArchiveSkipsWhenLocked(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	past := now.AddDate(0, -2, 0) // safely a finished month
	post(t, dir, past, "example-repo", "ci", "old month")
	oldName := "wall-" + past.UTC().Format("2006-01") + ".ndjson"
	lock := filepath.Join(dir, ".archive.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	done, err := Archive(dir, now)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(done) != 0 {
		t.Fatalf("archived despite fresh lock: %v", done)
	}
	if _, err := os.Stat(filepath.Join(dir, oldName)); err != nil {
		t.Fatal("plain file touched despite lock")
	}

	// a stale lock (>15min) is broken so the NEXT run can proceed
	stale := now.Add(-20 * time.Minute)
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatal(err)
	}
	if done, _ := Archive(dir, now); len(done) != 0 {
		t.Fatalf("lock-breaking run must not archive itself: %v", done)
	}
	if done, _ := Archive(dir, now); len(done) != 1 {
		t.Fatalf("run after lock break should archive: %v", done)
	}
}

// Posting into an already-archived month (clock skew, backdated TS) must
// not lose the message — it lands in the live month instead.
func TestAppendArchivedMonthFallsBackToCurrent(t *testing.T) {
	dir := t.TempDir()
	july := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	post(t, dir, july, "example-repo", "ci", "from july")
	if _, err := Archive(dir, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("archive: %v", err)
	}

	e := Event{TS: july, Repo: "example-repo", Msg: "backdated"}
	if err := Append(dir, e); err != nil {
		t.Fatalf("backdated append lost: %v", err)
	}
	live, _, err := ParseFile(CurrentFile(dir, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range live {
		if ev.Msg == "backdated" {
			found = true
		}
	}
	if !found {
		t.Fatal("backdated event not in live month file")
	}
}

func TestFilesPrefersPlainOverGzAndValidatesNames(t *testing.T) {
	dir := t.TempDir()
	june := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	post(t, dir, june, "example-repo", "ci", "june")
	plain := CurrentFile(dir, june)
	if err := gzipFile(plain); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	// interrupted archive run: both plain and .gz exist → read plain only
	for _, junk := range []string{"wall-.ndjson", "wall-2026-13.ndjson", "wall-foo.ndjson", "wall-2026-6.ndjson"} {
		if err := os.WriteFile(filepath.Join(dir, junk), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := Files(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], "wall-2026-06.ndjson") {
		t.Fatalf("dedupe/name validation failed: %v", files)
	}
	got, _, err := ReadLast(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("duplicate month read: %d events", len(got))
	}
}

// A truncated or replaced file must reset the follow offset instead of
// hanging mid-file forever (review finding #5).
// Post-time lints call this on every write, so it must stay cheap and
// current-month-only: an actor posting for the first time would otherwise
// decompress every archived month just to learn it has no history.
func TestRecentByActorReadsCurrentMonthOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	last := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	mustAppend(t, dir, Event{TS: last, Repo: "x", Actor: "a", Msg: "previous month"})
	for i := 0; i < 5; i++ {
		mustAppend(t, dir, Event{TS: now.Add(time.Duration(i) * time.Minute), Repo: "x", Actor: "a", Msg: "mine"})
	}
	mustAppend(t, dir, Event{TS: now, Repo: "x", Actor: "b", Msg: "someone else"})
	mustAppend(t, dir, Event{TS: now, Repo: "x", Actor: "a", Msg: "attached", Kind: KindAttach})

	got, err := RecentByActor(dir, "a", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 own posts from the current month, got %d", len(got))
	}
	for _, e := range got {
		if e.Actor != "a" || e.Kind != "" || e.Msg == "previous month" {
			t.Errorf("unexpected event in result: %+v", e)
		}
	}
	if got[len(got)-1].TS.Before(got[0].TS) {
		t.Error("result must be oldest-first — the last element is the clock for --took")
	}
	if limited, err := RecentByActor(dir, "a", 2, now); err != nil || len(limited) != 2 {
		t.Errorf("limit ignored: got %d events, err %v", len(limited), err)
	} else if !limited[1].TS.Equal(got[4].TS) {
		t.Error("limit must keep the newest events, not the oldest")
	}
	// a wall that has never been written to is empty, not broken
	if evs, err := RecentByActor(t.TempDir(), "a", 0, now); err != nil || len(evs) != 0 {
		t.Errorf("missing month file: got %d events, err %v", len(evs), err)
	}
}

func mustAppend(t *testing.T, dir string, e Event) {
	t.Helper()
	if err := Append(dir, e); err != nil {
		t.Fatal(err)
	}
}

func TestDrainResetsOnShrink(t *testing.T) {
	dir := t.TempDir()
	post(t, dir, ts(9, 10), "example-repo", "ci", "one")
	post(t, dir, ts(9, 11), "example-repo", "ci", "two")
	path := CurrentFile(dir, ts(9, 10))

	evs, off := Drain(path, 0)
	if len(evs) != 2 {
		t.Fatalf("initial drain: %d events", len(evs))
	}

	// replace the file with shorter content (restore, manual edit, sync tool)
	e := Event{TS: ts(9, 12), Repo: "example-repo", Msg: "fresh"}
	line, _ := json.Marshal(e)
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	evs, _ = Drain(path, off)
	if len(evs) != 1 || evs[0].Msg != "fresh" {
		t.Fatalf("drain after shrink: %+v", evs)
	}
}
