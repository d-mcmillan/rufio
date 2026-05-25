// Package cli — tests for the RouteSummon wiring in dev.go.
//
// Tripwire tests asserting that fsnotify Create events under
// live/summons/pending/<id>.gdl reach routing.RouteSummon, and that
// events under sibling state directories (accepted/declined/expired)
// do NOT match any dispatch entry (they aren't routed — RouteSummon
// only fires on pending arrivals per design §2.I).
package cli

import (
	"testing"
)

// TestDefaultEventHandler_RoutesSummonPending_ToRouteSummon proves the
// dispatch entry forwards live/summons/pending/*.gdl add events to
// routing.RouteSummon. With no file on disk at the synthetic path,
// RouteSummon's os.ReadFile returns a non-nil *fs.PathError — observing
// that non-nil error proves the closure was invoked.
func TestDefaultEventHandler_RoutesSummonPending_ToRouteSummon(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "add", Path: "live/summons/pending/fake-id.gdl"})
	if err == nil {
		t.Fatal("expected non-nil error (proves dispatch reached RouteSummon)")
	}
}

// TestDefaultEventHandler_IgnoresSummonsAcceptedDeclined asserts that
// events under live/summons/accepted, declined, expired do NOT match any
// dispatch entry — the handler returns nil (no-op) for these paths.
func TestDefaultEventHandler_IgnoresSummonsAcceptedDeclined(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	for _, sub := range []string{"accepted", "declined", "expired"} {
		err := h(FileEvent{Kind: "add", Path: "live/summons/" + sub + "/fake-id.gdl"})
		if err != nil {
			t.Errorf("%s: expected nil (no handler), got %v", sub, err)
		}
	}
}
