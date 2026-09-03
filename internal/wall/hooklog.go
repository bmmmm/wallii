// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The Stop hook writes one line per Stop — not one per firing. A firing
// counter cannot tell "the condition was false" from "the trigger never ran",
// and for this hook the second is the common case: the .start marker is
// written after every guard, and 107 of them in 12 days sit against roughly
// 60 sessions a day. Read as firings, the idle trigger's zero says its
// condition never held; read as reach, it may say the trigger was never
// evaluated. This file reads the record that separates the two.
//
// Nothing here computes with what the hook decided. It counts the words the
// hook wrote, in the fields the hook wrote them in, and the one hard rule is
// that a word this reader does not know is counted under its own name. A
// vocabulary the hook grows and the reader does not must not quietly land in
// a bucket that reads as "condition false" — that is the exact confusion the
// protocol exists against, applied to itself.

// StopUnreached is the state a trigger carries when the hook exited above it.
// It is the one word the reader has an opinion about: everything else the
// hook can put in the sig field means the signature block ran.
const StopUnreached = "unreached"

// stopFieldPrefixes name the last four fields in the order the hook writes
// them. A record with anything else in those positions is not a record.
var stopFieldPrefixes = [4]string{"exit=", "sig=", "idle=", "commit="}

// Stop is one Stop as the hook left it: when, which session, which repo, and
// which trigger decided what. No content — no found lines, no commit
// subjects — because the hook logs decisions and nothing it saw.
type Stop struct {
	TS     time.Time `json:"ts"`
	SID    string    `json:"sid,omitempty"`
	Repo   string    `json:"repo,omitempty"`
	Exit   string    `json:"exit"`
	Sig    string    `json:"sig"`
	Idle   string    `json:"idle"`
	Commit string    `json:"commit"`
}

// Reached says whether this Stop got as far as the first trigger. It reads
// the hook's own report rather than a list of guard words kept in sync here:
// every path past the guards writes something into sig, so anything but
// "unreached" means the trigger block ran. An unknown word therefore counts
// as reached, which is the honest direction — the hook decided something.
func (s Stop) Reached() bool { return s.Sig != StopUnreached }

// StopRead reports what the reader passed over, in the doctrine of ReadLast:
// a corrupt line must never blank the record around it, but it must not
// vanish either.
type StopRead struct {
	Files   int      `json:"files"`
	Bad     int      `json:"bad_lines,omitempty"`
	Skipped []string `json:"skipped_files,omitempty"`
}

// Triggers is the folded protocol. Reached against Stops is the first number
// the reader prints, because it is the one that decides how to read every
// other: a trigger with no firings across Stops it never reached has not been
// measured at all.
type Triggers struct {
	Stops   int       `json:"stops"`
	Reached int       `json:"reached"`
	First   time.Time `json:"first,omitempty"`
	Last    time.Time `json:"last,omitempty"`

	Exit   []NameCount `json:"exit"`
	Sig    []NameCount `json:"sig"`
	Idle   []NameCount `json:"idle"`
	Commit []NameCount `json:"commit"`

	Read StopRead `json:"read"`
}

// StopLogDir is where the hook keeps its protocol — the same directory as the
// shortcut markers, for the same reason signalsDir gives: the hook has no
// config file, and a second way to name the place is a second way for the two
// to disagree about it.
func StopLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, signalsDir), nil
}

// StopLogFiles lists the monthly protocol files, oldest name first. An empty
// list is not zero Stops: it is no protocol, and the difference is the whole
// point of this package's other source fields.
func StopLogFiles(dir string) ([]string, error) {
	return filepath.Glob(filepath.Join(dir, "stops-*.log"))
}

// ReadStops reads every protocol file in dir, keeping the records at or after
// since (a zero since keeps everything). Unparsable lines are skipped and
// counted, unreadable files are skipped and named: the record of a Stop is
// worth less than the record of the month around it, and one corrupt line
// must not take the rest with it.
func ReadStops(dir string, since time.Time) ([]Stop, StopRead, error) {
	var read StopRead
	files, err := StopLogFiles(dir)
	if err != nil {
		return nil, read, err
	}
	read.Files = len(files)
	var out []Stop
	for _, path := range files {
		stops, bad, err := readStopFile(path)
		if err != nil {
			read.Skipped = append(read.Skipped, filepath.Base(path))
			continue
		}
		read.Bad += bad
		for _, s := range stops {
			if since.IsZero() || !s.TS.Before(since) {
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out, read, nil
}

func readStopFile(path string) ([]Stop, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var out []Stop
	bad := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if s, ok := ParseStop(line); ok {
			out = append(out, s)
		} else {
			bad++
		}
	}
	if sc.Err() != nil {
		// a line longer than the cap, or a read error partway through: what
		// was already parsed stands, and the remainder counts as one loss
		// rather than as none
		bad++
	}
	return out, bad, nil
}

// ParseStop reads one record. Everything about the shape is required — seven
// fields, a timestamp, the four prefixes, a non-empty state each — and
// nothing about the vocabulary is: a state word this build has never seen is
// returned as it stands.
func ParseStop(line string) (Stop, bool) {
	f := strings.Split(line, "\t")
	if len(f) != 7 {
		return Stop{}, false
	}
	ts, err := time.Parse(time.RFC3339, f[0])
	if err != nil {
		return Stop{}, false
	}
	var states [4]string
	for i, prefix := range stopFieldPrefixes {
		v, ok := strings.CutPrefix(f[3+i], prefix)
		if !ok || v == "" {
			return Stop{}, false
		}
		states[i] = v
	}
	return Stop{
		TS: ts, SID: f[1], Repo: f[2],
		Exit: states[0], Sig: states[1], Idle: states[2], Commit: states[3],
	}, true
}

// FoldTriggers counts the protocol. A pure fold over what was read: no file
// access, no clock, and no vocabulary of its own beyond StopUnreached.
func FoldTriggers(stops []Stop, read StopRead) Triggers {
	t := Triggers{Stops: len(stops), Read: read}
	exit, sig, idle, commit := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	for _, s := range stops {
		if s.Reached() {
			t.Reached++
		}
		exit[s.Exit]++
		sig[s.Sig]++
		idle[s.Idle]++
		commit[s.Commit]++
		if t.First.IsZero() || s.TS.Before(t.First) {
			t.First = s.TS
		}
		if s.TS.After(t.Last) {
			t.Last = s.TS
		}
	}
	t.Exit, t.Sig, t.Idle, t.Commit = sortedCounts(exit), sortedCounts(sig), sortedCounts(idle), sortedCounts(commit)
	return t
}
