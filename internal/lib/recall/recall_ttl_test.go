package recall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- TTL filtering (#149) ---
//
// TTL semantics (D5.1, thought.ParseTTL):
//   - ttl is integer seconds; 0 = never expire
//   - a record is "TTL-expired" when ttl > 0 AND ts + ttl*seconds < now
//
// Filter must:
//   - default (IncludeExpired=false): hide TTL-expired records same way
//     it hides retracted records (symmetric design per #149).
//   - IncludeExpired=true: surface BOTH retracted AND TTL-expired records.

// TestFilter_DefaultHidesTTLExpired asserts that with IncludeExpired=false
// (the default), a record whose ts + ttl is in the past is filtered out.
func TestFilter_DefaultHidesTTLExpired(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	// Wrote 10 minutes ago with ttl=60s → expired 9 minutes ago.
	tsExpired := now.Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	// Wrote 10 minutes ago with ttl=3600s → still valid (50 min left).
	tsLive := now.Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)

	all := recs(
		RecallRecord{Type: "thought", TS: tsExpired, TTL: 60},
		RecallRecord{Type: "thought", TS: tsLive, TTL: 3600},
		// ttl=0 → never expires; always passes.
		RecallRecord{Type: "thought", TS: tsExpired, TTL: 0},
	)
	got := Filter(all, FilterParams{Now: now})
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (the ttl=60 expired one should be hidden), got=%+v", len(got), got)
	}
	for _, r := range got {
		if r.TTL == 60 {
			t.Errorf("expired ttl=60 record leaked through default view: %+v", r)
		}
	}
}

// TestFilter_IncludeExpired_SurfacesTTLExpired asserts that with
// IncludeExpired=true, the TTL-expired record IS surfaced (symmetric
// with retracted-record handling).
func TestFilter_IncludeExpired_SurfacesTTLExpired(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	tsExpired := now.Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)

	all := recs(
		RecallRecord{Type: "thought", TS: tsExpired, TTL: 60},
	)
	got := Filter(all, FilterParams{IncludeExpired: true, Now: now})
	if len(got) != 1 {
		t.Errorf("len=%d want 1 (--include-expired should surface the TTL-expired record)", len(got))
	}
}

// TestFilter_TTLZero_NeverExpires asserts that ttl=0 records always pass,
// regardless of age. (D5.1: 0 = never expire.)
func TestFilter_TTLZero_NeverExpires(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	ancient := now.Add(-365 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	all := recs(
		RecallRecord{Type: "thought", TS: ancient, TTL: 0},
	)
	got := Filter(all, FilterParams{Now: now})
	if len(got) != 1 {
		t.Errorf("len=%d want 1 (ttl=0 must always be visible)", len(got))
	}
}

// TestFilter_TTLExpired_BadTimestamp_PassesThrough asserts the filter is
// tolerant of unparseable timestamps — they pass through the TTL check
// (defensive: the SinceFloor/AsOf checks already drop them via skip).
// This keeps the new TTL gate from regressing on synthetic test records
// whose TS is empty (the existing tests use TS: "" extensively).
func TestFilter_TTLExpired_BadTimestamp_PassesThrough(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	all := recs(
		RecallRecord{Type: "thought", TS: "", TTL: 60},
		RecallRecord{Type: "thought", TS: "not-a-timestamp", TTL: 60},
	)
	got := Filter(all, FilterParams{Now: now})
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (unparseable TS should pass the TTL check)", len(got))
	}
}

// TestScanOutbox_ReadsTTL asserts that scanOutbox populates the new TTL
// field on RecallRecord (needed for Filter to gate on it).
func TestScanOutbox_ReadsTTL(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "agent-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := "@thought|id:1-x|author:agent-a|type:hypothesis|subject:customer\\:1|" +
		"content:c|scope:fleet|ts:2026-05-20T12:00:00Z|ttl:30\n"
	if err := os.WriteFile(filepath.Join(dir, "1-x.gdl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := scanOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].TTL != 30 {
		t.Errorf("TTL=%d want 30 (scanOutbox must populate TTL)", got[0].TTL)
	}
}
