// live_substrate_test.go — PR-G1: tests for the live substrate
// loader + the OPEN-2 quorum-threshold render-boundary resolution.
//
// Determinism: every test writes synthetic on-disk records via the REAL
// lib writers (thought.Write / confirm.Append) under t.TempDir() and
// reads them back through the loader — NO wall-clock, NO fsnotify, NO
// time.Now(). The loader is a pure function of `root` (the disk state)
// + an injected `now`, exactly like project.go's pure layer.
package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/lib/autopromote"
	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// liveNow is the deterministic clock the loader is given (mirrors
// project_test.go's fixedNow).
var liveNow = time.Date(2026, 5, 15, 14, 10, 0, 0, time.UTC)

// writeOutboxThought writes a @thought into live/outbox/<author>/<id>.gdl
// via the real thought.Write so the on-disk shape is the lib's exactly
// (the same path stream.EmitCatchUp walks).
func writeOutboxThought(t *testing.T, root, id, author, typ, subject, content, ts string) {
	t.Helper()
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: author, Type: typ, Subject: subject,
		Content: content, Scope: "fleet", TS: ts, TTL: 0,
	})
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write: %v", err)
	}
}

// TestLoadSubstrate_OrderedFeedAndProjection proves the loader produces
// the v8 chat rows from the on-disk broadcast log: the events are read
// via the stream lib (EmitCatchUp, NOT a hand-rolled walk), ordered
// chronologically by the thought-id unix-millis prefix (NOT
// WalkDir/lexical order), projected via the shared projectThread (NOT
// reimplemented), and OPEN-2 is applied at this boundary.
func TestLoadSubstrate_OrderedFeedAndProjection(t *testing.T) {
	root := t.TempDir()
	const op = "operator"

	// Write OUT OF chronological order on disk to prove the loader
	// re-orders by the id unix-millis prefix (filepath.WalkDir yields
	// lexical/dir order, which would otherwise scramble the thread).
	writeOutboxThought(t, root, "1747000002000-d29", "claude-code", "decision",
		"customer:5821", "decision: offer downgrade, not churn-save discount",
		"2026-05-15T14:02:46Z")
	writeOutboxThought(t, root, "1747000000000-op0", op, "focus",
		"customer:5821", "investigate customer:5821 churn risk — rufio fleet",
		"2026-05-15T14:02:09Z")
	writeOutboxThought(t, root, "1747000001000-h01", "claude-code", "hypothesis",
		"customer:5821", "14-day silence, customer mentioned cancel — churn signals",
		"2026-05-15T14:02:11Z")

	// Two confirms on the decision (sorted+deduped by confirm.ReadAll).
	if err := confirm.Append(root, "1747000002000-d29",
		confirm.BuildConfirm("1747000002000-d29", "cursor", "", "2026-05-15T14:02:14Z")); err != nil {
		t.Fatal(err)
	}
	if err := confirm.Append(root, "1747000002000-d29",
		confirm.BuildConfirm("1747000002000-d29", "data-analyst", "", "2026-05-15T14:02:15Z")); err != nil {
		t.Fatal(err)
	}

	rows := loadSubstrate(root, op, liveNow)

	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3:\n%#v", len(rows), rows)
	}
	// Chronological order by id prefix: op (…000) → hypothesis (…001) →
	// decision (…002), NOT the disk/lexical write order.
	wantOrder := []struct {
		who, role, kind string
	}{
		{op, "focus", kindOp},
		{"claude-code", "hypothesis", kindPlan},
		{"claude-code", roleDecision, kindPlan},
	}
	for i, w := range wantOrder {
		if rows[i].Who != w.who || rows[i].Role != w.role || rows[i].Kind != w.kind {
			t.Fatalf("row %d = {%s,%s,%s}, want {%s,%s,%s}",
				i, rows[i].Who, rows[i].Role, rows[i].Kind, w.who, w.role, w.kind)
		}
	}
	// Last on the freshest (final) row.
	if !rows[2].Last {
		t.Errorf("freshest row missing Last=true")
	}
	// OPEN-2 (LOCKED 2026-05-16): the decision row's Quorum.Total is the
	// auto-promote constant (NEVER a literal 3); Yes is the sorted-deduped
	// confirm tally.
	q := rows[2].Quorum
	if q == nil {
		t.Fatalf("decision row missing Quorum")
	}
	if q.Total != autopromote.MinDistinctConfirmers {
		t.Errorf("OPEN-2: Quorum.Total = %d, want autopromote.MinDistinctConfirmers (%d)",
			q.Total, autopromote.MinDistinctConfirmers)
	}
	if !reflect.DeepEqual(q.Yes, []string{"cursor", "data-analyst"}) {
		t.Errorf("Quorum.Yes = %v, want [cursor data-analyst] (sorted-deduped tally)", q.Yes)
	}
}

// TestLoadSubstrate_Empty proves a fresh/empty substrate (no outbox at
// all) yields zero rows, not a panic — the "fresh/empty" cold-start
// signal the App uses to show the setup hint.
func TestLoadSubstrate_Empty(t *testing.T) {
	rows := loadSubstrate(t.TempDir(), "operator", liveNow)
	if len(rows) != 0 {
		t.Fatalf("empty substrate → %#v, want 0 rows", rows)
	}
}

// TestLoadSubstrate_HistoryNoConfirms proves a decision with NO confirms
// on disk renders (history-on-disk, daemon offline) with a nil Quorum
// (no tally) — the row still appears; the console is a filesystem
// console, never gated on a confirm.
func TestLoadSubstrate_HistoryNoConfirms(t *testing.T) {
	root := t.TempDir()
	writeOutboxThought(t, root, "1700000000000-d01", "claude-code", "decision",
		"x:1", "a decision with no confirms yet", "2026-05-15T14:00:00Z")
	rows := loadSubstrate(root, "operator", liveNow)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Quorum != nil {
		t.Errorf("decision with no confirms → Quorum=%#v, want nil", rows[0].Quorum)
	}
}

// TestApplyQuorumThreshold_OnlyRowsWithAQuorum is the focused OPEN-2
// unit, updated for #131 (2026-05-18): the threshold is applied to
// EVERY row that carries a Quorum — NOT decision-only — and is the SAME
// denominator regardless of thought type (the auto-promote engine is
// type-agnostic). Rows with NO Quorum (an unconfirmed thought of any
// type) are untouched: the `Quorum == nil` guard preserves the
// no-`0/3`-clutter property. This pins both the broadening (a confirmed
// NON-decision row gets the threshold) and the retained nil guard.
func TestApplyQuorumThreshold_OnlyRowsWithAQuorum(t *testing.T) {
	rows := []ThreadMsg{
		{Who: "operator", Role: "focus", Kind: kindOp}, // unconfirmed, no Quorum
		{Who: "claude-code", Role: "hypothesis", Kind: kindPlan,
			Quorum: &Quorum{Yes: []string{"data-analyst"}, Total: 0}}, // #131: confirmed NON-decision
		{Who: "claude-code", Role: roleDecision, Kind: kindPlan,
			Quorum: &Quorum{Yes: []string{"cursor"}, Total: 0}}, // confirmed decision (regression)
		{Who: "cursor", Role: roleDecision, Kind: kindPlan}, // decision, no tally → no Quorum
	}
	applyQuorumThreshold(rows)

	// #131: the confirmed NON-decision row gets the SAME threshold.
	if rows[1].Quorum == nil || rows[1].Quorum.Total != autopromote.MinDistinctConfirmers {
		t.Errorf("confirmed non-decision row Quorum.Total = %#v, want Total %d",
			rows[1].Quorum, autopromote.MinDistinctConfirmers)
	}
	// Regression: the confirmed decision row is unchanged.
	if rows[2].Quorum.Total != autopromote.MinDistinctConfirmers {
		t.Errorf("decision row Quorum.Total = %d, want %d",
			rows[2].Quorum.Total, autopromote.MinDistinctConfirmers)
	}
	// Retained nil guard: rows without a Quorum stay untouched (no 0/3).
	if rows[0].Quorum != nil || rows[3].Quorum != nil {
		t.Errorf("threshold leaked onto a non-quorum row: %#v", rows)
	}
}

// TestLoadSubstrate_PrefixlessIDStableLast guards the ordering tie-break:
// ids without a unix-millis prefix sort by Path so the feed stays
// deterministic (no flaky last-row caret).
func TestLoadSubstrate_PrefixlessIDStableLast(t *testing.T) {
	root := t.TempDir()
	writeOutboxThought(t, root, "zzz-no-prefix", "claude-code", "hypothesis",
		"x:1", "weird id", "2026-05-15T14:00:00Z")
	writeOutboxThought(t, root, "1700000000000-h01", "claude-code", "hypothesis",
		"x:1", "normal id", "2026-05-15T14:00:01Z")
	a := loadSubstrate(root, "operator", liveNow)
	b := loadSubstrate(root, "operator", liveNow)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("loadSubstrate not deterministic:\n a=%#v\n b=%#v", a, b)
	}
	// The prefixless id has stamp 0 → sorts FIRST; the freshest (Last)
	// row is the prefixed one.
	if len(a) != 2 || !a[len(a)-1].Last {
		t.Fatalf("expected 2 rows with Last on the final, got %#v", a)
	}
}

// TestLoadSubstrate_QuorumOnConfirmedNonDecision is the #131 contract:
// the quorum-dot projection follows the auto-promote ENGINE (which is
// type-agnostic — autopromote.MinDistinctConfirmers distinct confirmers
// on ANY thought), NOT the obsolete decision-only projection scoping. A
// confirmed NON-decision thought (here a `hypothesis`) with ≥1 distinct
// confirmer projects a Quorum exactly as a decision does: Yes =
// sorted-deduped confirmers, Total = autopromote.MinDistinctConfirmers,
// and the rendered row shows the dot row (asserted via the SAME render
// path the existing decision-row test uses — renderRow). RED at base
// 153fc6b: the decision-only gates (live_substrate.go:216/256,
// project.go:230) reject the non-decision id so Quorum stays nil.
func TestLoadSubstrate_QuorumOnConfirmedNonDecision(t *testing.T) {
	root := t.TempDir()
	const op = "operator"

	// A confirmed NON-decision thought: a hypothesis with two distinct
	// confirmers (written out of order + duplicated to prove the
	// sorted-deduped guarantee survives the broadened gate).
	writeOutboxThought(t, root, "1747000001000-h01", "claude-code", "hypothesis",
		"customer:5821", "14-day silence, customer mentioned cancel — churn signals",
		"2026-05-15T14:02:11Z")
	for _, c := range []struct{ who, ts string }{
		{"data-analyst", "2026-05-15T14:02:13Z"},
		{"cursor", "2026-05-15T14:02:14Z"},
		{"cursor", "2026-05-15T14:02:15Z"}, // dup → deduped by confirm.ReadAll
	} {
		if err := confirm.Append(root, "1747000001000-h01",
			confirm.BuildConfirm("1747000001000-h01", c.who, "", c.ts)); err != nil {
			t.Fatal(err)
		}
	}

	rows := loadSubstrate(root, op, liveNow)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1:\n%#v", len(rows), rows)
	}
	r := rows[0]
	if r.Role != "hypothesis" {
		t.Fatalf("row Role = %q, want hypothesis (the non-decision under test)", r.Role)
	}
	q := r.Quorum
	if q == nil {
		t.Fatalf("#131: a confirmed non-decision thought must carry a Quorum, got nil (decision-only gate still rejecting it)")
	}
	if q.Total != autopromote.MinDistinctConfirmers {
		t.Errorf("Quorum.Total = %d, want autopromote.MinDistinctConfirmers (%d) — same denominator as a decision",
			q.Total, autopromote.MinDistinctConfirmers)
	}
	if !reflect.DeepEqual(q.Yes, []string{"cursor", "data-analyst"}) {
		t.Errorf("Quorum.Yes = %v, want [cursor data-analyst] (sorted-deduped tally)", q.Yes)
	}
	// The dots actually reach the screen end-to-end: assert via the SAME
	// render path the existing decision-row test uses (renderRow →
	// renderQuorumDots, the nil-gated chat.go:496 path — no role gate).
	styles.SetProfile(termenv.Ascii)
	out := renderRow(r, "", 120)
	if !strings.Contains(out, dotVoted) {
		t.Errorf("confirmed non-decision row missing quorum %q dots end-to-end: %q", dotVoted, out)
	}
	if !strings.Contains(out, "2/3") {
		t.Errorf("confirmed non-decision row missing 2/3 counter (AutoPromote threshold): %q", out)
	}
}

// TestLoadSubstrate_NonDecisionNoConfirmsNoQuorum is the #131 ≥1-confirm
// guard: broadening the projection to ANY confirmed thought must NOT
// introduce `0/3` clutter. A non-decision thought with ZERO confirms on
// disk projects NO Quorum (nil) and renders NO dot row — exactly the
// no-clutter property the existing decision path has (the
// loadConfirmTallies len(Confirms)==0 guard, retained verbatim).
func TestLoadSubstrate_NonDecisionNoConfirmsNoQuorum(t *testing.T) {
	root := t.TempDir()
	writeOutboxThought(t, root, "1747000001000-h02", "claude-code", "hypothesis",
		"customer:5821", "a hypothesis nobody has confirmed yet",
		"2026-05-15T14:02:11Z")

	rows := loadSubstrate(root, "operator", liveNow)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Quorum != nil {
		t.Errorf("non-decision with NO confirms → Quorum=%#v, want nil (≥1-confirm guard; no 0/3 clutter)", rows[0].Quorum)
	}
	styles.SetProfile(termenv.Ascii)
	out := renderRow(rows[0], "", 120)
	if strings.Contains(out, dotVoted) || strings.Contains(out, dotUnvoted) || strings.Contains(out, "0/3") {
		t.Errorf("unconfirmed non-decision row must render NO dot row, got: %q", out)
	}
}

// TestLoadSubstrate_DecisionRegressionUnchanged is the #131 REGRESSION
// guard: decisions are now a strict SUPERSET of confirmed thoughts, so a
// confirmed decision must project byte-identically to before the
// broadening (Yes sorted-deduped, Total == the constant, dots render).
// This is the same shape TestLoadSubstrate_OrderedFeedAndProjection
// asserted for the decision row, isolated so the regression is explicit.
func TestLoadSubstrate_DecisionRegressionUnchanged(t *testing.T) {
	root := t.TempDir()
	writeOutboxThought(t, root, "1747000002000-d29", "claude-code", "decision",
		"customer:5821", "decision: offer downgrade, not churn-save discount",
		"2026-05-15T14:02:46Z")
	if err := confirm.Append(root, "1747000002000-d29",
		confirm.BuildConfirm("1747000002000-d29", "cursor", "", "2026-05-15T14:02:14Z")); err != nil {
		t.Fatal(err)
	}
	if err := confirm.Append(root, "1747000002000-d29",
		confirm.BuildConfirm("1747000002000-d29", "data-analyst", "", "2026-05-15T14:02:15Z")); err != nil {
		t.Fatal(err)
	}

	rows := loadSubstrate(root, "operator", liveNow)
	if len(rows) != 1 || rows[0].Role != roleDecision {
		t.Fatalf("want 1 decision row, got %#v", rows)
	}
	q := rows[0].Quorum
	if q == nil {
		t.Fatalf("decision regression: Quorum must still be populated")
	}
	if q.Total != autopromote.MinDistinctConfirmers {
		t.Errorf("decision regression: Quorum.Total = %d, want %d (unchanged)",
			q.Total, autopromote.MinDistinctConfirmers)
	}
	if !reflect.DeepEqual(q.Yes, []string{"cursor", "data-analyst"}) {
		t.Errorf("decision regression: Quorum.Yes = %v, want [cursor data-analyst] (unchanged)", q.Yes)
	}
	styles.SetProfile(termenv.Ascii)
	out := renderRow(rows[0], "", 120)
	if !strings.Contains(out, dotVoted) || !strings.Contains(out, "2/3") {
		t.Errorf("decision regression: dot row must still render: %q", out)
	}
}
