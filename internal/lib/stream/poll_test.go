package stream

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// encodeNoDelim base64-encodes payload WITHOUT the ts\x00path delimiter, to
// exercise Poll's "valid base64 but structurally invalid cursor" path.
func encodeNoDelim(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// seedInbox writes one @thought record per (ts) into live/inbox/agent-a so
// Poll has a deterministic, (ts,path)-orderable corpus. Each record lands in
// its own file (mirrors how routing drops one record per file into an inbox).
//
// Records use scope:fleet so the predicate's privacy gate (#139 followup)
// doesn't filter them — these tests exercise ordering/cursor mechanics, not
// scope semantics, and a fleet broadcast from `other` to agent-a is the
// realistic shape for a daemon-routed inbox-mirror record.
func seedInbox(t *testing.T, root string, recs []struct{ name, ts string }) {
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

func TestPoll_OrdersBoundsAndResumes(t *testing.T) {
	root := t.TempDir()
	// Deliberately out-of-filename-order timestamps so a correct impl must
	// sort by (ts,path), not by walk order.
	seedInbox(t, root, []struct{ name, ts string }{
		{"c.gdl", "2026-05-12T00:00:03Z"},
		{"a.gdl", "2026-05-12T00:00:01Z"},
		{"b.gdl", "2026-05-12T00:00:02Z"},
		{"d.gdl", "2026-05-12T00:00:04Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	// Page 1: bounded to 2, ordered by (ts,path).
	page1, next1, err := Poll(root, dirs, fp, "", 2)
	if err != nil {
		t.Fatalf("Poll page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}
	if page1[0].TS != "2026-05-12T00:00:01Z" || page1[1].TS != "2026-05-12T00:00:02Z" {
		t.Fatalf("page1 not (ts,path)-ordered: %q %q", page1[0].TS, page1[1].TS)
	}
	if next1 == "" {
		t.Fatal("next cursor after page1 must be non-empty")
	}

	// Page 2: resume from next1, no overlap with page1.
	page2, next2, err := Poll(root, dirs, fp, next1, 100)
	if err != nil {
		t.Fatalf("Poll page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len = %d, want 2 (the remaining records)", len(page2))
	}
	if page2[0].TS != "2026-05-12T00:00:03Z" || page2[1].TS != "2026-05-12T00:00:04Z" {
		t.Fatalf("page2 not ordered/contiguous: %q %q", page2[0].TS, page2[1].TS)
	}
	// No record from page1 reappears in page2.
	seen := map[string]bool{}
	for _, e := range page1 {
		seen[e.Path] = true
	}
	for _, e := range page2 {
		if seen[e.Path] {
			t.Fatalf("page2 overlaps page1 at %q", e.Path)
		}
	}

	// Idempotent re-poll: nothing new → empty events AND the SAME cursor.
	empty, next3, err := Poll(root, dirs, fp, next2, 100)
	if err != nil {
		t.Fatalf("Poll re-poll: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("idempotent re-poll returned %d events, want 0", len(empty))
	}
	if next3 != next2 {
		t.Fatalf("idempotent re-poll changed cursor: %q -> %q", next2, next3)
	}
}

func TestPoll_TiebreaksByPathWithinSameTS(t *testing.T) {
	root := t.TempDir()
	// Three records sharing one timestamp — order MUST fall back to path.
	seedInbox(t, root, []struct{ name, ts string }{
		{"z.gdl", "2026-05-12T00:00:00Z"},
		{"m.gdl", "2026-05-12T00:00:00Z"},
		{"a.gdl", "2026-05-12T00:00:00Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	evs, _, err := Poll(root, dirs, fp, "", 100)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("len = %d, want 3", len(evs))
	}
	want := []string{
		"live/inbox/agent-a/a.gdl",
		"live/inbox/agent-a/m.gdl",
		"live/inbox/agent-a/z.gdl",
	}
	for i, w := range want {
		if evs[i].Path != w {
			t.Fatalf("evs[%d].Path = %q, want %q (same-ts tiebreak by path)", i, evs[i].Path, w)
		}
	}
}

func TestPoll_MalformedCursor(t *testing.T) {
	root := t.TempDir()
	seedInbox(t, root, []struct{ name, ts string }{{"a.gdl", "2026-05-12T00:00:01Z"}})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	// Not valid base64.
	if _, _, err := Poll(root, dirs, fp, "!!!not-base64!!!", 100); err == nil {
		t.Fatal("expected error for non-base64 cursor")
	} else if err.Error() != "invalid cursor" {
		t.Fatalf("malformed-cursor error = %q, want %q", err.Error(), "invalid cursor")
	}

	// Valid base64 but missing the \x00 ts/path delimiter.
	bad := encodeNoDelim("just-ts-no-nul")
	if _, _, err := Poll(root, dirs, fp, bad, 100); err == nil {
		t.Fatal("expected error for delimiter-less cursor")
	} else if err.Error() != "invalid cursor" {
		t.Fatalf("delimiter-less cursor error = %q, want %q", err.Error(), "invalid cursor")
	}
}

// TestPoll_VariableFractionTSChronologicalAcrossPages is the C1 regression.
// versioning.NowISO() emits RFC3339Nano, which trims trailing-zero fraction
// digits → variable-width fractions. A LEXICAL compare of those mis-orders
// same-second events ("…01.1Z" sorts after "…01.15Z"); Poll would then return
// the later one, advance the cursor past it, and the strictly-after filter
// would permanently skip "…01.1Z" across the page boundary — silent loss
// baked into the opaque cursor. This test pages max:1 across that boundary
// and asserts every same-second variable-fraction record is returned exactly
// once in chronological order. It FAILS on a lexical (ts,path) order.
func TestPoll_VariableFractionTSChronologicalAcrossPages(t *testing.T) {
	root := t.TempDir()
	// All within the same second; fractions in RFC3339Nano trimmed form.
	// Chronological order: 01 < 01.05 < 01.1 < 01.15 < 01.2. Filenames are
	// deliberately NOT in that order so neither walk order nor a lexical
	// (ts,path) key accidentally yields the right sequence.
	seedInbox(t, root, []struct{ name, ts string }{
		{"f5.gdl", "2026-05-12T00:00:01.2Z"},
		{"f1.gdl", "2026-05-12T00:00:01Z"},
		{"f4.gdl", "2026-05-12T00:00:01.15Z"},
		{"f2.gdl", "2026-05-12T00:00:01.05Z"},
		{"f3.gdl", "2026-05-12T00:00:01.1Z"},
	})
	fp := FilterParams{CurrentAgent: "agent-a"}
	dirs := []string{"live/inbox/agent-a"}

	wantChrono := []string{
		"2026-05-12T00:00:01Z",
		"2026-05-12T00:00:01.05Z",
		"2026-05-12T00:00:01.1Z",
		"2026-05-12T00:00:01.15Z",
		"2026-05-12T00:00:01.2Z",
	}

	// Page one-at-a-time across the boundary, following the opaque cursor.
	var got []string
	cursor := ""
	for i := 0; i < len(wantChrono)+2; i++ { // +2 guards against an infinite loop
		evs, next, err := Poll(root, dirs, fp, cursor, 1)
		if err != nil {
			t.Fatalf("Poll page %d: %v", i, err)
		}
		if len(evs) == 0 {
			break
		}
		if len(evs) != 1 {
			t.Fatalf("max:1 returned %d events", len(evs))
		}
		got = append(got, evs[0].TS)
		if next == cursor {
			t.Fatalf("cursor failed to advance past %q (page %d)", evs[0].TS, i)
		}
		cursor = next
	}

	if len(got) != len(wantChrono) {
		t.Fatalf("got %d events across pages, want %d (some were silently skipped): got=%v",
			len(got), len(wantChrono), got)
	}
	for i, w := range wantChrono {
		if got[i] != w {
			t.Fatalf("page %d ts = %q, want %q (not chronological): full=%v", i, got[i], w, got)
		}
	}
}

// TestPoll_NULInTSRecordIsSkippedNotMissplit is the I1 regression. Event.TS
// is an unvalidated GDL field; a record whose ts contains a literal NUL must
// be treated as unparseable (skipped) rather than mis-splitting the cursor.
// The well-formed sibling must still be returned.
func TestPoll_NULInTSRecordIsSkippedNotMissplit(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "live", "inbox", "agent-a")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	// Hostile: a NUL embedded in the ts value. scope:fleet keeps the
	// record visible to agent-a's Poll (the privacy gate added in #139
	// excludes other-author scope:agent records — orthogonal to this test).
	bad := "@thought|ts:2026-05-12T00:00:01\x00Z|author:other|subject:agent::agent-a|content:hi|scope:fleet\n"
	if err := os.WriteFile(filepath.Join(inbox, "bad.gdl"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	good := "@thought|ts:2026-05-12T00:00:02Z|author:other|subject:agent::agent-a|content:ok|scope:fleet\n"
	if err := os.WriteFile(filepath.Join(inbox, "good.gdl"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}

	evs, _, err := Poll(root, []string{"live/inbox/agent-a"}, FilterParams{CurrentAgent: "agent-a"}, "", 100)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected the NUL-ts record skipped and only the good one returned, got %d: %+v", len(evs), evs)
	}
	if evs[0].TS != "2026-05-12T00:00:02Z" {
		t.Fatalf("returned event = %q, want the well-formed sibling", evs[0].TS)
	}
}

// TestDecodeCursor_LastNULSplitKeepsPathIntact exercises the LastIndexByte
// (not IndexByte) split directly. encodeCursor only ever joins one
// NUL-free canonical ts with a NUL-free path, but Poll's defence-in-depth
// (skip any event whose canonical ts or path contains a NUL) means a
// 2-NUL payload should never be produced by encodeCursor. Should one ever
// reach decodeCursor anyway, splitting on the LAST NUL keeps the trailing
// path component intact (everything before the final NUL becomes the ts
// half) — a hostile NUL smuggled earlier cannot shift the path boundary.
// This closes the otherwise-untested LastIndexByte branch.
func TestDecodeCursor_LastNULSplitKeepsPathIntact(t *testing.T) {
	// payload = "<ts>\x00<smuggled>\x00<path>" — two NULs.
	ts := "2026-05-12T00:00:01.000000000Z"
	smuggled := "live/inbox/agent-a/decoy.gdl"
	path := "live/inbox/agent-a/real.gdl"
	c := base64.RawURLEncoding.EncodeToString([]byte(ts + "\x00" + smuggled + "\x00" + path))

	gotTS, gotPath, err := decodeCursor(c)
	if err != nil {
		t.Fatalf("decodeCursor(2-NUL payload) errored: %v", err)
	}
	// LastIndexByte splits on the FINAL NUL: path stays the true trailing
	// component; the ts half absorbs everything before it.
	if gotPath != path {
		t.Fatalf("path half = %q, want %q (LastIndexByte must keep the trailing path intact)", gotPath, path)
	}
	if gotTS != ts+"\x00"+smuggled {
		t.Fatalf("ts half = %q, want %q (everything before the last NUL)", gotTS, ts+"\x00"+smuggled)
	}
}

func TestPoll_MissingDirIsEmptyNotError(t *testing.T) {
	root := t.TempDir() // no live/inbox/agent-a at all
	evs, next, err := Poll(root, []string{"live/inbox/agent-a"}, FilterParams{CurrentAgent: "agent-a"}, "", 100)
	if err != nil {
		t.Fatalf("Poll over a missing inbox dir must not error, got: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected 0 events for missing dir, got %d", len(evs))
	}
	if next != "" {
		t.Fatalf("expected empty cursor for empty result, got %q", next)
	}
}
