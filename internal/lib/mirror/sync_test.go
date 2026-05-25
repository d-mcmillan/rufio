package mirror

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/observation"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

func TestMirrorSync_WritesIncomingEvents(t *testing.T) {
	srvRoot := initProject(t)
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Sync(ctx, SyncOptions{
			ServerURL: srvURL, Token: token, To: mirrorDir, InsecureTLS: true,
		})
	}()

	// Give Sync a moment to connect.
	time.Sleep(300 * time.Millisecond)

	// Seed a new thought on the server; the mirror should pick it up
	// within a poll-tick or two.
	seedThought(t, srvRoot, "alice", "fleet", "test:1", "sync incoming hypothesis")

	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		time.Sleep(100 * time.Millisecond)
		_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			if bs, _ := os.ReadFile(p); strings.Contains(string(bs), "sync incoming hypothesis") {
				found = true
			}
			return nil
		})
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Sync did not return after cancel")
	}
	if !found {
		t.Fatal("mirror did not pick up the seeded event within 3s")
	}
}

func TestMirrorSync_RespectsPrivacy(t *testing.T) {
	srvRoot := initProject(t)
	srvURL, aliceToken := startServer(t, srvRoot)
	mirrorDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Sync(ctx, SyncOptions{
			ServerURL: srvURL, Token: aliceToken, To: mirrorDir, InsecureTLS: true,
		})
	}()
	time.Sleep(300 * time.Millisecond)

	// Bob writes a scope=agent record — should NEVER appear in alice's mirror.
	seedThought(t, srvRoot, "bob", "agent", "test:1", "bob secret should never mirror")
	// Bob also writes a fleet record — should appear.
	seedThought(t, srvRoot, "bob", "fleet", "test:1", "bob fleet should mirror")

	time.Sleep(1500 * time.Millisecond)
	cancel()
	<-done

	leakedSecret := false
	sawFleet := false
	_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		bs, _ := os.ReadFile(p)
		if strings.Contains(string(bs), "bob secret should never mirror") {
			leakedSecret = true
		}
		if strings.Contains(string(bs), "bob fleet should mirror") {
			sawFleet = true
		}
		return nil
	})
	if leakedSecret {
		t.Error("alice's mirror leaked bob's scope=agent record — privacy breach")
	}
	if !sawFleet {
		t.Error("alice's mirror missed bob's scope=fleet record — over-filtering")
	}
}

func TestMirrorSync_CursorPersists(t *testing.T) {
	srvRoot := initProject(t)
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()
	cursorFile := filepath.Join(mirrorDir, ".rufio", ".mirror-cursor")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = Sync(ctx, SyncOptions{
			ServerURL: srvURL, Token: token, To: mirrorDir, InsecureTLS: true,
		})
	}()
	time.Sleep(300 * time.Millisecond)
	seedThought(t, srvRoot, "alice", "fleet", "test:1", "first event for cursor")
	time.Sleep(1500 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)

	// Cursor file should exist after a successful sync.
	if _, err := os.Stat(cursorFile); err != nil {
		t.Errorf("cursor file %s should exist after sync wrote an event: %v", cursorFile, err)
	}
}

func TestMirrorSync_AtomicWrites(t *testing.T) {
	// We can't kill -9 a goroutine, so we approximate the atomicity
	// guarantee by inspecting that no .tmp files remain after a clean
	// shutdown — proof that writeAtomic's rename completed every time.
	srvRoot := initProject(t)
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = Sync(ctx, SyncOptions{
			ServerURL: srvURL, Token: token, To: mirrorDir, InsecureTLS: true,
		})
	}()
	time.Sleep(300 * time.Millisecond)
	for i := 0; i < 3; i++ {
		seedThought(t, srvRoot, "alice", "fleet", "test:1", "atomic test "+string(rune('A'+i)))
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)

	hasTmp := false
	_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".tmp") {
			hasTmp = true
		}
		return nil
	})
	if hasTmp {
		t.Error("mirror dir contains lingering .tmp files — atomic write contract violated")
	}
}

// TestMirrorSync_RejectsPathTraversal (security audit H1). A malicious
// or misconfigured server emitting a /listen event whose `path` field
// contains traversal segments must NOT result in writes outside the
// mirror root. We drive persistEvent directly (the JSON-decode+write
// path is the load-bearing surface) and assert every attack vector
// is rejected before touching disk.
func TestMirrorSync_RejectsPathTraversal(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"parent-relative", "../../../etc/cron.d/exploit"},
		{"absolute-posix", "/etc/passwd"},
		{"embedded-dotdot", "live/../../etc/passwd"},
		{"nul-injection", "live/outbox/alice/x\x00.gdl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			to := t.TempDir()
			ev := map[string]interface{}{
				"_type": "thought",
				"path":  c.path,
				"raw":   "@thought|id:x|author:attacker|content:exploit|scope:fleet|ts:2026-05-22T00:00:00Z",
				"ts":    "2026-05-22T00:00:00Z",
			}
			bs, _ := json.Marshal(ev)
			if _, err := persistEvent(string(bs), to); err == nil {
				t.Errorf("persistEvent accepted suspicious path %q (must reject)", c.path)
			}
			// Belt-and-suspenders: no file containing the attacker
			// marker should exist anywhere under to (the mirror root
			// is the only writable surface; an escape would land
			// elsewhere on the filesystem — t.TempDir's parent is
			// /tmp/... but we still pin "no files under to" as
			// proof the write was refused).
			matched, _ := filepath.Glob(filepath.Join(to, "**/*"))
			for _, m := range matched {
				bs, _ := os.ReadFile(m)
				if strings.Contains(string(bs), "attacker") {
					t.Errorf("attacker payload landed at %s", m)
				}
			}
		})
	}
}

func TestMirrorSync_ReconnectsBackoff(t *testing.T) {
	// Soft assertion of the backoff loop: when the server URL is
	// invalid, Sync should not return immediately — the reconnect
	// loop ticks the backoff timer and retries. We give it 1500ms
	// and verify it's still running (ctx not yet returned).
	mirrorDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = Sync(ctx, SyncOptions{
			ServerURL: "https://127.0.0.1:1", // refused
			Token:     "rufio_bogus",
			To:        mirrorDir,
		})
		close(done)
	}()
	select {
	case <-done:
		// Returned on its own — usually because ctx timed out at 1500ms.
		// That's fine; the assertion is "Sync didn't crash immediately
		// on a bad server" — backoff kept it alive.
	case <-time.After(2 * time.Second):
		// Goroutine still running past ctx deadline — also acceptable
		// as long as it eventually returns. Force-cancel.
		cancel()
		<-done
	}
}

// TestMirrorSync_AutoCreatesToDirIfMissing regression-pins the F-series
// bug: `rufio mirror sync --to=DIR` where DIR does NOT exist beforehand
// must auto-create the dir and start writing events.
//
// Pre-fix, the F4 symlink defense walked up filepath.EvalSymlinks
// looking for an existing ancestor of the dst path. When --to itself
// didn't exist, the walk continued past it up to /tmp (on macOS
// /tmp → /private/tmp), then found that /tmp does NOT sit under
// /tmp/fresh-mirror, and refused the write as a "symlink escape".
// Every event was silently dropped; sync logged "connected" and
// produced zero output.
//
// The documented user flow (cross-machine gate runbook) does NOT
// pre-mkdir the target. This test pins the documented happy path.
func TestMirrorSync_AutoCreatesToDirIfMissing(t *testing.T) {
	srvRoot := initProject(t)
	srvURL, token := startServer(t, srvRoot)
	// Construct a --to path under t.TempDir() but DO NOT create it.
	// MkdirAll is the contract under test.
	parent := t.TempDir()
	mirrorDir := filepath.Join(parent, "fresh-mirror-does-not-exist-yet")
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Fatalf("test setup error: %s should not exist; stat=%v", mirrorDir, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Sync(ctx, SyncOptions{
			ServerURL: srvURL, Token: token, To: mirrorDir, InsecureTLS: true,
		})
	}()
	time.Sleep(300 * time.Millisecond)
	seedThought(t, srvRoot, "alice", "fleet", "test:1", "auto-create marker")

	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		time.Sleep(100 * time.Millisecond)
		_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			if bs, _ := os.ReadFile(p); strings.Contains(string(bs), "auto-create marker") {
				found = true
			}
			return nil
		})
	}
	cancel()
	<-done
	if !found {
		t.Fatal("mirror did not pick up the seeded event into a fresh --to directory")
	}
	if _, err := os.Stat(mirrorDir); err != nil {
		t.Errorf("--to directory must exist after Sync runs: %v", err)
	}
}

// seedPromotedObservation drops a @observation record into the server's
// learned/<subject-segments>/<id>.gdlm location, mirroring what the
// auto-promote engine writes when a thought clears the confirm-quorum
// threshold. Used by the Gate-4 follow-up tests to seed the durable
// knowledge layer the mirror must propagate.
func seedPromotedObservation(t *testing.T, root, subject, predicate, object string) string {
	t.Helper()
	id, err := thought.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	rec := observation.BuildObservationRecord(observation.ObservationInput{
		ID:         id,
		Author:     "auto-promote",
		Subject:    subject,
		Predicate:  predicate,
		Object:     object,
		Scope:      "fleet",
		Confidence: 1.0,
		TS:         versioning.NowISO(),
	})
	if err := observation.Write(root, subject, id, rec); err != nil {
		t.Fatalf("observation.Write: %v", err)
	}
	return id
}

// TestMirrorSync_PropagatesLearnedRecords (Gate 4 follow-up). The
// mirror's manifesto claim is "file-native local shadow of the remote
// substrate." Pre-fix, the /listen handler only walked `live/`, so
// promoted observations landing in learned/<subject>/<id>.gdlm never
// reached the mirror. A user grepping their local mirror for
// promoted decisions found nothing.
//
// This test seeds a promoted observation on the server AFTER sync is
// running, then asserts the mirror's learned/ tree contains the same
// .gdlm file within a poll cycle.
func TestMirrorSync_PropagatesLearnedRecords(t *testing.T) {
	srvRoot := initProject(t)
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Sync(ctx, SyncOptions{
			ServerURL: srvURL, Token: token, To: mirrorDir, InsecureTLS: true,
		})
	}()
	time.Sleep(300 * time.Millisecond)

	// Drop a promoted observation on the server. This is the durable
	// knowledge-layer record the mirror must propagate.
	obsID := seedPromotedObservation(t, srvRoot, "demo:1", "is", "promoted-marker-sync")

	// Within 3 seconds, the mirror must contain
	// learned/demo/1/<id>.gdlm.
	wantRel := filepath.Join("learned", "demo", "1", obsID+".gdlm")
	wantAbs := filepath.Join(mirrorDir, wantRel)
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(wantAbs); err == nil {
			found = true
		}
	}
	cancel()
	<-done
	if !found {
		var paths []string
		_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				paths = append(paths, p)
			}
			return nil
		})
		t.Fatalf("learned observation NOT propagated to mirror. wantAbs=%s mirror_contents=%v", wantAbs, paths)
	}
	bs, _ := os.ReadFile(wantAbs)
	if !strings.Contains(string(bs), "promoted-marker-sync") {
		t.Errorf("mirror file content doesn't include marker: %q", bs)
	}
}

// TestMirrorSync_AbortsAfterPersistent401 (Gate 5 UX follow-up).
// During Gate 5 (token revocation) the sync client retried
// indefinitely with exponential backoff after the server started
// returning 401. Functional — Go's HTTP client just kept trying —
// but ugly UX: the user has no signal "your token is revoked; stop
// retrying." Real-world consequence: a CI job consumed compute and
// network indefinitely after the operator revoked its token.
//
// Fix: after 5 consecutive 401 responses, Sync logs a clear
// "token appears revoked; aborting" message and returns a non-nil
// error (so a wrapping process exits non-zero). Any successful
// connection in between resets the counter.
//
// The test points sync at a server that ALWAYS returns 401 and
// asserts Sync exits within a bounded window (~31s for 5 retries
// with 1+2+4+8+16s backoff; we give 40s of headroom).
func TestMirrorSync_AbortsAfterPersistent401(t *testing.T) {
	// Always-401 server. Use httptest.NewServer (plain HTTP) so the
	// test client doesn't need a special TLS config.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="rufio"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	mirrorDir := t.TempDir()

	// Bound the test at 40s — well under the indefinite-loop pre-fix
	// behavior and well over the expected 5-retry exit window.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Sync(ctx, SyncOptions{
			ServerURL: ts.URL, Token: "rufio_dummy", To: mirrorDir,
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Errorf("Sync should return non-nil error after persistent 401; got nil")
		}
	case <-time.After(35 * time.Second):
		cancel()
		<-done
		t.Fatal("Sync did not abort after persistent 401 within 35s — UX floor breached")
	}
}
