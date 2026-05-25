// Package stream — RED tests for the L1 minor-cleanup (end-of-catch-up
// cursor emit) and the L2 minor-cleanup (stream --from="" parity with
// listen --catch-up).
//
// L1 closes the R26 "short-pipeline gap" finding: `rufio listen --catch-up
// | tail` for a cursor in low-event substrates returned nothing in <30s
// because periodic cursor cadence is 50-events-or-30s. WatchAndEmitFrom
// must emit a final cursor record AT THE END of its catch-up replay,
// before live watch engages — turning "SDK reconnect via shell pipe"
// into a one-pipe operation.
//
// L2 closes the R26 "stream --from="" silently no-ops" finding: stream
// docs claim --from="" is "start from the epoch and replay every visible
// record first" but the code path treats empty cursor as "no replay".
// The fix is upstream in the CLI (cmd.Flags().Changed("from")), so this
// file only locks the lib-level invariant — the EmitOpts contract — and
// the end-of-catch-up cursor emit. CLI-level parity is locked in
// internal/cli/listen_cursor_test.go.
package stream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEmit_AtEndOfCatchUp_FinalCursorLine — L1 RED. After WatchAndEmitFrom
// finishes the catch-up replay (ReplayBeforeWatch || FromCursor != ""),
// it MUST emit a single {"_type":"cursor",...} JSONL line whose Value is
// the cursor pointing at the last replayed event. This happens BEFORE
// the live watch engages, so a short pipeline like `listen --catch-up |
// head -N` is guaranteed to see a cursor without waiting for the 30s
// periodic tick.
func TestEmit_AtEndOfCatchUp_FinalCursorLine(t *testing.T) {
	root := t.TempDir()
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"a.gdl", "2026-05-12T00:00:01Z"},
		{"b.gdl", "2026-05-12T00:00:02Z"},
		{"c.gdl", "2026-05-12T00:00:03Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf safeBufferCursor
	done := make(chan error, 1)
	go func() {
		// Use a huge N + huge D so neither periodic path fires during
		// the test window — the cursor we observe MUST be the
		// end-of-catch-up emit, not a periodic one.
		opts := EmitOpts{
			FromCursor:         "",
			ReplayBeforeWatch:  true,
			CursorEveryNEvents: 1000,
			CursorEveryD:       time.Hour,
		}
		done <- WatchAndEmitFrom(ctx, &buf, root, dirs, fp, opts)
	}()

	// Wait until at least one cursor appears OR a short timeout. Catch-up
	// is synchronous + happens immediately, so this should not require
	// fsnotify race-window retries.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), `"_type":"cursor"`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchAndEmitFrom didn't return after cancel")
	}

	got := buf.String()
	if !strings.Contains(got, `"_type":"cursor"`) {
		t.Fatalf("expected an end-of-catch-up cursor line, got:\n%s", got)
	}

	// All three records must be present (catch-up replay), AND a cursor
	// line must appear AFTER the last event. We assert the ordering: the
	// last "_type":"cursor" line's offset must be >= the last event
	// "_type":"thought" line's offset.
	lastEv := strings.LastIndex(got, `"_type":"thought"`)
	lastCur := strings.LastIndex(got, `"_type":"cursor"`)
	if lastEv == -1 {
		t.Fatalf("expected replayed events in output, got:\n%s", got)
	}
	if lastCur < lastEv {
		t.Fatalf("end-of-catch-up cursor must appear AFTER the last replayed event\nlastEvOffset=%d lastCurOffset=%d\noutput:\n%s",
			lastEv, lastCur, got)
	}

	// Decode the last cursor line and assert its Value points at the
	// last (chronological) replayed event.
	var rec CursorRecord
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.Contains(line, `"_type":"cursor"`) {
			continue
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad cursor JSON %q: %v", line, err)
		}
	}
	if rec.Value == "" {
		t.Fatal("cursor value is empty — must carry the opaque pass-back token")
	}
	// The cursor must equal CursorOf(last event in chronological order).
	evs, _, err := Poll(root, dirs, fp, "", 100)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("Poll returned no events; can't validate end-of-catch-up cursor")
	}
	wantCursor := CursorOf(evs[len(evs)-1])
	if rec.Value != wantCursor {
		t.Errorf("end-of-catch-up cursor value = %q, want CursorOf(last replayed) = %q",
			rec.Value, wantCursor)
	}
}

// TestListen_CatchUp_CursorAvailableInShortPipeline — L1 RED. Simulates
// the R26 short-pipeline gap: a consumer doing `rufio listen --catch-up
// | head -1 | jq` (or any small N) MUST get a cursor without waiting for
// the 30s periodic tick. We seed an empty substrate (zero events) and
// assert a cursor still appears at the end of catch-up — the
// short-pipeline case is the LEAST likely to ever see a cursor today.
//
// Note: with zero events the cursor's Value is the empty string ("" /
// epoch). That's the contract: a cursor record with empty Value is still
// a checkpoint — the consumer pass-back path treats "" as "from-epoch",
// so it's a perfectly valid resume token.
func TestListen_CatchUp_CursorAvailableInShortPipeline(t *testing.T) {
	root := t.TempDir()
	// Project marker — listen uses paths.FindProjectRoot, but the
	// lib-level WatchAndEmitFrom doesn't, so we don't strictly need it.
	// Inbox dir must exist so fsnotify add doesn't skip it.
	if err := os.MkdirAll(filepath.Join(root, "live", "inbox", "agent-a"), 0o755); err != nil {
		t.Fatal(err)
	}

	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf safeBufferCursor
	done := make(chan error, 1)
	go func() {
		opts := EmitOpts{
			FromCursor:         "",
			ReplayBeforeWatch:  true,
			CursorEveryNEvents: 1000,
			CursorEveryD:       time.Hour, // 30s tick disabled; only end-of-catch-up can produce a cursor
		}
		done <- WatchAndEmitFrom(ctx, &buf, root, dirs, fp, opts)
	}()

	// Tight deadline — the whole point is "short pipeline, no wait".
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), `"_type":"cursor"`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchAndEmitFrom didn't return after cancel")
	}

	if !strings.Contains(buf.String(), `"_type":"cursor"`) {
		t.Fatalf("short-pipeline gap: end-of-catch-up must emit a cursor even on empty substrate; got:\n%s",
			buf.String())
	}
}
