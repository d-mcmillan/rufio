package serve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// TestConnCounter_TryAcquireEnforcesCapPerToken pins the M3 logic at
// the unit level — multiple acquires by the same token cumulate; the
// (cap+1)th acquire fails; different tokens have independent counters.
func TestConnCounter_TryAcquireEnforcesCapPerToken(t *testing.T) {
	cc := newConnCounter()
	const cap int32 = 3

	// 3 acquires by alice → all succeed.
	for i := 0; i < int(cap); i++ {
		if !cc.tryAcquire("alice", cap) {
			t.Fatalf("acquire %d/3 for alice should succeed", i+1)
		}
	}
	// 4th acquire fails.
	if cc.tryAcquire("alice", cap) {
		t.Errorf("acquire 4/3 for alice should fail at the cap")
	}
	// bob has his own counter.
	if !cc.tryAcquire("bob", cap) {
		t.Errorf("bob's 1st acquire should succeed (independent counter)")
	}
	if cc.count("alice") != 3 {
		t.Errorf("alice count = %d, want 3", cc.count("alice"))
	}
	if cc.count("bob") != 1 {
		t.Errorf("bob count = %d, want 1", cc.count("bob"))
	}

	// Release one alice slot — next acquire succeeds.
	cc.release("alice")
	if !cc.tryAcquire("alice", cap) {
		t.Errorf("acquire after release should succeed")
	}
}

// TestConnCounter_ConcurrentAcquireSafe drives 100 goroutines all
// trying to acquire under a cap of 32; the final count MUST be 32
// (no over-acquire, no under-acquire). Catches a CAS race.
func TestConnCounter_ConcurrentAcquireSafe(t *testing.T) {
	cc := newConnCounter()
	const cap int32 = 32
	var wg sync.WaitGroup
	successes := int32(0)
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cc.tryAcquire("alice", cap) {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != cap {
		t.Errorf("expected exactly %d successful acquires under concurrent load, got %d", cap, successes)
	}
	if cc.count("alice") != cap {
		t.Errorf("post-race count = %d, want %d", cc.count("alice"), cap)
	}
}

// TestListen_RejectsAt33rdConnection is the integration-level guard:
// 32 simultaneous /listen connections succeed (the cap), the 33rd
// returns 429 Too Many Requests with a Retry-After hint.
func TestListen_RejectsAt33rdConnection(t *testing.T) {
	root := setupServeProject(t)
	plaintext, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	handler, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Reset the shared counter so other tests don't pre-fill alice's
	// cell. Since we use a package-level singleton, this is the
	// simplest way to isolate.
	defaultConnCounter.mu.Lock()
	delete(defaultConnCounter.cells, plaintext)
	defaultConnCounter.mu.Unlock()

	// Open 32 long-lived /listen connections. We close them via
	// context cancel at test teardown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	openConnections := make([]*http.Response, 0, 32)
	for i := 0; i < 32; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/listen", nil)
		req.Header.Set("Authorization", "Bearer "+plaintext)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("connect %d: %v", i+1, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("connect %d: status %d, want 200", i+1, resp.StatusCode)
		}
		openConnections = append(openConnections, resp)
	}
	defer func() {
		for _, r := range openConnections {
			_ = r.Body.Close()
		}
	}()

	// 33rd connection MUST be rejected with 429.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/listen", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("33rd connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("33rd connection: status %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Errorf("expected Retry-After header on 429; got empty")
	}
}

// TestListen_DifferentTokensIndependent pins that two tokens have
// completely independent counters — alice's 32 cap doesn't block
// bob.
func TestListen_DifferentTokensIndependent(t *testing.T) {
	root := setupServeProject(t)
	aliceTok, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken alice: %v", err)
	}
	bobTok, _, err := admin.MintToken(root, "bob")
	if err != nil {
		t.Fatalf("MintToken bob: %v", err)
	}
	handler, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Pre-fill alice's counter to the cap so any further connection
	// for alice would be rejected.
	defaultConnCounter.mu.Lock()
	defaultConnCounter.cells[aliceTok] = nil
	delete(defaultConnCounter.cells, aliceTok)
	delete(defaultConnCounter.cells, bobTok)
	defaultConnCounter.mu.Unlock()
	for i := 0; i < int(listenConnCap); i++ {
		_ = defaultConnCounter.tryAcquire(aliceTok, listenConnCap)
	}
	defer func() {
		for i := 0; i < int(listenConnCap); i++ {
			defaultConnCounter.release(aliceTok)
		}
	}()

	// bob connects — MUST succeed, alice's cap is irrelevant.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/listen", nil)
	req.Header.Set("Authorization", "Bearer "+bobTok)
	req.Header.Set("Accept", "text/event-stream")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bob connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bob connect: status %d, want 200 (alice's cap should not affect bob)", resp.StatusCode)
	}
}

// TestConnCounter_DeletesZeroCells — audit L3 follow-up.
//
// Pre-fix, connCounter.release decremented the per-token atomic but
// NEVER deleted the cell entry from the map. A long-running server
// that sees N distinct tokens connect-then-disconnect over its
// lifetime accumulates N entries in the map indefinitely — a slow
// memory leak. The fix: when release drops a cell to zero, delete
// it from the map under the write lock (with a re-check, so we
// don't race a fresh acquire).
//
// Verification posture: acquire N tokens to N independent cells,
// release each; expect the map to be empty afterward.
func TestConnCounter_DeletesZeroCells(t *testing.T) {
	cc := newConnCounter()
	const tokens = 50
	const cap int32 = 4

	// Acquire 2 slots per token (well under the cap), then release
	// both. Final state: every cell should be deleted.
	for i := 0; i < tokens; i++ {
		tok := fmt.Sprintf("tok-%d", i)
		if !cc.tryAcquire(tok, cap) {
			t.Fatalf("acquire #1 for %s should succeed", tok)
		}
		if !cc.tryAcquire(tok, cap) {
			t.Fatalf("acquire #2 for %s should succeed", tok)
		}
	}
	for i := 0; i < tokens; i++ {
		tok := fmt.Sprintf("tok-%d", i)
		cc.release(tok)
		cc.release(tok)
	}

	// Inspect the map directly — every cell should be gone.
	cc.mu.RLock()
	remaining := len(cc.cells)
	cc.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("connCounter.cells leaked: %d entries remain after release-to-zero (want 0)", remaining)
	}
}

// TestConnCounter_RaceFreshAcquireAcrossRelease pins that the
// delete-on-zero path does NOT discard a cell whose counter races
// back above zero between the decrement and the lock acquisition.
// The release path's re-check under the write lock is the defense.
//
// Strategy: start the long-holder FIRST and let it acquire its
// slot deterministically. THEN spin up the churn goroutine that
// repeatedly acquires-and-releases. Throughout, the long-holder's
// count() must observe ≥1 (its own slot is never released until
// goroutine exit). This is the load-bearing assertion — that L3's
// delete-on-zero doesn't strand the long-holder's slot on an
// orphaned cell while a new cell takes its place in the map.
//
// Pre-fix (no delete at all) this passed trivially. Post-fix the
// re-check-under-lock + cell-identity comparison are what keep it
// passing — if the release path deleted the cell while the long-
// holder still held a slot, count() would read 0 from a fresh
// empty cell created by the worker.
func TestConnCounter_RaceFreshAcquireAcrossRelease(t *testing.T) {
	cc := newConnCounter()
	const cap int32 = 1000

	// Step 1: long-holder acquires its slot BEFORE the worker
	// starts. This eliminates the "long-holder hasn't reached
	// tryAcquire yet" race that made the original test flake on
	// loaded CI runners.
	if !cc.tryAcquire("token-x", cap) {
		t.Fatalf("long-holder acquire failed unexpectedly")
	}
	defer cc.release("token-x")

	// Sanity: from the long-holder's perspective, count is now 1.
	if c := cc.count("token-x"); c < 1 {
		t.Fatalf("post-acquire count = %d, want >= 1", c)
	}

	// Step 2: spawn a worker that churns acquires + releases.
	// Each iteration MAY trigger the L3 delete-on-zero path if
	// the worker's release temporarily drops count to 0 — but
	// the long-holder's slot keeps it pinned, so the delete
	// branch's `cell.Load() == 0` re-check fails and the cell
	// stays alive.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if cc.tryAcquire("token-x", cap) {
					cc.release("token-x")
				}
			}
		}
	}()

	// Step 3: long-holder polls count() many times. With the
	// long-holder's slot held continuously, count MUST stay >= 1.
	const iters = 1000
	for i := 0; i < iters; i++ {
		if c := cc.count("token-x"); c < 1 {
			close(stop)
			wg.Wait()
			t.Fatalf("long-holder's slot lost at iter %d: count=%d (cell may have been deleted while still in use)", i, c)
		}
	}
	close(stop)
	wg.Wait()
}

// Stub for the test file to compile when run in isolation —
// fmt.Sprintf usage in case future test additions need it.
var _ = fmt.Sprintf
