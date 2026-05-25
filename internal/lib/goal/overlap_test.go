package goal

import (
	"reflect"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// ---- ExtractEntities --------------------------------------------------------

func TestExtractEntities_HappyPath(t *testing.T) {
	got := ExtractEntities("fix customer:5821 churn")
	want := []string{"customer:5821"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractEntities: got %v, want %v", got, want)
	}
}

func TestExtractEntities_MultipleDistinct(t *testing.T) {
	got := ExtractEntities("coordinate customer:5821 and customer:5822 rollout")
	want := []string{"customer:5821", "customer:5822"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractEntities: got %v, want %v", got, want)
	}
}

func TestExtractEntities_Duplicates(t *testing.T) {
	got := ExtractEntities("check customer:5821 then customer:5821 again")
	want := []string{"customer:5821"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractEntities: got %v, want %v (should dedup)", got, want)
	}
}

func TestExtractEntities_NoMatches(t *testing.T) {
	got := ExtractEntities("just plain text with no entity ids")
	if got != nil {
		t.Errorf("ExtractEntities: got %v, want nil", got)
	}
}

func TestExtractEntities_Empty(t *testing.T) {
	if got := ExtractEntities(""); got != nil {
		t.Errorf("ExtractEntities(\"\"): got %v, want nil", got)
	}
}

func TestExtractEntities_MultiSegment(t *testing.T) {
	got := ExtractEntities("fix org:cloud-platform:database failover")
	want := []string{"org:cloud-platform:database"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractEntities: got %v, want %v", got, want)
	}
}

func TestExtractEntities_FirstOccurrenceOrder(t *testing.T) {
	got := ExtractEntities("customer:5821 then vendor:acme")
	want := []string{"customer:5821", "vendor:acme"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractEntities: got %v, want %v (order matters)", got, want)
	}
}

// ---- FindOverlaps -----------------------------------------------------------

func TestFindOverlaps_NoExisting_ReturnsEmpty(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "fix customer:5821 churn",
		State:     StateActive,
	}
	got := FindOverlaps(newGoal, nil)
	if got != nil {
		t.Errorf("FindOverlaps with nil existing: got %v, want nil", got)
	}
}

func TestFindOverlaps_SelfSuppressed_SameAuthor(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "fix customer:5821 churn",
		State:     StateActive,
	}
	existing := []Goal{
		{
			ID:        "2-bbbbbb",
			Author:    "agent-a", // same author
			Statement: "address customer:5821 dashboard",
			State:     StateActive,
		},
	}
	got := FindOverlaps(newGoal, existing)
	if len(got) != 0 {
		t.Errorf("FindOverlaps with same-author existing: got %v, want empty (D18.2 self-suppression)", got)
	}
}

func TestFindOverlaps_NonActiveSkipped(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "fix customer:5821 churn",
		State:     StateActive,
	}
	existing := []Goal{
		{
			ID:        "2-bbbbbb",
			Author:    "agent-b",
			Statement: "address customer:5821 dashboard",
			State:     StateCompleted, // terminal — must be skipped
		},
		{
			ID:        "3-cccccc",
			Author:    "agent-c",
			Statement: "address customer:5821 retention",
			State:     StateAbandoned, // terminal — must be skipped
		},
	}
	got := FindOverlaps(newGoal, existing)
	if len(got) != 0 {
		t.Errorf("FindOverlaps with non-active existing: got %v, want empty (D18.3)", got)
	}
}

func TestFindOverlaps_SameID_Skipped(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "fix customer:5821 churn",
		State:     StateActive,
	}
	existing := []Goal{
		{
			ID:        "1-aaaaaa", // same ID as newGoal — defensive skip
			Author:    "agent-b",
			Statement: "address customer:5821 dashboard",
			State:     StateActive,
		},
	}
	got := FindOverlaps(newGoal, existing)
	if len(got) != 0 {
		t.Errorf("FindOverlaps with same-ID existing: got %v, want empty (defensive)", got)
	}
}

func TestFindOverlaps_HappyPath(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "fix customer:5821 churn",
		State:     StateActive,
	}
	peer := Goal{
		ID:        "2-bbbbbb",
		Author:    "agent-b",
		Statement: "build customer:5821 dashboard",
		State:     StateActive,
	}
	got := FindOverlaps(newGoal, []Goal{peer})
	if len(got) != 1 {
		t.Fatalf("FindOverlaps happy path: got %d pairs, want 1 (%v)", len(got), got)
	}
	if got[0].Entity != "customer:5821" {
		t.Errorf("Entity: got %q, want %q", got[0].Entity, "customer:5821")
	}
	if got[0].PeerGoal.ID != peer.ID {
		t.Errorf("PeerGoal.ID: got %q, want %q", got[0].PeerGoal.ID, peer.ID)
	}
	if got[0].PeerGoal.Author != "agent-b" {
		t.Errorf("PeerGoal.Author: got %q, want %q", got[0].PeerGoal.Author, "agent-b")
	}
}

func TestFindOverlaps_MultiEntity_TwoPairs(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "coordinate customer:5821 with vendor:acme contract",
		State:     StateActive,
	}
	peer := Goal{
		ID:        "2-bbbbbb",
		Author:    "agent-b",
		Statement: "renew vendor:acme deal and stabilise customer:5821",
		State:     StateActive,
	}
	got := FindOverlaps(newGoal, []Goal{peer})
	if len(got) != 2 {
		t.Fatalf("FindOverlaps multi-entity: got %d pairs, want 2 (%v)", len(got), got)
	}
	// Order follows new-goal entity order: customer:5821 first, then vendor:acme.
	if got[0].Entity != "customer:5821" {
		t.Errorf("got[0].Entity: %q, want %q", got[0].Entity, "customer:5821")
	}
	if got[1].Entity != "vendor:acme" {
		t.Errorf("got[1].Entity: %q, want %q", got[1].Entity, "vendor:acme")
	}
	for i, p := range got {
		if p.PeerGoal.ID != peer.ID {
			t.Errorf("got[%d].PeerGoal.ID: %q, want %q", i, p.PeerGoal.ID, peer.ID)
		}
	}
}

func TestFindOverlaps_MultiEntity_PartialIntersection(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "coordinate customer:5821 with vendor:acme contract",
		State:     StateActive,
	}
	peer := Goal{
		ID:        "2-bbbbbb",
		Author:    "agent-b",
		Statement: "customer:5821 retention plan tied to product:foo launch",
		State:     StateActive,
	}
	got := FindOverlaps(newGoal, []Goal{peer})
	if len(got) != 1 {
		t.Fatalf("FindOverlaps partial intersection: got %d pairs, want 1 (%v)", len(got), got)
	}
	if got[0].Entity != "customer:5821" {
		t.Errorf("Entity: got %q, want %q", got[0].Entity, "customer:5821")
	}
}

func TestFindOverlaps_MultiAgent(t *testing.T) {
	newGoal := Goal{
		ID:        "1-aaaaaa",
		Author:    "agent-a",
		Statement: "investigate customer:5821 spike",
		State:     StateActive,
	}
	peerB := Goal{
		ID:        "2-bbbbbb",
		Author:    "agent-b",
		Statement: "build customer:5821 dashboard",
		State:     StateActive,
	}
	peerC := Goal{
		ID:        "3-cccccc",
		Author:    "agent-c",
		Statement: "support customer:5821 onboarding",
		State:     StateActive,
	}
	got := FindOverlaps(newGoal, []Goal{peerB, peerC})
	if len(got) != 2 {
		t.Fatalf("FindOverlaps multi-agent: got %d pairs, want 2 (%v)", len(got), got)
	}
	authors := map[string]bool{}
	for _, p := range got {
		if p.Entity != "customer:5821" {
			t.Errorf("Entity: got %q, want %q", p.Entity, "customer:5821")
		}
		authors[p.PeerGoal.Author] = true
	}
	if !authors["agent-b"] || !authors["agent-c"] {
		t.Errorf("expected peers from both agent-b and agent-c, got %v", authors)
	}
}

// ---- BuildOverlapRecord -----------------------------------------------------

func TestBuildOverlapRecord_FieldOrder(t *testing.T) {
	rec := BuildOverlapRecord("agent-b", "agent-a", "customer:5821", "src-id", "tgt-id", "2026-05-12T00:00:00Z")
	if rec.Type != "goal-overlap" {
		t.Errorf("Type=%q, want goal-overlap", rec.Type)
	}
	// Field order is locked at to, from, entity, target-goal, source-goal, ts (D18.4).
	wantKeys := []string{"to", "from", "entity", "target-goal", "source-goal", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	// Spot check rendered line matches expected escaping. `:` in values is
	// backslash-escaped by gdl.EscapeValue, so customer:5821 becomes
	// customer\:5821 and the ts timestamp likewise.
	line := gdl.RenderLine(rec)
	want := `@goal-overlap|to:agent-b|from:agent-a|entity:customer\:5821|target-goal:tgt-id|source-goal:src-id|ts:2026-05-12T00\:00\:00Z`
	if line != want {
		t.Errorf("RenderLine:\n got=%q\nwant=%q", line, want)
	}
}
