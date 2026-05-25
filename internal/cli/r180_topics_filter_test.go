// Package cli — #180 tests for the `rufio recall --topics=<csv>` filter.
//
// Cross-vendor convergent friction from the 2026-05-21 live 4-vendor
// cross-harness session: write verbs (attend/think/observe) accept
// `--topics=<csv>` to tag records, but the read verb (recall) had no
// matching filter. Three independent agents (Claude, Cursor, Codex)
// hit the same STOP on their first encounter with the substrate.
//
// These tests pin the contract end-to-end through runRecall, mirroring
// the existing r31_polish_test.go pattern. RED-first.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// TestRecall_TopicsFilter_SingleTopic_FiltersCorrectly — recall
// --topics=foo MUST surface ONLY records carrying topic foo (ANY-match
// against a single value collapses to exact-token-in-set).
func TestRecall_TopicsFilter_SingleTopic_FiltersCorrectly(t *testing.T) {
	root := scopeTestProject(t, "alice")

	// Seed two thoughts with different topics.
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "A",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think 1: %v", err)
	}
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:2", Content: "B",
		Topics: "topic-b", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think 2: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types:  "thought",
			Topics: "topic-a",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	if !strings.Contains(out, "test:1") {
		t.Errorf("recall --topics=topic-a missing test:1 row; out:\n%s", out)
	}
	if strings.Contains(out, "test:2") {
		t.Errorf("recall --topics=topic-a leaked test:2 (tagged topic-b); out:\n%s", out)
	}
}

// TestRecall_TopicsFilter_MultiTopic_ANYMatch — recall --topics=A,B
// MUST surface records tagged with EITHER A or B (set-intersection).
func TestRecall_TopicsFilter_MultiTopic_ANYMatch(t *testing.T) {
	root := scopeTestProject(t, "alice")

	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "A",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:2", Content: "B",
		Topics: "topic-b", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:3", Content: "C",
		Topics: "topic-c", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed 3: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types:  "thought",
			Topics: "topic-a,topic-b",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	if !strings.Contains(out, "test:1") {
		t.Errorf("missing test:1 (topic-a); out:\n%s", out)
	}
	if !strings.Contains(out, "test:2") {
		t.Errorf("missing test:2 (topic-b); out:\n%s", out)
	}
	if strings.Contains(out, "test:3") {
		t.Errorf("leaked test:3 (topic-c); out:\n%s", out)
	}
}

// TestRecall_TopicsFilter_RecordWithoutTopics_Excluded — records that
// were written WITHOUT --topics= (i.e. no topics: field on disk) MUST
// be EXCLUDED when --topics= is set. No implicit "all topics" match.
func TestRecall_TopicsFilter_RecordWithoutTopics_Excluded(t *testing.T) {
	root := scopeTestProject(t, "alice")

	// Seed: one with topics, one without.
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "with topics",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed with-topics: %v", err)
	}
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:2", Content: "no topics",
		Scope: "fleet",
		// Topics intentionally omitted — on disk this elides the
		// topics: field entirely (see thought.BuildThoughtRecord).
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed no-topics: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types:  "thought",
			Topics: "topic-a",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	if !strings.Contains(out, "test:1") {
		t.Errorf("missing test:1 (tagged); out:\n%s", out)
	}
	if strings.Contains(out, "test:2") {
		t.Errorf("test:2 (no topics:) leaked under --topics=topic-a; out:\n%s", out)
	}
}

// TestRecall_TopicsFilter_CombinesWithTypes — --types=thought
// --topics=topic-a is AND-combined: a record must satisfy BOTH the type
// AND the topic filter.
func TestRecall_TopicsFilter_CombinesWithTypes(t *testing.T) {
	root := scopeTestProject(t, "alice")

	// Seed a thought tagged topic-a.
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "customer:5821", Content: "thought tagged",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think: %v", err)
	}
	// Seed an observation tagged topic-a.
	if err := runObserve(root, observeArgs{
		Subject: "customer:5821", Predicate: "uses", Object: "policy:refund-2",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed observe: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types:  "thought",
			Topics: "topic-a",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	// MUST surface the thought.
	if !strings.Contains(out, "thought") {
		t.Errorf("missing thought row under --types=thought --topics=topic-a; out:\n%s", out)
	}
	// MUST NOT surface the observation (excluded by --types=thought).
	if strings.Contains(out, "policy:refund-2") {
		t.Errorf("--types=thought leaked observation row; out:\n%s", out)
	}
}

// TestRecall_TopicsFilter_RespectsPrivacyFloor — a scope:agent record
// authored by ANOTHER agent that happens to be tagged with the filtered
// topic MUST stay hidden from a non-author caller. The #147 privacy
// floor is independent of the topic filter.
func TestRecall_TopicsFilter_RespectsPrivacyFloor(t *testing.T) {
	root := scopeTestProject(t, "alice")

	// Hand-write a scope:agent thought authored by agent-b under the
	// outbox layout — bypassing runThink so we don't have to swap
	// identities mid-test.
	bobDir := filepath.Join(root, "live", "outbox", "agent-b")
	if err := os.MkdirAll(bobDir, 0o755); err != nil {
		t.Fatalf("mkdir bob outbox: %v", err)
	}
	bobRec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1779000000000-bob001"},
		{Key: "author", Value: "agent-b"},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: "private:thing"},
		{Key: "content", Value: "bob private"},
		{Key: "scope", Value: "agent"},
		{Key: "topics", Value: "topic-a"},
		{Key: "ts", Value: versioning.NowISO()},
		{Key: "ttl", Value: "0"},
	}}
	bobPath := filepath.Join(bobDir, "1779000000000-bob001.gdl")
	if err := os.WriteFile(bobPath, []byte(gdl.RenderLine(bobRec)+"\n"), 0o644); err != nil {
		t.Fatalf("write bob thought: %v", err)
	}
	// Seed alice's own topic-a tagged thought so we have a positive hit.
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "alice:thing", Content: "alice public",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed alice think: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types:  "thought",
			Topics: "topic-a",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	if !strings.Contains(out, "alice:thing") {
		t.Errorf("missing alice's own topic-a row; out:\n%s", out)
	}
	if strings.Contains(out, "private:thing") {
		t.Errorf("bob's scope:agent record leaked via --topics filter (privacy floor breached); out:\n%s", out)
	}
}

// TestRecall_NoTopicsFilter_ReturnsAllAsBefore — regression guard:
// behavior MUST be byte-identical to pre-#180 when --topics= is omitted.
// Records both with AND without on-disk topics: field surface as before.
func TestRecall_NoTopicsFilter_ReturnsAllAsBefore(t *testing.T) {
	root := scopeTestProject(t, "alice")

	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "with topics",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed with-topics: %v", err)
	}
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:2", Content: "no topics",
		Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed no-topics: %v", err)
	}

	out := captureStdout(t, func() {
		// Topics intentionally empty — must behave as pre-#180.
		if err := runRecall(root, recallArgs{
			Types: "thought",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	if !strings.Contains(out, "test:1") {
		t.Errorf("missing test:1; out:\n%s", out)
	}
	if !strings.Contains(out, "test:2") {
		t.Errorf("missing test:2 (regression: no-topics record dropped without --topics filter); out:\n%s", out)
	}
}

// TestRecall_TopicsFilter_EmptyValue_NoOp — `--topics=` with no value
// (empty CSV) MUST behave as if the flag was not passed (matches the
// splitCSVTrim contract: "" → nil → no filter). It must NOT mean
// "match records with empty topics" (which would be a confusing
// inversion of the write verbs' --topics= contract).
func TestRecall_TopicsFilter_EmptyValue_NoOp(t *testing.T) {
	root := scopeTestProject(t, "alice")

	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "with topics",
		Topics: "topic-a", Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:2", Content: "no topics",
		Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types:  "thought",
			Topics: "",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	if !strings.Contains(out, "test:1") || !strings.Contains(out, "test:2") {
		t.Errorf("--topics= (empty) must be a no-op; out:\n%s", out)
	}
}
