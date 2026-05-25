// Package cli — tripwire test for the TTLSweeper wiring in dev.go.
//
// TTLSweeper is unique among daemon engines: it WRITES to live/expired/
// (via ttlsweep.Move) but no engine READS events from there. The dispatch
// table must NOT route live/expired/*.gdl events to any handler — if a
// future change accidentally adds a row for that prefix, this test fails
// before the feedback loop bites.
//
// End-to-end behaviour (ticker firing, catch-up sweep on startup) is
// covered by the integration suite in Task 4.
package cli

import (
	"testing"
)

// TestDefaultEventHandler_IgnoresExpiredEvents asserts no engine in the
// dispatch table reacts to events under live/expired/. TTLSweeper writes
// there but no engine reads from there — a future regression that wires
// a handler would be caught here.
func TestDefaultEventHandler_IgnoresExpiredEvents(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	for _, kind := range []string{"add", "change", "unlink"} {
		err := h(FileEvent{Kind: kind, Path: "live/expired/agent-a/x.gdl"})
		if err != nil {
			t.Errorf("kind=%s: expected nil (no handler), got %v", kind, err)
		}
	}
}
