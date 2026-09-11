// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bmmmm/wallii/internal/wall"
)

// `wallii dash --serve` keeps a dashboard current while you work: it watches
// the wall directory, rebuilds the page when it changes, and the open tab
// reloads itself.
//
// It polls /live every 1.5s rather than holding an SSE stream. SSE is the
// part that cannot be tested without binding a real socket AND the part that
// carries the concurrency; a poll handler is a pure function from an
// atomic.Pointer to JSON and is covered completely with httptest.NewRecorder.
// With one or two tabs on loopback the felt difference is zero.
//
// It binds 127.0.0.1 and nothing else, with no --host. The page is the whole
// wall in clear text: 0.0.0.0 would mean everyone on the same wifi can read
// it without authentication. Remote access is `ssh -L`.

const (
	dashLivePollMS = 1500
	dashProbeHdr   = "X-Wallii"
	dashProbeVal   = "dash"
)

// dashServeOpts is what cmdDash hands the server after it has parsed flags.
type dashServeOpts struct {
	port        int
	open        bool
	commitEvery time.Duration // how often the git half may re-run; 0 = every rebuild
	commitsOff  bool
}

// dashSnapshot is one rendered page plus what a client needs to notice that
// it is stale. It is replaced wholesale, never mutated: it is shared across
// requests, and editing one in place would change a page already served.
type dashSnapshot struct {
	html    []byte
	version int64 // unix nanos of the build; the only thing /live compares
}

// dashLive is the JSON /live answers with.
type dashLive struct {
	Version int64 `json:"version"`
}

// wallFingerprint is the change detector: name, size and modification time
// of every wall file. It deliberately goes through wall.Files, so the rule
// for what counts as a wall file stays in internal/wall/store.go and a
// dashboard.html written next to them never triggers a rebuild — otherwise
// `wallii dash` in another terminal would drive this server in a loop.
//
// A file that cannot be stat'ed contributes a marker rather than nothing:
// deleting a month must read as a change, and silence would make it read as
// no change at all.
func wallFingerprint(dir string) string {
	files, err := wall.Files(dir)
	if err != nil {
		return "error:" + err.Error()
	}
	parts := make([]string, 0, len(files))
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			parts = append(parts, f+":gone")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", f, fi.Size(), fi.ModTime().UnixNano()))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// debouncer decides when a burst of changes has settled. Pure function of
// the three timestamps, so the table test needs no sleep and cannot flake:
// five posts in a second produce one rebuild, and a stream that never goes
// quiet still renders every maxWait.
type debouncer struct {
	quiet   time.Duration // no change for this long → build
	maxWait time.Duration // changing this long without a pause → build anyway
}

var defaultDebouncer = debouncer{quiet: 400 * time.Millisecond, maxWait: 3 * time.Second}

// due reports whether a change first seen at first and last seen at last
// should be rendered now.
func (d debouncer) due(now, first, last time.Time) bool {
	if first.IsZero() {
		return false
	}
	return now.Sub(last) >= d.quiet || now.Sub(first) >= d.maxWait
}

// dashLiveSnippet is the only code that exists in the served page and not in
// a written file. The signature is the guarantee: two integers go in, so no
// string off the wall can reach it.
func dashLiveSnippet(version int64, everyMS int) string {
	return `<script>
"use strict";
/* Served-only: the file on disk never carries this. It asks /live for the
   build stamp and reloads the whole page when it changes.

   The boot version is injected, not fetched. Asked for over the first poll,
   a post landing between render and that poll would leave this tab showing
   stale data forever, with no symptom at all. */
(function () {
  const BOOT = ` + strconv.FormatInt(version, 10) + `;
  const KEY = "wallii-dash-scroll";
  if ("scrollRestoration" in history) history.scrollRestoration = "manual";
  try {
    const y = sessionStorage.getItem(KEY);
    if (y !== null) window.scrollTo(0, +y);
  } catch { /* not restored */ }
  addEventListener("beforeunload", () => {
    try { sessionStorage.setItem(KEY, String(window.scrollY)); } catch { /* not saved */ }
  });
  let failures = 0;
  async function poll() {
    try {
      const r = await fetch("/live", { cache: "no-store" });
      if (!r.ok) throw new Error(r.status);
      const v = await r.json();
      failures = 0;
      if (v.version !== BOOT) location.reload();
    } catch {
      // a server that went away is not an error worth painting; keep trying
      failures++;
    }
  }
  setInterval(poll, ` + strconv.Itoa(everyMS) + `);
})();
</script>`
}

// dashServer holds the current snapshot and answers the two routes.
type dashServer struct {
	snap atomic.Pointer[dashSnapshot]
}

func (s *dashServer) handler() http.Handler {
	mux := http.NewServeMux()
	// Exactly two routes, and neither of them turns a request into a file
	// name: there is no http.FileServer, no http.Dir and no ServeFile
	// anywhere, so /wall-2026-09.ndjson cannot be fetched however it is
	// spelled.
	mux.HandleFunc("/live", s.serveLive)
	mux.HandleFunc("/", s.servePage)
	return dashGuard(mux)
}

// dashGuard rejects anything but a loopback Host. Without it any website can
// point a DNS name at 127.0.0.1 and read this page same-origin — the wall in
// clear text, handed to whoever the user happened to visit.
func dashGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(dashProbeHdr, dashProbeVal)
		if !isLoopbackHost(r.Host) {
			http.Error(w, "wallii dash serves 127.0.0.1 only — reach it over ssh -L, not by name", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (s *dashServer) servePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	snap := s.snap.Load()
	if snap == nil {
		http.Error(w, "still building", http.StatusServiceUnavailable)
		return
	}
	// no-store, or the reload the snippet triggers is answered out of the
	// cache and the page never changes — which looks exactly like broken
	// change detection
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// the machine-readable form of the README's promise: this page fetches
	// nothing but its own /live
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(snap.html)))
		return
	}
	w.Write(snap.html)
}

func (s *dashServer) serveLive(w http.ResponseWriter, r *http.Request) {
	snap := s.snap.Load()
	var v int64
	if snap != nil {
		v = snap.version
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dashLive{Version: v})
}

// dashPortInUse asks a listener that is already on the port whether it is
// one of ours, so "already running" can be told from "someone else has the
// port". It never falls back to another port: an open tab would poll the
// dead one forever, and nothing about that looks like an error.
func dashPortInUse(client *http.Client, port int) (ours bool, reachable bool) {
	req, err := http.NewRequest(http.MethodHead, fmt.Sprintf("http://127.0.0.1:%d/live", port), nil)
	if err != nil {
		return false, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	return resp.Header.Get(dashProbeHdr) == dashProbeVal, true
}

// serveDash renders once, binds, and keeps rendering while the wall changes.
// render is handed in so the caller keeps every decision about what a page
// is; this function only decides when.
func serveDash(ctx context.Context, dir string, opts dashServeOpts, render func(live bool, version int64) ([]byte, error)) error {
	srv := &dashServer{}
	// The fingerprint is taken BEFORE the first render, for the same reason
	// the snippet's boot version is injected rather than fetched: a post
	// landing between the render and the watcher's first look would be
	// inside the baseline and never trigger a rebuild — the page would sit
	// stale with no symptom.
	startFP := wallFingerprint(dir)
	first := time.Now().UnixNano()
	html, err := render(true, first)
	if err != nil {
		return err
	}
	srv.snap.Store(&dashSnapshot{html: html, version: first})

	addr := fmt.Sprintf("127.0.0.1:%d", opts.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ours, reachable := dashPortInUse(&http.Client{Timeout: 700 * time.Millisecond}, opts.port)
		switch {
		case ours:
			return fmt.Errorf("port %d already serves a wallii dash — open http://%s/ or pass --port", opts.port, addr)
		case reachable:
			return fmt.Errorf("port %d is taken by something else — pass --port", opts.port)
		}
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}

	httpSrv := &http.Server{Handler: srv.handler(), ReadHeaderTimeout: 5 * time.Second}
	url := "http://" + addr + "/"
	fmt.Printf("dashboard: %s (watching %s — ctrl-c to stop)\n", url, dir)
	// after the listener exists, before Serve: a browser that wins the race
	// against Serve would otherwise get a connection refused
	if opts.open {
		openBrowser(url)
	}

	done := make(chan error, 1)
	go func() { done <- httpSrv.Serve(ln) }()
	go watchWall(ctx, dir, opts, srv, render, startFP)

	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutCtx)
		fmt.Println()
		return nil // ctrl-c is how this command ends, not a failure
	}
}

// watchWall rebuilds the snapshot when the wall directory changes. Polling
// the fingerprint rather than watching the filesystem: it is a handful of
// Stat calls a second against files this machine just wrote, it needs no
// platform-specific API, and it cannot miss a change the way a coalesced
// event queue can.
func watchWall(ctx context.Context, dir string, opts dashServeOpts, srv *dashServer, render func(live bool, version int64) ([]byte, error), startFP string) {
	const tick = 250 * time.Millisecond
	last := startFP
	var firstChange, lastChange time.Time
	lastCommits := time.Now()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			fp := wallFingerprint(dir)
			if fp != last {
				last = fp
				if firstChange.IsZero() {
					firstChange = now
				}
				lastChange = now
			}
			if !defaultDebouncer.due(now, firstChange, lastChange) {
				continue
			}
			firstChange, lastChange = time.Time{}, time.Time{}
			// The git half is the expensive one — it forks per repo, 0.42s
			// over 40 repos here and up to gitTimeout() in the worst case.
			// It must not run on every post.
			if dashCommitsDue(opts, lastCommits, now) {
				lastCommits = now
				dashCommitsFresh.Store(true)
			} else {
				dashCommitsFresh.Store(false)
			}
			version := now.UnixNano()
			html, err := render(true, version)
			if err != nil {
				fmt.Fprintf(os.Stderr, "wallii: rebuild failed, keeping the previous page: %v\n", err)
				continue
			}
			srv.snap.Store(&dashSnapshot{html: html, version: version})
		}
	}
}

// dashCommitsFresh tells the renderer whether this rebuild may re-run git.
// It is read by cmdDash's render closure, in the same goroutine that wrote
// it, one rebuild at a time.
var dashCommitsFresh atomic.Bool

func dashCommitsDue(opts dashServeOpts, lastRun, now time.Time) bool {
	if opts.commitsOff {
		return false
	}
	if opts.commitEvery <= 0 {
		return true
	}
	return now.Sub(lastRun) >= opts.commitEvery
}

func openBrowser(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if err := exec.Command(opener, url).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "wallii: could not open a browser (%v) — open %s yourself\n", err, url)
	}
}

// dashSignalContext is ctrl-c, and ctrl-c is how this command ends.
func dashSignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
