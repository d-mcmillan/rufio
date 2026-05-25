package stream

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBufferCursor is a goroutine-safe writer for the streaming tests.
// (stream_test.go has the same shape — duplicated here so this test file
// stays self-contained while #155's RED commit lands without touching
// stream_test.go.)
type safeBufferCursor struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBufferCursor) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBufferCursor) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// seedInboxRecords writes one @thought per timestamp under live/inbox/agent-a
// — the same shape poll_test.go's seedInbox uses, copied so #155's RED
// commit doesn't reach across files.
func seedInboxRecords(t *testing.T, root string, recs []struct{ name, ts string }) {
	t.Helper()
	inbox := filepath.Join(root, "live", "inbox", "agent-a")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		body := "@thought|ts:" + r.ts + "|author:other|subject:agent::agent-a|content:hi|scope:fleet\n"
		if err := os.WriteFile(filepath.Join(inbox, r.name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestEncodeCursor_ExportedSymbolMatchesPollFormat — the public encoder
// must produce a value bit-identical to the internal one Poll already
// uses, so CLI consumers and MCP consumers share one wire format.
func TestEncodeCursor_ExportedSymbolMatchesPollFormat(t *testing.T) {
	ts := "2026-05-12T00:00:01.000000000Z"
	path := "live/inbox/agent-a/x.gdl"
	got := EncodeCursor(ts, path)
	want := base64.RawURLEncoding.EncodeToString([]byte(ts + "\x00" + path))
	if got != want {
		t.Fatalf("EncodeCursor != Poll wire format: got %q want %q", got, want)
	}
}

// TestDecodeCursor_ExportedSymbolRoundtrips — the public decoder must
// split exactly like Poll's internal split.
func TestDecodeCursor_ExportedSymbolRoundtrips(t *testing.T) {
	ts := "2026-05-12T00:00:01.000000000Z"
	path := "live/inbox/agent-a/x.gdl"
	c := EncodeCursor(ts, path)
	gotTS, gotPath, err := DecodeCursor(c)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if gotTS != ts || gotPath != path {
		t.Fatalf("roundtrip mismatch: ts=%q path=%q want ts=%q path=%q", gotTS, gotPath, ts, path)
	}
}

// TestCursorOf_MatchesPollNextCursor — the cursor of the last event in a
// page must equal Poll's next_cursor for the same record set. Locks the
// "byte-for-byte identical to MCP Poll" non-negotiable from #155.
func TestCursorOf_MatchesPollNextCursor(t *testing.T) {
	root := t.TempDir()
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"a.gdl", "2026-05-12T00:00:01Z"},
		{"b.gdl", "2026-05-12T00:00:02Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}
	evs, pollNext, err := Poll(root, dirs, fp, "", 100)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("Poll returned no events; can't compare cursor format")
	}
	got := CursorOf(evs[len(evs)-1])
	if got != pollNext {
		t.Fatalf("CursorOf(last) = %q, Poll.next_cursor = %q — formats must match byte-for-byte", got, pollNext)
	}
}

// TestEmitCatchUpFrom_EmitsOnlyAfterCursor — RED for the core spec:
// `EmitCatchUpFrom(.., fromCursor)` returns only events strictly AFTER
// that cursor, in chronological (canonicalTS,path) order. Mirrors the
// MCP Poll contract on the CLI side.
func TestEmitCatchUpFrom_EmitsOnlyAfterCursor(t *testing.T) {
	root := t.TempDir()
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"a.gdl", "2026-05-12T00:00:01Z"},
		{"b.gdl", "2026-05-12T00:00:02Z"},
		{"c.gdl", "2026-05-12T00:00:03Z"},
		{"d.gdl", "2026-05-12T00:00:04Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	// Step 1: poll page 1 (2 records) to obtain a cursor at b.gdl.
	page1, after2, err := Poll(root, dirs, fp, "", 2)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("Poll page1 = %d events, want 2", len(page1))
	}

	// Step 2: EmitCatchUpFrom with cursor at b.gdl must emit only c+d.
	var buf bytes.Buffer
	last, err := EmitCatchUpFrom(&buf, root, dirs, fp, after2)
	if err != nil {
		t.Fatalf("EmitCatchUpFrom: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `"ts":"2026-05-12T00:00:01Z"`) {
		t.Errorf("a.gdl leaked through cursor filter: %q", out)
	}
	if strings.Contains(out, `"ts":"2026-05-12T00:00:02Z"`) {
		t.Errorf("b.gdl leaked through cursor filter (was AT cursor, not strictly-after): %q", out)
	}
	if !strings.Contains(out, `"ts":"2026-05-12T00:00:03Z"`) {
		t.Errorf("c.gdl missing — strictly-after cursor must include it: %q", out)
	}
	if !strings.Contains(out, `"ts":"2026-05-12T00:00:04Z"`) {
		t.Errorf("d.gdl missing: %q", out)
	}
	// Returned last-cursor must match Poll's cursor for d.gdl.
	wantLast, _, err := Poll(root, dirs, fp, after2, 100)
	if err != nil {
		t.Fatalf("verify Poll: %v", err)
	}
	if len(wantLast) == 0 {
		t.Fatal("verify Poll returned no events after the partial cursor")
	}
	if last != CursorOf(wantLast[len(wantLast)-1]) {
		t.Errorf("EmitCatchUpFrom returned last=%q, want CursorOf(last event)=%q",
			last, CursorOf(wantLast[len(wantLast)-1]))
	}
}

// TestEmitCatchUpFrom_EmptyCursorIsFromEpoch — empty cursor matches
// today's `--catch-up` semantic: emit every visible record in
// chronological order, return cursor of the last one.
func TestEmitCatchUpFrom_EmptyCursorIsFromEpoch(t *testing.T) {
	root := t.TempDir()
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"a.gdl", "2026-05-12T00:00:01Z"},
		{"b.gdl", "2026-05-12T00:00:02Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	var buf bytes.Buffer
	last, err := EmitCatchUpFrom(&buf, root, dirs, fp, "")
	if err != nil {
		t.Fatalf("EmitCatchUpFrom: %v", err)
	}
	if !strings.Contains(buf.String(), `"ts":"2026-05-12T00:00:01Z"`) ||
		!strings.Contains(buf.String(), `"ts":"2026-05-12T00:00:02Z"`) {
		t.Fatalf("empty cursor must replay everything, got %q", buf.String())
	}
	if last == "" {
		t.Error("non-empty replay must return non-empty last cursor")
	}
}

// TestEmitCatchUpFrom_CursorRoundtripsThroughPoll — feed the cursor
// returned by EmitCatchUpFrom into Poll and confirm Poll continues from
// the same point. Locks the symmetric-contract guarantee.
func TestEmitCatchUpFrom_CursorRoundtripsThroughPoll(t *testing.T) {
	root := t.TempDir()
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"a.gdl", "2026-05-12T00:00:01Z"},
		{"b.gdl", "2026-05-12T00:00:02Z"},
		{"c.gdl", "2026-05-12T00:00:03Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	// Replay everything via EmitCatchUpFrom, capture last cursor.
	var buf bytes.Buffer
	last, err := EmitCatchUpFrom(&buf, root, dirs, fp, "")
	if err != nil {
		t.Fatalf("EmitCatchUpFrom: %v", err)
	}
	if last == "" {
		t.Fatal("expected non-empty last cursor after full replay")
	}
	// Now seed a new event AFTER the cursor.
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"d.gdl", "2026-05-12T00:00:04Z"},
	})
	// Poll continuing from EmitCatchUpFrom's last cursor must see only d.
	evs, _, err := Poll(root, dirs, fp, last, 100)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(evs) != 1 || evs[0].TS != "2026-05-12T00:00:04Z" {
		t.Fatalf("Poll after EmitCatchUpFrom cursor returned %+v, want [d.gdl]", evs)
	}
}

// TestEmitCatchUpFrom_MalformedCursorIsError — invalid cursor surfaces a
// "invalid cursor" error, matching Poll's behaviour.
func TestEmitCatchUpFrom_MalformedCursorIsError(t *testing.T) {
	root := t.TempDir()
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"a.gdl", "2026-05-12T00:00:01Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	var buf bytes.Buffer
	_, err := EmitCatchUpFrom(&buf, root, dirs, fp, "!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected error for malformed cursor")
	}
	if !strings.Contains(err.Error(), "invalid cursor") {
		t.Fatalf("error %q must contain 'invalid cursor'", err.Error())
	}
}

// TestWatchAndEmitFrom_EmitsPeriodicCursor — RED for the second core
// spec deliverable: streaming consumers must see a {"_type":"cursor",...}
// line in stdout so they can checkpoint without parsing every record.
// Cadence is configurable (every N events OR every D seconds); this test
// drives N=1 so a single record forces a cursor emit immediately.
func TestWatchAndEmitFrom_EmitsPeriodicCursor(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "live", "inbox", "agent-a")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf safeBufferCursor
	done := make(chan error, 1)
	go func() {
		opts := EmitOpts{
			FromCursor:         "",
			CursorEveryNEvents: 1,
			CursorEveryD:       time.Hour, // ensure event-count path triggers, not time path
		}
		done <- WatchAndEmitFrom(ctx, &buf, root, []string{"live/inbox/agent-a"}, FilterParams{CurrentAgent: "agent-a"}, opts)
	}()
	time.Sleep(300 * time.Millisecond)
	body := "@thought|ts:2026-05-12T00:00:01Z|author:other|subject:agent::agent-a|content:hi|scope:fleet\n"
	target := filepath.Join(inbox, "t1.gdl")
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Same fsnotify-race retry shape as the existing WatchAndEmit test.
	deadline := time.Now().Add(5 * time.Second)
	var got string
	nextRewrite := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		got = buf.String()
		if strings.Contains(got, `"_type":"cursor"`) {
			break
		}
		if time.Now().After(nextRewrite) {
			_ = os.WriteFile(target, []byte(body), 0o644)
			nextRewrite = time.Now().Add(500 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchAndEmitFrom didn't return after cancel")
	}
	if !strings.Contains(got, `"_type":"cursor"`) {
		t.Fatalf("expected a periodic cursor line in output, got %q", got)
	}
	// The cursor line must be valid JSONL with the expected shape.
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.Contains(line, `"_type":"cursor"`) {
			continue
		}
		var rec CursorRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("invalid cursor JSON %q: %v", line, err)
		}
		if rec.Type != "cursor" {
			t.Errorf("rec.Type = %q, want %q", rec.Type, "cursor")
		}
		if rec.Value == "" {
			t.Error("cursor value is empty — must carry the opaque pass-back token")
		}
		if rec.TS == "" {
			t.Error("cursor ts is empty — should carry the canonical-TS hint for telemetry")
		}
		return
	}
	t.Fatal("could not find a parseable cursor line")
}

// TestWatchAndEmitFrom_FromCursorReplaysCatchUp — RED for the resume
// path: `--from=<cursor>` on `rufio listen` (the SDK reconnect contract)
// implies an implicit bounded catch-up of every record strictly after
// the cursor, then live-watch from there. The test seeds pre-existing
// records, computes a partial cursor, restarts the watch with that
// cursor, and asserts only the strictly-after records replay.
func TestWatchAndEmitFrom_FromCursorReplaysCatchUp(t *testing.T) {
	root := t.TempDir()
	seedInboxRecords(t, root, []struct{ name, ts string }{
		{"a.gdl", "2026-05-12T00:00:01Z"},
		{"b.gdl", "2026-05-12T00:00:02Z"},
		{"c.gdl", "2026-05-12T00:00:03Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	// Take a cursor at b (page 1 of Poll up to b).
	_, afterB, err := Poll(root, dirs, fp, "", 2)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf safeBufferCursor
	done := make(chan error, 1)
	go func() {
		opts := EmitOpts{FromCursor: afterB, CursorEveryNEvents: 100, CursorEveryD: time.Hour}
		done <- WatchAndEmitFrom(ctx, &buf, root, dirs, fp, opts)
	}()
	// Give the catch-up replay time to flush before cancelling.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), `"ts":"2026-05-12T00:00:03Z"`) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchAndEmitFrom didn't return after cancel")
	}
	got := buf.String()
	if strings.Contains(got, `"ts":"2026-05-12T00:00:01Z"`) {
		t.Errorf("a.gdl leaked through fromCursor: %q", got)
	}
	if strings.Contains(got, `"ts":"2026-05-12T00:00:02Z"`) {
		t.Errorf("b.gdl leaked through fromCursor (was AT, not strictly-after): %q", got)
	}
	if !strings.Contains(got, `"ts":"2026-05-12T00:00:03Z"`) {
		t.Errorf("c.gdl missing from fromCursor replay: %q", got)
	}
}

// TestWatchAndEmitFrom_CursorIsOpaqueAndRoundtrips — the cursor a
// consumer sees on stdout must be the same opaque value they can pass
// back via FromCursor. No parsing on the consumer side.
func TestWatchAndEmitFrom_CursorIsOpaqueAndRoundtrips(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "live", "inbox", "agent-a")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed 2 records pre-watch so the initial catch-up emits a cursor.
	for i, ts := range []string{"2026-05-12T00:00:01Z", "2026-05-12T00:00:02Z"} {
		body := "@thought|ts:" + ts + "|author:other|subject:agent::agent-a|content:hi|scope:fleet\n"
		name := fmt.Sprintf("seed-%d.gdl", i)
		if err := os.WriteFile(filepath.Join(inbox, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf safeBufferCursor
	done := make(chan error, 1)
	go func() {
		// Drive a catch-up by passing FromCursor:"" with replay.
		opts := EmitOpts{FromCursor: "", CursorEveryNEvents: 1, CursorEveryD: time.Hour, ReplayBeforeWatch: true}
		done <- WatchAndEmitFrom(ctx, &buf, root, []string{"live/inbox/agent-a"}, FilterParams{CurrentAgent: "agent-a"}, opts)
	}()
	// Wait for the cursor line to appear.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), `"_type":"cursor"`) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first WatchAndEmitFrom didn't return after cancel")
	}

	// Extract a cursor.
	var captured string
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if !strings.Contains(line, `"_type":"cursor"`) {
			continue
		}
		var rec CursorRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad cursor JSON %q: %v", line, err)
		}
		captured = rec.Value
	}
	if captured == "" {
		t.Fatal("no cursor value captured")
	}

	// Pass it back: only strictly-after records (none yet, but no crash + no replay of seeded ones).
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	var buf2 safeBufferCursor
	done2 := make(chan error, 1)
	go func() {
		opts := EmitOpts{FromCursor: captured, CursorEveryNEvents: 100, CursorEveryD: time.Hour, ReplayBeforeWatch: true}
		done2 <- WatchAndEmitFrom(ctx2, &buf2, root, []string{"live/inbox/agent-a"}, FilterParams{CurrentAgent: "agent-a"}, opts)
	}()
	time.Sleep(500 * time.Millisecond)
	cancel2()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("second WatchAndEmitFrom didn't return after cancel")
	}
	if strings.Contains(buf2.String(), `"_type":"thought"`) {
		t.Fatalf("passing the cursor back replayed already-seen records: %q", buf2.String())
	}
}
