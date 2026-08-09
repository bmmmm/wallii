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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := CurrentFile(dir, e.TS)
	if _, err := os.Stat(path + ".gz"); err == nil {
		// refuse rather than shadow the archive: Files() prefers plain over
		// .gz, so a backdated append would silently hide the archived month
		return fmt.Errorf("month %s is already archived — backdated appends are not supported", e.TS.UTC().Format("2006-01"))
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// Files lists wall month files oldest-first. If a month exists both plain
// and gzipped (interrupted archive run), the plain file wins so no month is
// read twice.
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
		case strings.HasPrefix(name, "wall-") && strings.HasSuffix(name, ".ndjson"):
			byMonth[strings.TrimSuffix(name, ".ndjson")] = name
		case strings.HasPrefix(name, "wall-") && strings.HasSuffix(name, ".ndjson.gz"):
			month := strings.TrimSuffix(name, ".ndjson.gz")
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

// ReadFile parses one wall file (plain or gzipped). Malformed lines are
// skipped and counted, not fatal — one bad write must not brick the wall.
func ReadFile(path string) ([]Event, int, error) {
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
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out []Event
	bad := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			bad++
			continue
		}
		out = append(out, e)
	}
	return out, bad, sc.Err()
}

// ReadLast returns up to limit matching events (0 = no limit), oldest first,
// plus the count of malformed lines skipped. Month files are read
// newest-first so deep archives are only opened until the limit is met;
// match==nil accepts everything.
func ReadLast(dir string, limit int, match func(Event) bool) ([]Event, int, error) {
	files, err := Files(dir)
	if err != nil {
		return nil, 0, err
	}
	var chunks [][]Event
	total, badTotal := 0, 0
	for i := len(files) - 1; i >= 0; i-- {
		evs, bad, err := ReadFile(files[i])
		if err != nil {
			return nil, badTotal, err
		}
		badTotal += bad
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
	return out, badTotal, nil
}

// Archive gzips finished months and removes the plain originals. A month is
// only touched once it has been over for >1h, so a concurrent poster still
// holding an old "current month" path cannot append to a file mid-compress.
func Archive(dir string, now time.Time) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var done []string
	for _, en := range entries {
		name := en.Name()
		if !strings.HasPrefix(name, "wall-") || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		month, err := time.Parse("2006-01", strings.TrimSuffix(strings.TrimPrefix(name, "wall-"), ".ndjson"))
		if err != nil {
			continue
		}
		if !now.UTC().After(month.AddDate(0, 1, 0).Add(time.Hour)) {
			continue
		}
		src := filepath.Join(dir, name)
		if err := gzipFile(src); err != nil {
			return done, fmt.Errorf("archive %s: %w", name, err)
		}
		if err := os.Remove(src); err != nil {
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
	tmp := src + ".gz.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
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
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	// rename after full write: readers never see a half-written .gz
	return os.Rename(tmp, src+".gz")
}
