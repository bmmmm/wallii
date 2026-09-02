// SPDX-License-Identifier: GPL-3.0-or-later
package wall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// markerFile points HOME at a temp dir, names a session, and returns the
// path the Stop hook would write for repo — the file itself is the test's
// to create, or not.
func markerFile(t *testing.T, repo string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-0001")
	dir := filepath.Join(home, signalsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "sess-0001-"+repo+".shortcut")
}

func writeMarker(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The marker the hook wrote is what the post carries: path and line, as the
// diff showed them, and the source says the hook looked.
func TestSessionSignalsReadsTheHooksMarker(t *testing.T) {
	path := markerFile(t, "webshop")
	writeMarker(t, path, "internal/cart/cart_test.go\tt.Skip(\"flaky under load\")\n.github/workflows/ci.yml\tcontinue-on-error: true\n")

	values, src := SessionSignals("webshop")
	if src != SignalHook {
		t.Fatalf("src = %q, want %q", src, SignalHook)
	}
	want := []string{
		`internal/cart/cart_test.go: t.Skip("flaky under load")`,
		".github/workflows/ci.yml: continue-on-error: true",
	}
	if strings.Join(values, "|") != strings.Join(want, "|") {
		t.Fatalf("values = %q, want %q", values, want)
	}
	// what the marker yields must be storable as it is
	e := Event{TS: time.Now(), Repo: "webshop", Msg: "cart totals fixed", Signals: values, SignalSrc: src}
	if err := e.Validate(); err != nil {
		t.Fatalf("a post carrying the marker's signals was rejected: %v", err)
	}
}

// Three readings, three answers. An empty file is the hook saying it looked
// and found nothing; no file is nobody looking; no session id is no file to
// look for. Only the first of them carries a source.
func TestSessionSignalsTellsMeasuredFromUnmeasured(t *testing.T) {
	path := markerFile(t, "webshop")
	writeMarker(t, path, "")
	values, src := SessionSignals("webshop")
	if len(values) != 0 || src != SignalHook {
		t.Errorf("empty marker = %q from %q, want no values from %q — measured, nothing found", values, src, SignalHook)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if values, src := SessionSignals("webshop"); values != nil || src != "" {
		t.Errorf("no marker = %q from %q, want nothing at all — nobody measured", values, src)
	}

	writeMarker(t, path, "a_test.go\tt.Skip(\"x\")\n")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if values, src := SessionSignals("webshop"); values != nil || src != "" {
		t.Errorf("without a session id = %q from %q, want nothing — there is no file to name", values, src)
	}
}

// The marker is per session and repo: a finding in one repo must not land on
// another repo's posts, and a session id that names a path outside the
// marker directory names nothing.
func TestSessionSignalsStaysInItsOwnMarker(t *testing.T) {
	path := markerFile(t, "webshop")
	writeMarker(t, path, "a_test.go\tt.Skip(\"x\")\n")
	if values, src := SessionSignals("api-gateway"); values != nil || src != "" {
		t.Errorf("another repo read webshop's marker: %q from %q", values, src)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "../sess-0001")
	if values, src := SessionSignals("webshop"); values != nil || src != "" {
		t.Errorf("a traversing session id read a marker: %q from %q", values, src)
	}
}

// A file that exists but cannot be read is not a clean diff. Reporting it as
// "measured, nothing found" would be the one lie the source exists to
// prevent, so it reads as nobody measured. A directory in the file's place
// is the portable way to make ReadFile fail.
func TestSessionSignalsUnreadableMarkerIsNobodyMeasured(t *testing.T) {
	path := markerFile(t, "webshop")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if values, src := SessionSignals("webshop"); values != nil || src != "" {
		t.Errorf("unreadable marker = %q from %q, want nothing at all", values, src)
	}
}

// The post is a pointer, not a catalogue: the first MaxSignals lines land,
// the rest stays in the file. And every value fits the signal cap, with an
// ellipsis where the line was longer.
func TestSessionSignalsCapsCountAndLength(t *testing.T) {
	path := markerFile(t, "webshop")
	long := strings.Repeat("assertEventually(", 8) + "x)"
	writeMarker(t, path, strings.Join([]string{
		"a_test.go\tt.Skip(\"one\")",
		"b_test.go\tt.Skip(\"two\")",
		"c_test.go\tt.Skip(\"three\")",
		"d_test.go\tt.Skip(\"four\")",
		"e_test.go\t" + long,
	}, "\n")+"\n")

	values, _ := SessionSignals("webshop")
	if len(values) != MaxSignals {
		t.Fatalf("got %d signals, want %d", len(values), MaxSignals)
	}
	if !strings.HasPrefix(values[0], "a_test.go") || !strings.HasPrefix(values[2], "c_test.go") {
		t.Errorf("first lines must win, got %q", values)
	}

	writeMarker(t, path, "e_test.go\t"+long+"\n")
	values, _ = SessionSignals("webshop")
	if n := len([]rune(values[0])); n > MaxSignalRunes || !strings.Contains(values[0], "…") {
		t.Errorf("long line renders as %d runes %q, want ≤ %d with an …", n, values[0], MaxSignalRunes)
	}
	e := Event{TS: time.Now(), Repo: "webshop", Msg: "x", Signals: values, SignalSrc: SignalHook}
	if err := e.Validate(); err != nil {
		t.Fatalf("a cut signal must still validate: %v", err)
	}
}

// Cutting the tail off a signal drops the reason, and the reason is what
// tells a guard from a shortcut: `t.Skip("flaky under load")` is one,
// `t.Skip("no ffmpeg — run: brew install ffmpeg")` is the other, and they
// differ only in the words at the end. So an over-long line loses its
// middle — the function signature nobody reads — and keeps both ends.
func TestSignalCutKeepsThePathAndTheReason(t *testing.T) {
	path := markerFile(t, "webshop")
	reason := `t.Skip("flaky under load")`
	line := "cart_test.go\tfunc TestCartCheckoutAppliesEveryDiscountInTheOrderTheBasketListsThem(t *testing.T) { " + reason
	writeMarker(t, path, line+"\n")

	values, _ := SessionSignals("webshop")
	if len(values) != 1 {
		t.Fatalf("got %d signals, want 1", len(values))
	}
	got := values[0]
	if n := utf8.RuneCountInString(got); n > MaxSignalRunes {
		t.Fatalf("cut signal is %d runes, max %d: %q", n, MaxSignalRunes, got)
	}
	if !strings.HasPrefix(got, "cart_test.go: ") {
		t.Errorf("the path must open the line, got %q", got)
	}
	if !strings.HasSuffix(got, reason) {
		t.Errorf("the reason must survive the cut, got %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a cut line must say it was cut, got %q", got)
	}
	// and the same line at 64 runes would have ended mid-reason — the bug
	// this cut exists to prevent
	if utf8.RuneCountInString(line) <= MaxFieldRunes {
		t.Fatalf("the fixture must be longer than the old cap to prove anything")
	}
	e := Event{TS: time.Now(), Repo: "webshop", Msg: "x", Signals: values, SignalSrc: SignalHook}
	if err := e.Validate(); err != nil {
		t.Fatalf("a cut signal must still validate: %v", err)
	}
}

// Blank lines are not findings. A tab inside the content — an indented
// line the hook kept — would fail Validate as a control character, and
// losing the signal to its own whitespace is the wrong trade. A line in a
// shape the hook never wrote is kept as it is: it is still what was found.
func TestSessionSignalsKeepsEveryLineStorable(t *testing.T) {
	path := markerFile(t, "webshop")
	writeMarker(t, path, "\n\na_test.go\tif x {\tt.Skip(\"y\") }\n   \nsomething without a tab  \n")
	values, _ := SessionSignals("webshop")
	want := []string{`a_test.go: if x { t.Skip("y") }`, "something without a tab"}
	if strings.Join(values, "|") != strings.Join(want, "|") {
		t.Fatalf("values = %q, want %q", values, want)
	}
	e := Event{TS: time.Now(), Repo: "webshop", Msg: "x", Signals: values, SignalSrc: SignalHook}
	if err := e.Validate(); err != nil {
		t.Fatalf("marker lines must validate as stored: %v", err)
	}
}
