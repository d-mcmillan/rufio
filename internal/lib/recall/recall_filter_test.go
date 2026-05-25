package recall

import (
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

func recs(items ...RecallRecord) []RecallRecord { return items }

func TestFilter_NoFilters_PassesAll(t *testing.T) {
	all := recs(
		RecallRecord{Type: "thought", TS: "2026-05-12T00:00:00Z", Scope: "fleet"},
		RecallRecord{Type: "observation", TS: "2026-05-12T00:00:00Z", Scope: "agent"},
	)
	got := Filter(all, FilterParams{})
	if len(got) != 2 {
		t.Errorf("len=%d want 2", len(got))
	}
}

func TestFilter_Types_ExcludesNonMatching(t *testing.T) {
	all := recs(
		RecallRecord{Type: "thought"},
		RecallRecord{Type: "observation"},
		RecallRecord{Type: "reason"},
	)
	got := Filter(all, FilterParams{Types: []string{"thought", "reason"}})
	if len(got) != 2 {
		t.Errorf("len=%d want 2", len(got))
	}
	for _, r := range got {
		if r.Type == "observation" {
			t.Errorf("observation should have been filtered out")
		}
	}
}

func TestFilter_Scope_GivenAlwaysPasses(t *testing.T) {
	all := recs(
		RecallRecord{Type: "given", Scope: ""},
		RecallRecord{Type: "thought", Scope: "fleet"},
		RecallRecord{Type: "thought", Scope: "agent", Author: "agent-b"},
	)
	got := Filter(all, FilterParams{Scope: "agent", CurrentAgent: "agent-a"})
	// given passes; agent-b's agent-scoped thought is excluded;
	// fleet-scoped thought... what's the rule? Scope=agent means
	// "filter to records visible at agent scope" — i.e., only records
	// AUTHORED by the current agent at agent scope, plus all higher-scope
	// records? Or strictly records WITH Scope="agent" AND Author=me?
	// Per design line 153: "--scope=agent records visible only to author"
	// So Scope=agent filter means: records with Scope="agent" AND
	// Author=CurrentAgent. Records with broader scope (deployment, fleet)
	// should also pass since they're visible at the agent level.
	// Simplest mental model: filter only EXCLUDES records that are
	// scoped MORE TIGHTLY than the filter. So --scope=agent excludes
	// agent-scoped records authored by OTHERS, includes everything else.
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (given + fleet thought), got=%v", len(got), got)
	}
}

func TestFilter_Scope_AgentOnlyKeepsOwnAndBroader(t *testing.T) {
	all := recs(
		RecallRecord{Type: "thought", Scope: "agent", Author: "agent-a"},
		RecallRecord{Type: "thought", Scope: "agent", Author: "agent-b"},
		RecallRecord{Type: "thought", Scope: "fleet", Author: "agent-b"},
	)
	got := Filter(all, FilterParams{Scope: "agent", CurrentAgent: "agent-a"})
	// Expected:
	// - agent-a's agent-scoped: pass
	// - agent-b's agent-scoped: excluded
	// - agent-b's fleet-scoped: pass (visible to all)
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (own agent + fleet), got=%v", len(got), got)
	}
	for _, r := range got {
		if r.Scope == "agent" && r.Author != "agent-a" {
			t.Errorf("foreign agent-scoped record leaked: %+v", r)
		}
	}
}

func TestFilter_Since_ExcludesOlderThanWindow(t *testing.T) {
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	all := recs(
		RecallRecord{Type: "thought", TS: "2026-05-12T11:50:00Z"}, // 10m ago — passes 1h window
		RecallRecord{Type: "thought", TS: "2026-05-12T10:50:00Z"}, // 70m ago — excluded
	)
	got := Filter(all, FilterParams{Since: time.Hour, Now: now})
	if len(got) != 1 {
		t.Errorf("len=%d want 1", len(got))
	}
}

func TestFilter_AsOf_ExcludesNewer(t *testing.T) {
	asof := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	all := recs(
		RecallRecord{Type: "thought", TS: "2026-05-12T11:00:00Z"}, // pre-asof, pass
		RecallRecord{Type: "thought", TS: "2026-05-12T13:00:00Z"}, // post-asof, exclude
	)
	got := Filter(all, FilterParams{AsOf: asof})
	if len(got) != 1 {
		t.Errorf("len=%d want 1", len(got))
	}
}

func TestFilter_IncludeExpiredFalse_HidesRetractedRecords(t *testing.T) {
	all := recs(
		RecallRecord{Type: "thought", TS: "2026-05-12T12:00:00Z", Retracted: false},
		RecallRecord{Type: "thought", TS: "2026-05-12T12:00:00Z", Retracted: true},
	)
	got := Filter(all, FilterParams{IncludeExpired: false})
	if len(got) != 1 {
		t.Errorf("len=%d want 1 (retracted should be hidden)", len(got))
	}
	if got[0].Retracted {
		t.Errorf("hidden record was the wrong one")
	}
}

func TestFilter_IncludeExpiredTrue_KeepsRetractedRecords(t *testing.T) {
	all := recs(
		RecallRecord{Type: "thought", TS: "2026-05-12T12:00:00Z", Retracted: false},
		RecallRecord{Type: "thought", TS: "2026-05-12T12:00:00Z", Retracted: true},
	)
	got := Filter(all, FilterParams{IncludeExpired: true})
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (both should be visible)", len(got))
	}
}

// --- Topics filter (#180) ---
//
// Cross-vendor convergent friction from the 2026-05-21 live 4-vendor
// cross-harness session: write verbs (attend/think/observe) accept
// `--topics=<csv>` but the read verb (recall) did not, breaking the
// verb-symmetry contract. These tests pin the new --topics= filter
// directly on the Filter() predicate (the CLI mirrors it).
//
// Semantics:
//   - nil / empty Topics → no filter (regression guard).
//   - non-empty Topics → ANY-match: a record passes iff its on-disk
//     topics: field (parsed as CSV) intersects p.Topics.
//   - records without a topics: field are EXCLUDED when Topics is set
//     (no implicit "all topics" match for unlabeled records).
//   - composes with other filters via AND (Types AND Topics AND …).
//   - the on-disk topics encoding mirrors the write side: comma-joined,
//     populated by the gdl parser into a string field. The filter
//     surface uses RecallRecord.Topics (a []string) which Scan
//     populates from the parsed `topics` field.

func TestFilter_Topics_NoFilter_PassesAll(t *testing.T) {
	// Regression guard: when p.Topics is empty (default), behavior must
	// be byte-identical to pre-#180.
	all := recs(
		RecallRecord{Type: "thought", Topics: []string{"alpha"}},
		RecallRecord{Type: "thought", Topics: nil},
		RecallRecord{Type: "thought", Topics: []string{"beta", "gamma"}},
	)
	got := Filter(all, FilterParams{})
	if len(got) != 3 {
		t.Errorf("len=%d want 3 (no topics filter = pass all)", len(got))
	}
}

func TestFilter_Topics_SingleTopic_FiltersCorrectly(t *testing.T) {
	all := recs(
		RecallRecord{Type: "thought", Topics: []string{"alpha"}},
		RecallRecord{Type: "thought", Topics: []string{"beta"}},
		RecallRecord{Type: "thought", Topics: []string{"alpha", "gamma"}},
	)
	got := Filter(all, FilterParams{Topics: []string{"alpha"}})
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (alpha matches both alpha and alpha,gamma)", len(got))
	}
	for _, r := range got {
		hit := false
		for _, tp := range r.Topics {
			if tp == "alpha" {
				hit = true
			}
		}
		if !hit {
			t.Errorf("non-alpha record leaked: %+v", r)
		}
	}
}

func TestFilter_Topics_MultiTopic_ANYMatch(t *testing.T) {
	// --topics=A,B means: pass records whose topics set intersects {A,B}.
	all := recs(
		RecallRecord{Type: "thought", Topics: []string{"alpha"}},         // pass (A)
		RecallRecord{Type: "thought", Topics: []string{"beta"}},          // pass (B)
		RecallRecord{Type: "thought", Topics: []string{"gamma"}},         // exclude
		RecallRecord{Type: "thought", Topics: []string{"alpha", "beta"}}, // pass (both A and B)
	)
	got := Filter(all, FilterParams{Topics: []string{"alpha", "beta"}})
	if len(got) != 3 {
		t.Errorf("len=%d want 3 (alpha OR beta), got=%v", len(got), got)
	}
	for _, r := range got {
		anyMatch := false
		for _, tp := range r.Topics {
			if tp == "alpha" || tp == "beta" {
				anyMatch = true
			}
		}
		if !anyMatch {
			t.Errorf("record without alpha or beta leaked: %+v", r)
		}
	}
}

func TestFilter_Topics_RecordWithoutTopics_Excluded(t *testing.T) {
	// When --topics= is SET, records that carry no topics: field at all
	// are EXCLUDED. No implicit "all topics" match for unlabeled records.
	all := recs(
		RecallRecord{Type: "thought", Topics: []string{"alpha"}}, // pass
		RecallRecord{Type: "thought", Topics: nil},               // exclude (no topics)
		RecallRecord{Type: "thought", Topics: []string{}},        // exclude (empty topics)
		RecallRecord{Type: "given"},                              // exclude (given/ has no topics: field by design)
	)
	got := Filter(all, FilterParams{Topics: []string{"alpha"}})
	if len(got) != 1 {
		t.Errorf("len=%d want 1 (only the alpha-tagged record), got=%v", len(got), got)
	}
}

func TestFilter_Topics_CombinesWithTypes(t *testing.T) {
	// --types=thought --topics=alpha is AND-combined: must be BOTH a
	// thought AND alpha-tagged.
	all := recs(
		RecallRecord{Type: "thought", Topics: []string{"alpha"}},         // pass
		RecallRecord{Type: "thought", Topics: []string{"beta"}},          // exclude (wrong topic)
		RecallRecord{Type: "observation", Topics: []string{"alpha"}},     // exclude (wrong type)
		RecallRecord{Type: "thought", Topics: []string{"alpha", "beta"}}, // pass
	)
	got := Filter(all, FilterParams{
		Types:  []string{"thought"},
		Topics: []string{"alpha"},
	})
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (thought AND alpha), got=%v", len(got), got)
	}
	for _, r := range got {
		if r.Type != "thought" {
			t.Errorf("non-thought leaked through --types=thought: %+v", r)
		}
	}
}

func TestFilter_Topics_EmptyValue_NoOp(t *testing.T) {
	// p.Topics being an empty slice (not nil) must still be treated as
	// "no filter" — matches the CSV-parse behavior of `--topics=`
	// (empty raw string → nil slice; the filter must tolerate both).
	all := recs(
		RecallRecord{Type: "thought", Topics: nil},
		RecallRecord{Type: "thought", Topics: []string{"alpha"}},
	)
	got := Filter(all, FilterParams{Topics: []string{}})
	if len(got) != 2 {
		t.Errorf("len=%d want 2 (empty topics filter = no-op), got=%v", len(got), got)
	}
}

// --- Match ---

func TestMatch_EmptyQueryPassesAll(t *testing.T) {
	all := recs(
		RecallRecord{Content: "foo"},
		RecallRecord{Content: "bar"},
	)
	got := Match(all, "")
	if len(got) != 2 {
		t.Errorf("len=%d", len(got))
	}
}

func TestMatch_SubstringAcrossFields(t *testing.T) {
	all := recs(
		RecallRecord{Type: "thought", Subject: "customer:5821", Content: "churn signals"},
		RecallRecord{Type: "observation", Subject: "customer:9999", Predicate: "has-status", Object: "active"},
		RecallRecord{Type: "thought", Subject: "order:1", Content: "different content"},
	)
	got := Match(all, "churn")
	if len(got) != 1 {
		t.Errorf("len=%d want 1", len(got))
	}
	if got[0].Subject != "customer:5821" {
		t.Errorf("subject=%q", got[0].Subject)
	}
}

func TestMatch_CaseInsensitive(t *testing.T) {
	all := recs(RecallRecord{Content: "CHURN signals"})
	got := Match(all, "churn")
	if len(got) != 1 {
		t.Errorf("case-insensitive failed")
	}
}

func TestMatch_MultiWord_ANDSemantics(t *testing.T) {
	all := recs(
		RecallRecord{Content: "churn signals showing"},
		RecallRecord{Content: "churn only no signals here"},
		RecallRecord{Content: "signals here, but no churn", Subject: "customer:9999"},
		RecallRecord{Content: "unrelated"},
	)
	got := Match(all, "churn signals")
	// All three should match — each contains both "churn" AND "signals" somewhere.
	if len(got) != 3 {
		t.Errorf("len=%d want 3 (all records with both 'churn' AND 'signals'), got=%v", len(got), got)
	}
}

func TestMatch_EntityIDForm_ExactSubjectMatch(t *testing.T) {
	all := recs(
		RecallRecord{Subject: "customer:5821", Content: "exact"},
		RecallRecord{Subject: "customer:5821:order:1", Content: "different subject"},
		RecallRecord{Subject: "order:9999", Content: "customer:5821 mentioned in content"},
	)
	// "customer:5821" matches the entity-id regex per thought.ValidateSubject.
	got := Match(all, "customer:5821")
	if len(got) != 1 {
		t.Errorf("len=%d want 1 (exact subject match), got=%v", len(got), got)
	}
	if got[0].Subject != "customer:5821" {
		t.Errorf("subject=%q want customer:5821", got[0].Subject)
	}
}

// silence — thought import used only via ValidateSubject inside Match
var _ = thought.ValidateSubject
