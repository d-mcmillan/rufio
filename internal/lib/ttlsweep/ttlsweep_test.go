package ttlsweep

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// --- IsExpired -------------------------------------------------------------

func TestIsExpired_BeforeBoundary(t *testing.T) {
	ts := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	ttl := 300 * time.Second
	now := ts.Add(ttl) // exactly at boundary; spec says strict `>`
	if IsExpired(ts, ttl, now) {
		t.Errorf("IsExpired(boundary) = true; want false (strict >)")
	}
}

func TestIsExpired_AfterBoundary(t *testing.T) {
	ts := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	ttl := 300 * time.Second
	now := ts.Add(ttl + time.Millisecond)
	if !IsExpired(ts, ttl, now) {
		t.Errorf("IsExpired(boundary+1ms) = false; want true")
	}
}

func TestIsExpired_TTLZero_NeverExpires(t *testing.T) {
	ts := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	now := ts.Add(100 * 365 * 24 * time.Hour) // a century later
	if IsExpired(ts, 0, now) {
		t.Errorf("IsExpired(ttl=0) = true; want false (D5.1 never-expire)")
	}
}

func TestIsExpired_TTLNegative_NeverExpires(t *testing.T) {
	ts := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	now := ts.Add(time.Hour)
	if IsExpired(ts, -1*time.Second, now) {
		t.Errorf("IsExpired(ttl<0) = true; want false (defensive)")
	}
}

// --- ParseThoughtTTL -------------------------------------------------------

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("setup: parse %q: %v", s, err)
	}
	return tt
}

func TestParseThoughtTTL_HappyPath(t *testing.T) {
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-aaa111"},
		{Key: "ts", Value: "2026-05-12T00:00:00Z"},
		{Key: "ttl", Value: "300"},
	}}
	ts, ttl := ParseThoughtTTL(rec)
	want := mustParseTime(t, "2026-05-12T00:00:00Z")
	if !ts.Equal(want) {
		t.Errorf("ts = %v; want %v", ts, want)
	}
	if ttl != 300*time.Second {
		t.Errorf("ttl = %v; want 300s", ttl)
	}
}

func TestParseThoughtTTL_MissingTTL(t *testing.T) {
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "ts", Value: "2026-05-12T00:00:00Z"},
	}}
	ts, ttl := ParseThoughtTTL(rec)
	if !ts.IsZero() {
		t.Errorf("ts = %v; want zero (no ttl field)", ts)
	}
	if ttl != 0 {
		t.Errorf("ttl = %v; want 0", ttl)
	}
}

func TestParseThoughtTTL_TTLZero(t *testing.T) {
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "ts", Value: "2026-05-12T00:00:00Z"},
		{Key: "ttl", Value: "0"},
	}}
	ts, ttl := ParseThoughtTTL(rec)
	if !ts.IsZero() {
		t.Errorf("ts = %v; want zero (ttl=0 short-circuit)", ts)
	}
	if ttl != 0 {
		t.Errorf("ttl = %v; want 0", ttl)
	}
}

func TestParseThoughtTTL_MalformedTS(t *testing.T) {
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "ts", Value: "not-a-date"},
		{Key: "ttl", Value: "300"},
	}}
	ts, ttl := ParseThoughtTTL(rec)
	if !ts.IsZero() || ttl != 0 {
		t.Errorf("ParseThoughtTTL(bad ts) = (%v, %v); want (zero, 0)", ts, ttl)
	}
}

func TestParseThoughtTTL_MalformedTTL(t *testing.T) {
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "ts", Value: "2026-05-12T00:00:00Z"},
		{Key: "ttl", Value: "abc"},
	}}
	ts, ttl := ParseThoughtTTL(rec)
	if !ts.IsZero() || ttl != 0 {
		t.Errorf("ParseThoughtTTL(bad ttl) = (%v, %v); want (zero, 0)", ts, ttl)
	}
}

// --- FindExpired -----------------------------------------------------------

// seedThought writes a single-line @thought record to <root>/<rel>.
func seedThought(t *testing.T, root, rel, id, ts string, ttlSeconds int) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: id},
		{Key: "author", Value: "agent-x"},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: "customer:1"},
		{Key: "content", Value: "c"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: ts},
		{Key: "ttl", Value: itoa(ttlSeconds)},
	}}
	line := gdl.RenderLine(rec) + "\n"
	if err := os.WriteFile(abs, []byte(line), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func itoa(n int) string {
	// keep test helper local — avoid pulling strconv to top of file noise.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func fixedNow(s string) func() time.Time {
	tt, _ := time.Parse(time.RFC3339Nano, s)
	return func() time.Time { return tt }
}

func TestFindExpired_OutboxOnly(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "live/outbox/agent-a/1-aaa111.gdl",
		"1-aaa111", "2026-05-12T00:00:00Z", 60)

	now := fixedNow("2026-05-12T00:02:00Z") // 120s later; ttl=60 → expired
	got, err := FindExpired(root, now)
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d; want 1 (entries=%v)", len(got), got)
	}
	g := got[0]
	if g.Agent != "agent-a" {
		t.Errorf("Agent = %q; want agent-a", g.Agent)
	}
	if g.ID != "1-aaa111" {
		t.Errorf("ID = %q; want 1-aaa111", g.ID)
	}
	wantPath := filepath.Join(root, "live", "outbox", "agent-a", "1-aaa111.gdl")
	if g.SourcePath != wantPath {
		t.Errorf("SourcePath = %q; want %q", g.SourcePath, wantPath)
	}
}

func TestFindExpired_InboxOnly(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "live/inbox/agent-b/2-bbb222.gdl",
		"2-bbb222", "2026-05-12T00:00:00Z", 30)

	now := fixedNow("2026-05-12T00:01:00Z") // 60s later; ttl=30 → expired
	got, err := FindExpired(root, now)
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d; want 1", len(got))
	}
	if got[0].Agent != "agent-b" || got[0].ID != "2-bbb222" {
		t.Errorf("entry = %+v; want agent-b/2-bbb222", got[0])
	}
}

func TestFindExpired_BothLocations_SameID(t *testing.T) {
	root := t.TempDir()
	id := "3-ccc333"
	seedThought(t, root, "live/outbox/agent-a/"+id+".gdl", id,
		"2026-05-12T00:00:00Z", 60)
	seedThought(t, root, "live/inbox/agent-b/"+id+".gdl", id,
		"2026-05-12T00:00:00Z", 60)

	now := fixedNow("2026-05-12T00:02:00Z")
	got, err := FindExpired(root, now)
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d; want 2 (both outbox + inbox)", len(got))
	}
	// Ensure both agents are represented.
	agents := map[string]bool{}
	for _, e := range got {
		agents[e.Agent] = true
	}
	if !agents["agent-a"] || !agents["agent-b"] {
		t.Errorf("agents = %v; want {agent-a, agent-b}", agents)
	}
}

func TestFindExpired_SkipsNonExpired(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "live/outbox/agent-a/4-ddd444.gdl",
		"4-ddd444", "2026-05-12T00:00:00Z", 3600) // 1h ttl

	now := fixedNow("2026-05-12T00:10:00Z") // 10 min later — not expired
	got, err := FindExpired(root, now)
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d; want 0", len(got))
	}
}

func TestFindExpired_SkipsTTLZero(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "live/outbox/agent-a/5-eee555.gdl",
		"5-eee555", "2026-05-12T00:00:00Z", 0)

	now := fixedNow("2030-01-01T00:00:00Z") // years later
	got, err := FindExpired(root, now)
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d; want 0 (ttl=0 never expires)", len(got))
	}
}

func TestFindExpired_SkipsNonThoughtFiles(t *testing.T) {
	root := t.TempDir()

	// File with @channel-message instead of @thought.
	channelDir := filepath.Join(root, "live", "outbox", "agent-a")
	if err := os.MkdirAll(channelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	chanLine := "@channel-message|id:m-1|from:agent-a|to:agent-b|content:hi|ts:2026-05-12T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(channelDir, "m-1.gdl"), []byte(chanLine), 0o644); err != nil {
		t.Fatal(err)
	}

	// File with @route only (no @thought) — defensive.
	routeOnly := "@route|to:agent-a|from:agent-b|thought:9-zzz999|ts:2026-05-12T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(channelDir, "route-only.gdl"), []byte(routeOnly), 0o644); err != nil {
		t.Fatal(err)
	}

	now := fixedNow("2030-01-01T00:00:00Z")
	got, err := FindExpired(root, now)
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d; want 0 (no @thought records)", len(got))
	}
}

// --- Move ------------------------------------------------------------------

func TestMove_OutboxToExpired_PreservesContent(t *testing.T) {
	root := t.TempDir()
	id := "10-aaa000"
	seedThought(t, root, "live/outbox/agent-a/"+id+".gdl", id,
		"2026-05-12T00:00:00Z", 60)
	src := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read src: %v", err)
	}

	if err := Move(root, ExpiredFile{SourcePath: src, Agent: "agent-a", ID: id}); err != nil {
		t.Fatalf("Move: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still exists after Move: err=%v", err)
	}
	dst := filepath.Join(root, "live", "expired", "agent-a", id+".gdl")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("content mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestMove_InboxToExpired_PreservesRouteRecord(t *testing.T) {
	root := t.TempDir()
	id := "11-bbb111"
	dir := filepath.Join(root, "live", "inbox", "agent-b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Inbox files carry both @thought + @route (per routing package).
	contents := "@thought|id:" + id + "|author:agent-a|type:hypothesis|subject:order:1|content:c|scope:fleet|ts:2026-05-12T00:00:00Z|ttl:60\n" +
		"@route|to:agent-b|from:agent-a|thought:" + id + "|ts:2026-05-12T00:00:00Z\n"
	src := filepath.Join(dir, id+".gdl")
	if err := os.WriteFile(src, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Move(root, ExpiredFile{SourcePath: src, Agent: "agent-b", ID: id}); err != nil {
		t.Fatalf("Move: %v", err)
	}

	dst := filepath.Join(root, "live", "expired", "agent-b", id+".gdl")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != contents {
		t.Errorf("content mismatch:\n got %q\nwant %q", got, contents)
	}
}

func TestMove_AlreadyExpiredError_OnDestExists(t *testing.T) {
	root := t.TempDir()
	id := "12-ccc222"
	seedThought(t, root, "live/outbox/agent-a/"+id+".gdl", id,
		"2026-05-12T00:00:00Z", 60)
	src := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")

	// Pre-seed destination.
	dstDir := filepath.Join(root, "live", "expired", "agent-a")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dstDir, id+".gdl")
	if err := os.WriteFile(dst, []byte("preexisting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Move(root, ExpiredFile{SourcePath: src, Agent: "agent-a", ID: id})
	if err == nil {
		t.Fatalf("Move: want error; got nil")
	}
	var aee *rufioerr.AlreadyExpiredError
	if !errors.As(err, &aee) {
		t.Fatalf("err type = %T (%v); want *AlreadyExpiredError", err, err)
	}
	if aee.Agent != "agent-a" || aee.ID != id {
		t.Errorf("AlreadyExpiredError = %+v; want {Agent:agent-a, ID:%s}", aee, id)
	}

	// Source must still exist (no data loss).
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source removed after AlreadyExpiredError: %v", statErr)
	}
	// Destination must be unchanged.
	got, _ := os.ReadFile(dst)
	if string(got) != "preexisting\n" {
		t.Errorf("destination overwritten: got %q", got)
	}
}

// --- Sweep -----------------------------------------------------------------

func TestSweep_MovesOnlyExpired(t *testing.T) {
	root := t.TempDir()
	// 2 expired, 1 not-expired.
	seedThought(t, root, "live/outbox/agent-a/exp-1.gdl", "exp-1",
		"2026-05-12T00:00:00Z", 30)
	seedThought(t, root, "live/outbox/agent-a/exp-2.gdl", "exp-2",
		"2026-05-12T00:00:00Z", 30)
	seedThought(t, root, "live/outbox/agent-a/live-1.gdl", "live-1",
		"2026-05-12T00:00:00Z", 3600)

	now := fixedNow("2026-05-12T00:01:00Z") // ttl=30 expired; ttl=3600 not
	moved, err := Sweep(root, now, io.Discard)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if moved != 2 {
		t.Errorf("moved = %d; want 2", moved)
	}
	// Non-expired stays in outbox.
	if _, err := os.Stat(filepath.Join(root, "live", "outbox", "agent-a", "live-1.gdl")); err != nil {
		t.Errorf("live-1 missing from outbox after sweep: %v", err)
	}
	// Expired files in expired/.
	for _, id := range []string{"exp-1", "exp-2"} {
		dst := filepath.Join(root, "live", "expired", "agent-a", id+".gdl")
		if _, err := os.Stat(dst); err != nil {
			t.Errorf("%s missing from expired/: %v", id, err)
		}
		src := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Errorf("%s still in outbox: err=%v", id, err)
		}
	}
}

func TestSweep_Idempotent_SecondCallNoop(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "live/outbox/agent-a/i-1.gdl", "i-1",
		"2026-05-12T00:00:00Z", 30)
	seedThought(t, root, "live/outbox/agent-a/i-2.gdl", "i-2",
		"2026-05-12T00:00:00Z", 30)

	now := fixedNow("2026-05-12T00:01:00Z")
	first, err := Sweep(root, now, io.Discard)
	if err != nil {
		t.Fatalf("Sweep first: %v", err)
	}
	if first != 2 {
		t.Errorf("first moved = %d; want 2", first)
	}

	second, err := Sweep(root, now, io.Discard)
	if err != nil {
		t.Fatalf("Sweep second: %v", err)
	}
	if second != 0 {
		t.Errorf("second moved = %d; want 0 (idempotent)", second)
	}
}

func TestSweep_PerFileError_LogsAndContinues(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "live/outbox/agent-a/p-1.gdl", "p-1",
		"2026-05-12T00:00:00Z", 30)
	seedThought(t, root, "live/outbox/agent-a/p-2.gdl", "p-2",
		"2026-05-12T00:00:00Z", 30)

	// Pre-seed dest for p-1 → Move(p-1) returns AlreadyExpiredError; p-2 still moves.
	dstDir := filepath.Join(root, "live", "expired", "agent-a")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "p-1.gdl"), []byte("pre\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := fixedNow("2026-05-12T00:01:00Z")
	var logBuf bytes.Buffer
	moved, err := Sweep(root, now, &logBuf)
	if err != nil {
		t.Fatalf("Sweep: %v (per-file errors must not abort the sweep)", err)
	}
	if moved != 1 {
		t.Errorf("moved = %d; want 1 (p-2 only; p-1 already-expired)", moved)
	}
	// p-2 actually moved.
	if _, err := os.Stat(filepath.Join(dstDir, "p-2.gdl")); err != nil {
		t.Errorf("p-2 not in expired/: %v", err)
	}
	// p-1 source still present (no data loss).
	if _, err := os.Stat(filepath.Join(root, "live", "outbox", "agent-a", "p-1.gdl")); err != nil {
		t.Errorf("p-1 source disappeared on AlreadyExpiredError: %v", err)
	}
	// Log writer received the per-file error message (proves injection +
	// continuation: the buffer captures the failure for p-1, and moved=1
	// proves p-2 was still attempted after p-1 failed).
	if got := logBuf.String(); !strings.Contains(got, "ttlsweep: move agent-a/p-1") {
		t.Errorf("logBuf = %q; want substring %q", got, "ttlsweep: move agent-a/p-1")
	}
}

// --- TickInterval constant -------------------------------------------------

func TestTickInterval_IsTenSeconds(t *testing.T) {
	// D14.1: daemon ticks every 10s. Guard against accidental edits.
	if TickInterval != 10*time.Second {
		t.Errorf("TickInterval = %v; want 10s (D14.1)", TickInterval)
	}
}
