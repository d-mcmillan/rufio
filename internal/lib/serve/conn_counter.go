package serve

import (
	"sync"
	"sync/atomic"
)

// listenConnCap is the per-token cap on concurrent /listen
// connections. 32 is generous for a single-agent fleet (each agent
// typically runs 1-2 listeners — one for the CLI tail, one for the
// SDK) and bounds a hostile / buggy client to a finite resource
// footprint.
//
// Security audit M3 (v1.0.5): pre-fix, a token holder could open
// unlimited /listen connections, consuming a goroutine + a slot in
// the poll ticker for each one. With a 32-cap the worst-case memory
// footprint per token is bounded.
const listenConnCap int32 = 32

// connCounter tracks the number of active /listen connections per
// token. Tokens are keyed by their plaintext (NOT logged); the value
// is an atomic int32 incremented on connect, decremented on the
// handler's defer. Lookups go through a sync.RWMutex on the map; the
// per-token counter itself is lock-free.
//
// The counter is a package-level singleton (defaultConnCounter) shared
// by every Handler returned from Handler(). A future multi-tenant
// deployment would scope this per-config; today the substrate root is
// always a singleton.
type connCounter struct {
	mu    sync.RWMutex
	cells map[string]*atomic.Int32
}

func newConnCounter() *connCounter {
	return &connCounter{cells: make(map[string]*atomic.Int32)}
}

// tryAcquire attempts to add one to the token's counter; returns
// false if the cap is already hit. The caller MUST call release on
// the same token when the connection closes.
//
// Audit L3 follow-up: the entire acquire path runs under the
// write lock to close a race with release's delete-on-zero. The
// previous lock-free CAS-loop allowed this sequence:
//  1. Worker A releases cell X (count: 1→0)
//  2. Worker B's tryAcquire: RLock finds cell X (count=0), drops
//     RLock, starts CAS loop, Load returns 0.
//  3. Worker A's release: WLock, cell.Load() == 0 → DELETES cell X.
//  4. Worker B's CAS(0, 1) succeeds — but cell X is orphaned.
//  5. Worker B's slot is stranded; cc.count() returns 0.
//
// The full-lock posture forces tryAcquire and the delete to
// serialize, eliminating the window where a successful CAS lands
// on a freshly-deleted cell. The performance cost is tiny — /listen
// connections are long-lived and acquire-rate is low.
func (c *connCounter) tryAcquire(token string, cap int32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	cell, ok := c.cells[token]
	if !ok {
		cell = &atomic.Int32{}
		c.cells[token] = cell
	}
	cur := cell.Load()
	if cur >= cap {
		return false
	}
	cell.Add(1)
	return true
}

// release decrements the token's counter. Safe to call even if the
// cell isn't present (the cell is created on first acquire).
//
// Audit L3 (v1.0.5 follow-up): when release drops the cell to zero,
// delete the map entry so a long-running server doesn't leak cells
// for every token it has ever seen. The decrement + delete-check
// runs entirely under the write lock — matching tryAcquire — so a
// concurrent acquire can never land on a freshly-deleted cell.
func (c *connCounter) release(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cell, ok := c.cells[token]
	if !ok {
		return
	}
	if cell.Add(-1) == 0 {
		delete(c.cells, token)
	}
}

// count returns the current connection count for a token. Exposed
// for tests; production code uses tryAcquire/release exclusively.
func (c *connCounter) count(token string) int32 {
	c.mu.RLock()
	cell, ok := c.cells[token]
	c.mu.RUnlock()
	if !ok {
		return 0
	}
	return cell.Load()
}

// defaultConnCounter is the package-level singleton used by the
// listen handler. Tests can construct their own counter via
// newConnCounter().
var defaultConnCounter = newConnCounter()
