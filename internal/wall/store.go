// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxLineBytes bounds a single wall line while reading. Valid lines are
// <10 KB after Validate's caps; anything bigger is counted as bad and
// skipped so one runaway write cannot brick the whole month.
const maxLineBytes = 1 << 20

// Dir returns the wall data directory: $WALLII_DIR or ~/.local/share/wallii.
func Dir() (string, error) {
	if d := os.Getenv("WALLII_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home dir (set WALLII_DIR): %w", err)
	}
	return filepath.Join(home, ".local", "share", "wallii"), nil
}

// CurrentFile is the plain NDJSON file receiving appends for t's month.
func CurrentFile(dir string, t time.Time) string {
	return filepath.Join(dir, "wall-"+t.UTC().Format("2006-01")+".ndjson")
}

// Append writes one validated event as a single NDJSON line. O_APPEND makes
// concurrent posters safe without locking: one line = one write syscall.
func Append(dir string, e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := CurrentFile(dir, e.TS)
	if _, err := os.Stat(path + ".gz"); err == nil {
		// the event's own month is already archived (clock skew, backdated
		// TS): losing the message would be worse than an out-of-order line,
		// so it goes into the live month instead
		path = CurrentFile(dir, time.Now())
		if _, err := os.Stat(path + ".gz"); err == nil {
			return fmt.Errorf("current month file %s has a .gz twin — wall dir is inconsistent, inspect %s", filepath.Base(path), dir)
		}
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	// fsync per post: posts are rare enough that durability is free
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if serr != nil {
		return serr
	}
	return cerr
}

// Files lists wall month files oldest-first, accepting only names with a
// valid YYYY-MM month. If a month exists both plain and gzipped
// (interrupted archive run), the plain file wins so no month is read twice.
func Files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	byMonth := map[string]string{}
	for _, en := range entries {
		name := en.Name()
		switch {
		case monthOf(name, ".ndjson") != "":
			byMonth[monthOf(name, ".ndjson")] = name
		case monthOf(name, ".ndjson.gz") != "":
			month := monthOf(name, ".ndjson.gz")
			if _, ok := byMonth[month]; !ok {
				byMonth[month] = name
			}
		}
	}
	months := make([]string, 0, len(byMonth))
	for m := range byMonth {
		months = append(months, m)
	}
	sort.Strings(months)
	out := make([]string, 0, len(months))
	for _, m := range months {
		out = append(out, filepath.Join(dir, byMonth[m]))
	}
	return out, nil
}

// monthOf extracts a validated YYYY-MM from "wall-YYYY-MM<suffix>", or "".
func monthOf(name, suffix string) string {
	if !strings.HasPrefix(name, "wall-") || !strings.HasSuffix(name, suffix) {
		return ""
	}
	m := strings.TrimSuffix(strings.TrimPrefix(name, "wall-"), suffix)
	if _, err := time.Parse("2006-01", m); err != nil {
		return ""
	}
	return m
}

// ParseFile parses one wall file (plain or gzipped). Malformed or oversized
// lines are skipped and counted, not fatal — one bad write must not brick
// the wall.
func ParseFile(path string) ([]Event, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		defer zr.Close()
		r = zr
	}
	evs, bad, err := parseLines(r)
	if err != nil {
		return evs, bad, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return evs, bad, nil
}

// parseLines reads NDJSON with a hard per-line cap. A trailing line without
// a newline is a write in progress and is silently left out.
func parseLines(r io.Reader) ([]Event, int, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var out []Event
	bad := 0
	var line []byte
	discard := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !discard {
			line = append(line, chunk...)
			if len(line) > maxLineBytes {
				discard = true
				bad++
			}
		}
		switch err {
		case bufio.ErrBufferFull:
			continue
		case nil:
			if !discard {
				trimmed := bytes.TrimSpace(line)
				if len(trimmed) > 0 {
					var e Event
					if json.Unmarshal(trimmed, &e) == nil {
						out = append(out, e)
					} else {
						bad++
					}
				}
			}
			line, discard = line[:0], false
		case io.EOF:
			return out, bad, nil
		default:
			return out, bad, err
		}
	}
}

// ReadStats reports what was skipped while reading the wall.
type ReadStats struct {
	BadLines     int
	SkippedFiles []string // unreadable files (corrupt gzip, I/O errors)
}

// ReadLast returns up to limit matching events (0 = no limit), oldest
// first. Month files are read newest-first so deep archives are only opened
// until the limit is met; match==nil accepts everything. An unreadable file
// is reported in stats and skipped — it must never blank the whole wall.
func ReadLast(dir string, limit int, match func(Event) bool) ([]Event, ReadStats, error) {
	var stats ReadStats
	files, err := Files(dir)
	if err != nil {
		return nil, stats, err
	}
	var chunks [][]Event
	total := 0
	for i := len(files) - 1; i >= 0; i-- {
		evs, bad, err := ParseFile(files[i])
		stats.BadLines += bad
		if err != nil {
			stats.SkippedFiles = append(stats.SkippedFiles, filepath.Base(files[i]))
			continue
		}
		if match != nil {
			kept := make([]Event, 0, len(evs))
			for _, e := range evs {
				if match(e) {
					kept = append(kept, e)
				}
			}
			evs = kept
		}
		chunks = append(chunks, evs)
		total += len(evs)
		if limit > 0 && total >= limit {
			break
		}
	}
	var out []Event
	for i := len(chunks) - 1; i >= 0; i-- {
		out = append(out, chunks[i]...)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, stats, nil
}

// Drain parses complete new lines past off and returns them with the new
// offset; a partially-written trailing line stays unconsumed until its
// newline lands. If the file shrank below off (replaced or truncated),
// reading restarts from the beginning instead of hanging mid-line forever.
func Drain(path string, off int64) ([]Event, int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, off
	}
	if fi.Size() < off {
		off = 0
	}
	if fi.Size() == off {
		return nil, off
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, off
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, off
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, off
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 {
		return nil, off
	}
	evs, _, _ := parseLines(bytes.NewReader(buf[:last+1]))
	return evs, off + int64(last) + 1
}

// Archive gzips finished months and removes the plain originals. A month is
// only touched once it has been over for >1h, so a concurrent poster still
// holding an old "current month" path cannot append to a file mid-compress.
// Runs are serialized via a lock file: every post triggers an opportunistic
// archive, and two archivers compressing the same file would race the
// remove step; the loser skips and the next post retries.
func Archive(dir string, now time.Time) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lock := filepath.Join(dir, ".archive.lock")
	lf, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			// break locks from killed archivers, but let the NEXT run
			// proceed — this one might be racing the dying owner
			if fi, serr := os.Stat(lock); serr == nil && now.Sub(fi.ModTime()) > 15*time.Minute {
				os.Remove(lock)
			}
			return nil, nil
		}
		return nil, err
	}
	lf.Close()
	defer os.Remove(lock)

	var done []string
	for _, en := range entries {
		name := en.Name()
		// sweep temp files from crashed archive runs
		if strings.Contains(name, ".ndjson.tmp-") {
			if fi, err := en.Info(); err == nil && now.Sub(fi.ModTime()) > time.Hour {
				os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		m := monthOf(name, ".ndjson")
		if m == "" {
			continue
		}
		month, _ := time.Parse("2006-01", m)
		if !now.UTC().After(month.AddDate(0, 1, 0).Add(time.Hour)) {
			continue
		}
		src := filepath.Join(dir, name)
		if err := gzipFile(src); err != nil {
			return done, fmt.Errorf("archive %s: %w", name, err)
		}
		if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
			return done, err
		}
		done = append(done, name+".gz")
	}
	return done, nil
}

func gzipFile(src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	dir, base := filepath.Split(src)
	// unique temp name: two archivers must never share a scratch file
	out, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	zw, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	// fsync before the rename publishes the .gz and the original dies —
	// otherwise a crash could leave a hollow archive and no source
	if serr := out.Sync(); err == nil {
		err = serr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, src+".gz")
}
