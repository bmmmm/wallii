// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// The fence around everything else in this file, and it was green before the
// server existed: the written dashboard.html asks the network for nothing.
// The only copy that talks is the one --serve hands out, and that one is
// never written to disk.
func TestDashFileStaysNetworkSilent(t *testing.T) {
	// The SVG namespace is a URL that is never fetched — createElementNS
	// takes it as an identifier. Everything else that looks like an address
	// is a finding.
	body := strings.ReplaceAll(dashTemplate, "http://www.w3.org/2000/svg", "«svg-ns»")
	for _, banned := range []string{
		"fetch(", "XMLHttpRequest", "WebSocket", "EventSource", "navigator.sendBeacon",
		"import(", "//cdn", "https://", "http://", "src=\"", "@import",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("dash.html contains %q — the written file requests nothing over the network", banned)
		}
	}
	// and the snippet that does talk must not be in the template
	if strings.Contains(dashTemplate, "/live") {
		t.Error("the live snippet leaked into the template — it belongs to the served copy only")
	}
}

// The snippet reaches the page only through the placeholder, and the
// placeholder must sit OUTSIDE the script block: two tests cut the script
// out with Index("<script>")/LastIndex("</script>") and substitute the
// placeholders by hand. A sixth one inside would stay literal there and kill
// both node harnesses with a SyntaxError.
func TestDashLivePlaceholderSitsOutsideTheScriptBlock(t *testing.T) {
	at := strings.Index(dashTemplate, "__WALLII_LIVE__")
	if at < 0 {
		t.Fatal("dash.html has no __WALLII_LIVE__ placeholder")
	}
	end := strings.LastIndex(dashTemplate, "</script>")
	if end < 0 {
		t.Fatal("dash.html has no script block")
	}
	if at < end {
		t.Fatalf("__WALLII_LIVE__ sits inside the script block (at %d, block ends %d) — the node harnesses would read it as code", at, end)
	}
	if strings.Count(dashTemplate, "__WALLII_LIVE__") != 1 {
		t.Error("the live placeholder must appear exactly once")
	}
}

func TestDashLiveSnippetCarriesTheBootVersion(t *testing.T) {
	s := dashLiveSnippet(1234567890, 1500)
	if !strings.Contains(s, "const BOOT = 1234567890;") {
		t.Errorf("the boot version is injected, never fetched — a post landing between render and first poll would leave the tab stale forever:\n%s", s)
	}
	if !strings.Contains(s, "setInterval(poll, 1500)") {
		t.Errorf("the poll interval did not reach the snippet:\n%s", s)
	}
	if !strings.Contains(s, "location.reload()") {
		t.Error("the snippet must reload the page, not swap data")
	}
}

// the pattern from internal/wall/pulse_test.go: no socket, just a transport
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestServer(t *testing.T, html string, version int64) http.Handler {
	t.Helper()
	srv := &dashServer{}
	srv.snap.Store(&dashSnapshot{html: []byte(html), version: version})
	return srv.handler()
}

func do(t *testing.T, h http.Handler, method, target string, host string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if host != "" {
		r.Host = host
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDashServeAnswersOnlyItsTwoRoutes(t *testing.T) {
	h := newTestServer(t, "<html>page</html>", 42)

	if w := do(t, h, http.MethodGet, "/", "127.0.0.1:8484"); w.Code != 200 || !strings.Contains(w.Body.String(), "page") {
		t.Errorf("GET / = %d %q", w.Code, w.Body.String())
	}

	w := do(t, h, http.MethodGet, "/live", "127.0.0.1:8484")
	if w.Code != 200 {
		t.Fatalf("GET /live = %d", w.Code)
	}
	var live dashLive
	if err := json.Unmarshal(w.Body.Bytes(), &live); err != nil {
		t.Fatalf("/live is not JSON: %v (%q)", err, w.Body.String())
	}
	if live.Version != 42 {
		t.Errorf("/live reports version %d, want 42", live.Version)
	}

	// The wall itself must not be reachable under any spelling: there is no
	// handler anywhere that turns a request into a file name.
	for _, path := range []string{
		"/wall-2026-09.ndjson", "/dashboard.html", "/index.html", "/live/", "/LIVE",
		"/%2e%2e/wall-2026-09.ndjson",
	} {
		if w := do(t, h, http.MethodGet, path, "127.0.0.1:8484"); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 — nothing but / and /live is served", path, w.Code)
		}
	}
	// A traversal is cleaned by the mux into a redirect; what matters is that
	// the place it points at is a 404 too, so no spelling reaches a file.
	w = do(t, h, http.MethodGet, "/../wall-2026-09.ndjson", "127.0.0.1:8484")
	if w.Code == 200 {
		t.Errorf("a traversal was answered with content: %q", w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		if after := do(t, h, http.MethodGet, loc, "127.0.0.1:8484"); after.Code != http.StatusNotFound {
			t.Errorf("the traversal redirect points at %s, which answers %d", loc, after.Code)
		}
	}
}

// Without a Host check any website can point a DNS name at 127.0.0.1 and
// read this page same-origin. The page is the whole wall in clear text.
func TestDashServeRefusesAForeignHost(t *testing.T) {
	h := newTestServer(t, "<html>page</html>", 1)
	for _, host := range []string{"wallii.example.com", "attacker.test:8484", "192.168.1.10:8484"} {
		w := do(t, h, http.MethodGet, "/", host)
		if w.Code != http.StatusForbidden {
			t.Errorf("Host %q got %d, want 403", host, w.Code)
		}
		if strings.Contains(w.Body.String(), "page") {
			t.Errorf("Host %q was served the page anyway", host)
		}
	}
	for _, host := range []string{"127.0.0.1:8484", "localhost:8484", "[::1]:8484", "127.0.0.1"} {
		if w := do(t, h, http.MethodGet, "/", host); w.Code != 200 {
			t.Errorf("loopback Host %q got %d, want 200", host, w.Code)
		}
	}
}

func TestDashServeIsReadOnlyAndUncached(t *testing.T) {
	h := newTestServer(t, "<html>page</html>", 1)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if w := do(t, h, m, "/", "127.0.0.1:8484"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s / = %d, want 405", m, w.Code)
		}
	}
	w := do(t, h, http.MethodHead, "/", "127.0.0.1:8484")
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Errorf("HEAD / = %d with %d bytes of body", w.Code, w.Body.Len())
	}
	got := do(t, h, http.MethodGet, "/", "127.0.0.1:8484")
	// Without no-store the reload the snippet triggers is answered from the
	// cache and the page never changes — which looks exactly like broken
	// change detection.
	if cc := got.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if csp := got.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP = %q — it is the machine-readable form of the README's promise", csp)
	}
	// never, under any circumstance, a CORS header
	for _, hdr := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"} {
		if v := got.Header().Get(hdr); v != "" {
			t.Errorf("%s = %q — this page is never readable cross-origin", hdr, v)
		}
	}
}

func TestDashServeSaysSoWhileTheFirstPageIsMissing(t *testing.T) {
	srv := &dashServer{}
	h := srv.handler()
	if w := do(t, h, http.MethodGet, "/", "127.0.0.1:8484"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("GET / with no snapshot = %d, want 503", w.Code)
	}
	// /live still answers, with the version nothing has: a client polling
	// through a restart must not take a missing page for an unchanged one
	w := do(t, h, http.MethodGet, "/live", "127.0.0.1:8484")
	var live dashLive
	json.Unmarshal(w.Body.Bytes(), &live)
	if w.Code != 200 || live.Version != 0 {
		t.Errorf("GET /live with no snapshot = %d version %d, want 200 and 0", w.Code, live.Version)
	}
}

func TestWallFingerprintSeesEveryKindOfChange(t *testing.T) {
	dir := t.TempDir()
	month := filepath.Join(dir, "wall-2026-09.ndjson")
	if err := os.WriteFile(month, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := wallFingerprint(dir)

	// an append
	if err := os.WriteFile(month, []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	grown := wallFingerprint(dir)
	if grown == base {
		t.Error("an appended post did not change the fingerprint")
	}

	// a new month
	if err := os.WriteFile(filepath.Join(dir, "wall-2026-10.ndjson"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rolled := wallFingerprint(dir)
	if rolled == grown {
		t.Error("a new month file did not change the fingerprint")
	}

	// deletion — silence would read as "nothing changed"
	if err := os.Remove(month); err != nil {
		t.Fatal(err)
	}
	if wallFingerprint(dir) == rolled {
		t.Error("removing a month did not change the fingerprint")
	}

	// and the one file that must NOT move it: a parallel `wallii dash`
	// writing its output next to the wall would otherwise drive the watcher
	// in a loop forever
	after := wallFingerprint(dir)
	if err := os.WriteFile(filepath.Join(dir, "dashboard.html"), []byte("<html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if wallFingerprint(dir) != after {
		t.Error("a dashboard.html written beside the wall changed the fingerprint — the server would rebuild forever")
	}
}

func TestDebouncerCollapsesABurstButNeverStalls(t *testing.T) {
	d := debouncer{quiet: 400 * time.Millisecond, maxWait: 3 * time.Second}
	t0 := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	// ages, measured back from now — no sleeps, so this cannot flake
	const never = -1
	for _, tc := range []struct {
		name              string
		firstAge, lastAge time.Duration
		want              bool
	}{
		{"nothing changed", never, never, false},
		{"still inside the quiet window", 200 * time.Millisecond, 100 * time.Millisecond, false},
		{"quiet for long enough", 900 * time.Millisecond, 500 * time.Millisecond, true},
		{"a burst that never pauses still renders", 4 * time.Second, 100 * time.Millisecond, true},
		{"exactly at the quiet edge", 400 * time.Millisecond, 400 * time.Millisecond, true},
		{"a long burst one tick short of maxWait", 2900 * time.Millisecond, 50 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first, last time.Time
			if tc.firstAge != never {
				first, last = t0.Add(-tc.firstAge), t0.Add(-tc.lastAge)
			}
			if got := d.due(t0, first, last); got != tc.want {
				t.Errorf("due = %v, want %v", got, tc.want)
			}
		})
	}
}

// The commit half is the expensive one and gets its own clock: it must not
// fork git for every post, and "off" must really mean never.
func TestCommitsHaveTheirOwnDeadline(t *testing.T) {
	now := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	every5 := dashServeOpts{commitEvery: 5 * time.Minute}
	if dashCommitsDue(every5, now.Add(-time.Minute), now) {
		t.Error("git re-ran one minute into a five-minute budget")
	}
	if !dashCommitsDue(every5, now.Add(-6*time.Minute), now) {
		t.Error("git did not re-run after the budget elapsed")
	}
	if !dashCommitsDue(dashServeOpts{commitEvery: 0}, now, now) {
		t.Error("--commits 0 must measure on every rebuild")
	}
	if dashCommitsDue(dashServeOpts{commitsOff: true, commitEvery: 0}, now.Add(-time.Hour), now) {
		t.Error("--commits off must never fork git")
	}
}

// A busy port is a loud error and never a quiet move to another one: an open
// tab would poll the dead port forever, and nothing about that looks broken.
func TestPortProbeTellsOurServerFromAStranger(t *testing.T) {
	ours := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody, Request: r,
			Header: http.Header{dashProbeHdr: []string{dashProbeVal}}}, nil
	})}
	stranger := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody, Request: r, Header: http.Header{}}, nil
	})}
	dead := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	})}

	if mine, up := dashPortInUse(ours, 8484); !mine || !up {
		t.Errorf("our own server was not recognised: ours=%v reachable=%v", mine, up)
	}
	if mine, up := dashPortInUse(stranger, 8484); mine || !up {
		t.Errorf("a stranger on the port read as ours: ours=%v reachable=%v", mine, up)
	}
	if mine, up := dashPortInUse(dead, 8484); mine || up {
		t.Errorf("nothing is listening, yet: ours=%v reachable=%v", mine, up)
	}
}

// serveDash renders before it binds, and a render that fails must not leave a
// server standing with no page.
func TestServeDashFailsBeforeBindingWhenTheFirstRenderFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	err := serveDash(ctx, t.TempDir(), dashServeOpts{port: 0}, func(live bool, version int64) ([]byte, error) {
		calls.Add(1)
		if !live {
			t.Error("the served copy must be rendered with live=true")
		}
		return nil, fmt.Errorf("no wall here")
	})
	if err == nil {
		t.Fatal("serveDash returned nil after the first render failed")
	}
	if calls.Load() != 1 {
		t.Errorf("render was called %d times, want 1", calls.Load())
	}
}

// The watcher's contract, without a socket: a post lands, the snapshot's
// version moves, and the new page is what /live and / report.
func TestWatcherRebuildsAfterAPost(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wall-2026-09.ndjson"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := wallFingerprint(dir)
	srv := &dashServer{}
	srv.snap.Store(&dashSnapshot{html: []byte("first"), version: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var renders atomic.Int32
	go watchWall(ctx, dir, dashServeOpts{}, srv, func(live bool, version int64) ([]byte, error) {
		renders.Add(1)
		return []byte("rebuilt"), nil
	}, base)

	if err := os.WriteFile(filepath.Join(dir, "wall-2026-09.ndjson"), []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snap := srv.snap.Load(); string(snap.html) == "rebuilt" && snap.version != 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the watcher never rebuilt after a post (renders=%d)", renders.Load())
}

// A failed rebuild keeps the page that worked, rather than serving nothing.
func TestAFailedRebuildKeepsThePreviousPage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wall-2026-09.ndjson"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := wallFingerprint(dir)
	srv := &dashServer{}
	srv.snap.Store(&dashSnapshot{html: []byte("good"), version: 7})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tries atomic.Int32
	go watchWall(ctx, dir, dashServeOpts{}, srv, func(live bool, version int64) ([]byte, error) {
		tries.Add(1)
		return nil, fmt.Errorf("the wall is half written")
	}, base)
	if err := os.WriteFile(filepath.Join(dir, "wall-2026-09.ndjson"), []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && tries.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if tries.Load() == 0 {
		t.Fatal("the watcher never attempted a rebuild")
	}
	if snap := srv.snap.Load(); string(snap.html) != "good" || snap.version != 7 {
		t.Errorf("a failed rebuild replaced the working page: %q version %d", snap.html, snap.version)
	}
}

// WALLII_TEST_BIND=1 opts into the one thing a recorder cannot cover: a real
// listener on a real port. CI sets it; the sandbox where this was written
// cannot bind, and a test that silently skips there is better than one that
// fails for a reason that has nothing to do with the code.
func TestServeDashOnARealSocket(t *testing.T) {
	if os.Getenv("WALLII_TEST_BIND") != "1" {
		t.Skip("set WALLII_TEST_BIND=1 to run the test that binds a real port")
	}
	dir := t.TempDir()
	if err := wall.Append(dir, wall.Event{TS: time.Now(), Repo: "webshop", Actor: "bot/builder", Msg: "hello"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := 18484
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serveDash(ctx, dir, dashServeOpts{port: port}, func(live bool, version int64) ([]byte, error) {
			return []byte("<html>served</html>"), nil
		})
	}()
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	var body string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// a server that refused to start says why — "did not answer" would
		// hide a denied bind behind a timeout
		select {
		case err := <-serveErr:
			t.Fatalf("the server stopped before it could answer: %v", err)
		default:
		}
		resp, err := http.Get(url)
		if err == nil {
			b := make([]byte, 64)
			n, _ := resp.Body.Read(b)
			resp.Body.Close()
			body = string(b[:n])
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(body, "served") {
		t.Fatalf("the real server did not answer: %q", body)
	}
}
