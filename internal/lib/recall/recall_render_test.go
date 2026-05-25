package recall

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

func TestRenderColumnar_EmptyInput_EmptyOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderColumnar(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("buf=%q want empty", buf.String())
	}
}

func TestRenderColumnar_ThoughtRecord(t *testing.T) {
	// H1c: text mode uses relative time + short ids (full ids opt-in via
	// RUFIO_FULL_IDS=1). Pin the full-ids env so the test can assert the
	// canonical id literally without time-sensitivity in the relative
	// timestamp column.
	t.Setenv("RUFIO_FULL_IDS", "1")
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1747000000000-ab12cd", TS: "2026-05-12T12:00:00Z", Author: "agent-a",
		Subject: "customer:5821", Content: "churn signals", Scope: "fleet",
	}
	if err := RenderColumnar(&buf, []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Text mode does NOT include the full RFC3339Nano (H1b). The other
	// values still surface in the unified row.
	for _, want := range []string{"thought", "agent-a", "customer:5821", "fleet", "churn signals"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Dogfood-surfaced gap: confirm/refute/--parent all need the
	// thought-id, but plain recall never surfaced it. H1c renders it as
	// a positional column (TAB-separated) instead of the old
	// `id=<id>` labelled form — operators no longer have to grep for a
	// label, the column position itself is the contract.
	if !strings.Contains(out, "1747000000000-ab12cd") {
		t.Errorf("plain output must surface the thought-id positionally:\n%s", out)
	}
}

func TestRenderColumnar_NonThoughtKindsHaveNoIDColumn(t *testing.T) {
	// Only thoughts carry an id that confirm/refute/--parent consume.
	// observation/reason/given have no actionable per-record id — we must
	// NOT fabricate an id column for them.
	var buf bytes.Buffer
	recs := []RecallRecord{
		{Type: "observation", TS: "2026-05-12T12:00:00Z", Author: "a", Subject: "customer:1", Predicate: "is", Object: "x"},
		{Type: "reason", TS: "2026-05-12T12:01:00Z", Author: "a", Content: "because"},
		{Type: "given", TS: "2026-05-12T12:02:00Z", Author: "a", Subject: "given/policy.md"},
	}
	if err := RenderColumnar(&buf, recs); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "id=") {
		t.Errorf("non-thought kinds must not get an id= field:\n%s", buf.String())
	}
}

func TestRenderColumnar_ObservationFormat(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", TS: "2026-05-12T12:00:00Z", Author: "a",
		Subject: "customer:5821", Predicate: "prefers", Object: "email", Scope: "fleet",
	}
	if err := RenderColumnar(&buf, []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	// Per spec line 231: subject:predicate="object" form
	if !strings.Contains(buf.String(), `customer:5821:prefers="email"`) {
		t.Errorf("missing key-data:\n%s", buf.String())
	}
}

// TestRenderColumnar_PromotedObservation_SurfacesProvenance is the #76
// recall gate (plain): a crowd-confirmed observation must render so a
// human/agent sees it WAS crowd-confirmed and BY WHOM + from which source
// thought. Provenance that cannot be recalled is still erased.
func TestRenderColumnar_PromotedObservation_SurfacesProvenance(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", TS: "2026-05-12T12:00:00Z", Author: "auto-promote",
		Subject: "customer:5821", Predicate: "asserted", Object: "prefers email",
		Scope:  "deployment",
		Origin: "agent-a", ConfirmedBy: []string{"agent-b", "agent-c", "agent-d"},
		Source: "1727000000-aaaaaa",
	}
	if err := RenderColumnar(&buf, []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"origin:agent-a",
		"confirmed-by:agent-b,agent-c,agent-d",
		"source:1727000000-aaaaaa",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing provenance %q:\n%s", want, out)
		}
	}
}

// TestRenderColumnar_NonPromotedObservation_ByteIdentical guards that a
// plain (non-promoted) observation row carries no spurious provenance
// suffix. The line shape itself moved in H1c (TAB-separated, unified
// columns, relative time, short ids) — provenance absence is what this
// test pins, not the exact pre-H1c byte sequence.
func TestRenderColumnar_NonPromotedObservation_ByteIdentical(t *testing.T) {
	t.Setenv("RUFIO_FULL_IDS", "1")
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", ID: "obs-123456", TS: "2026-05-12T12:00:00Z", Author: "a",
		Subject: "customer:5821", Predicate: "prefers", Object: "email", Scope: "fleet",
	}
	if err := RenderColumnar(&buf, []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// MUST NOT include any provenance segment (the · separator + a key).
	for _, banned := range []string{"origin:", "confirmed-by:", "source:", "·"} {
		if strings.Contains(out, banned) {
			t.Errorf("non-promoted observation must carry no provenance suffix; saw %q in:\n%s", banned, out)
		}
	}
	// And the canonical type label is bare `observation` (no
	// subtype-collapsing artefact).
	if !strings.Contains(out, "\tobservation\t") {
		t.Errorf("observation row missing the canonical `observation` type column:\n%s", out)
	}
}

func TestRenderJSON_PromotedObservation_ExposesProvenance(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", TS: "t", Author: "auto-promote",
		Subject: "customer:5821", Predicate: "asserted", Object: "x",
		Origin: "agent-a", ConfirmedBy: []string{"agent-b", "agent-c", "agent-d"},
		Source: "1727000000-aaaaaa",
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["origin"] != "agent-a" {
		t.Errorf("origin=%v want agent-a", got["origin"])
	}
	cb, ok := got["confirmed_by"].([]interface{})
	if !ok || len(cb) != 3 || cb[0] != "agent-b" || cb[2] != "agent-d" {
		t.Errorf("confirmed_by=%v want [agent-b agent-c agent-d]", got["confirmed_by"])
	}
	if got["source"] != "1727000000-aaaaaa" {
		t.Errorf("source=%v want 1727000000-aaaaaa", got["source"])
	}
}

// TestRenderJSON_NonPromotedObservation_StableEmptyProvenance keeps the
// stable-shape contract: provenance keys ALWAYS present (consumers rely on
// stable keys) but empty for non-promoted records.
func TestRenderJSON_NonPromotedObservation_StableEmptyProvenance(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", TS: "t", Author: "a", Subject: "x:1",
		Predicate: "is", Object: "y",
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if v, ok := got["origin"]; !ok || v != "" {
		t.Errorf("origin key must be present and empty, got %v ok=%v", v, ok)
	}
	if v, ok := got["source"]; !ok || v != "" {
		t.Errorf("source key must be present and empty, got %v ok=%v", v, ok)
	}
	cb, ok := got["confirmed_by"]
	if !ok {
		t.Errorf("confirmed_by key must always be present (stable shape)")
	}
	// Empty → JSON empty array (stable consumable shape), not null.
	if arr, isArr := cb.([]interface{}); !isArr || len(arr) != 0 {
		t.Errorf("confirmed_by must be an empty array for non-promoted, got %v", cb)
	}
}

func TestRenderJSON_EmptyInput_EmptyOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, "", nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("buf=%q want empty", buf.String())
	}
}

func TestRenderJSON_OneLinePerRecord(t *testing.T) {
	var buf bytes.Buffer
	recs := []RecallRecord{
		{Type: "thought", TS: "2026-05-12T12:00:00Z", Subject: "a:1", Content: "x"},
		{Type: "observation", TS: "2026-05-12T12:01:00Z", Subject: "b:2", Predicate: "is", Object: "y"},
	}
	if err := RenderJSON(&buf, "", recs); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, l := range lines {
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(l), &got); err != nil {
			t.Errorf("line %d not valid JSON: %v\n%q", i, err, l)
		}
	}
}

func TestRenderJSON_HasExpectedKeys(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1747000000000-ab12cd", TS: "2026-05-12T12:00:00Z", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	for _, k := range []string{"_type", "id", "ts", "author", "subject", "content", "scope"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if got["_type"] != "thought" {
		t.Errorf("_type=%v", got["_type"])
	}
	// Dogfood-surfaced gap: --json had NO top-level id; the id confirm/refute/
	// --parent need was only embedded in path. It must now be a first-class field.
	if got["id"] != "1747000000000-ab12cd" {
		t.Errorf("top-level id=%v want 1747000000000-ab12cd", got["id"])
	}
}

// TestRenderJSON_ThoughtTypeSurfacedConditionally is the #89 render gate:
// a record carrying ThoughtType must emit a top-level `type` key with that
// value; an empty ThoughtType must NOT sprout a spurious `type` key
// (omitempty semantics — non-thoughts/observation-type thoughts stay
// byte-identical).
func TestRenderJSON_ThoughtTypeSurfacedConditionally(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "approve", Scope: "fleet",
		ThoughtType: "decision",
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if got["type"] != "decision" {
		t.Errorf("type=%v want decision (#89: ThoughtType must surface as top-level `type`)", got["type"])
	}
	// _type (the namespace-kind) must remain unaffected.
	if got["_type"] != "thought" {
		t.Errorf("_type=%v want thought (must be untouched by #89)", got["_type"])
	}
}

func TestRenderJSON_NoThoughtTypeNoSpuriousKey(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", TS: "t", Author: "a", Subject: "x:1",
		Predicate: "is", Object: "y",
		// ThoughtType deliberately empty.
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if _, ok := got["type"]; ok {
		t.Errorf("empty ThoughtType must NOT emit a `type` key (omitempty semantics), got %v", got["type"])
	}
}

func TestRenderJSON_IDEmptyForNonThoughtKinds(t *testing.T) {
	// id is always present (stable shape) but empty for kinds with no
	// actionable id — we must not invent ids no verb can consume.
	var buf bytes.Buffer
	recs := []RecallRecord{
		{Type: "observation", TS: "t", Author: "a", Subject: "x:1", Predicate: "is", Object: "y"},
		{Type: "reason", TS: "t", Author: "a", Content: "because"},
	}
	if err := RenderJSON(&buf, "", recs); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		var got map[string]interface{}
		json.Unmarshal([]byte(l), &got)
		v, ok := got["id"]
		if !ok {
			t.Errorf("id key must always be present (stable shape):\n%s", l)
		}
		if v != "" {
			t.Errorf("non-thought id must be empty, got %v:\n%s", v, l)
		}
	}
}

// TestRenderJSON_TopicsSurfaced (v1.0.4 bug #1): the on-disk `topics:`
// field MUST round-trip through RenderJSON as a `topics` array. Pre-fix
// the key was dropped entirely — recall's --topics= filter worked
// server-side but the JSON output omitted the field, so any downstream
// (mirror snapshot, JSONL export, jq pipelines) reconstructed records
// without it. The mirror snapshot path was the load-bearing victim.
//
// Stable-shape contract: the key is ALWAYS present when the record
// has topics on disk, emitted as a JSON array of strings (never null).
// Records with no topics (Topics nil/empty) omit the key entirely
// (omitempty semantics) so non-tagged records stay byte-identical.
func TestRenderJSON_TopicsSurfaced(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
		Topics: []string{"alpha", "beta"},
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	topics, ok := got["topics"].([]interface{})
	if !ok {
		t.Fatalf("topics key missing or wrong type; got %#v (full=%v)", got["topics"], got)
	}
	if len(topics) != 2 || topics[0] != "alpha" || topics[1] != "beta" {
		t.Errorf("topics=%v want [alpha beta]", topics)
	}
}

// TestRenderJSON_NoTopicsOmitsKey pins the omitempty contract: records
// with no on-disk topics: field must NOT sprout a topics key (nil/[]
// would be ambiguous against "explicitly empty topics set"; the absent
// key is the canonical signal for "unlabeled").
func TestRenderJSON_NoTopicsOmitsKey(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
		// Topics deliberately nil.
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if _, present := got["topics"]; present {
		t.Errorf("untagged thought must NOT emit topics key (omitempty); got %v", got["topics"])
	}
}

// TestRenderJSON_TTLSurfaced (v1.0.4 bug #1): the on-disk `ttl:` field
// MUST round-trip through RenderJSON as a `ttl` integer. Symmetric with
// topics — recall's TTL-expiry filter uses RecallRecord.TTL internally
// but pre-fix the JSON output dropped the field, so mirror snapshots
// and JSONL pipelines lost it.
//
// Shape: integer seconds (D5.1 contract — 0 means "never expire"; matches
// how the on-disk field is rendered by thought.BuildThoughtRecord).
// Always present for @thought records (the only kind that writes a
// ttl: field today); omitted for non-thoughts to keep their shape
// byte-identical.
func TestRenderJSON_TTLSurfaced(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
		TTL: 600,
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	// JSON numbers decode as float64; compare via numeric coerce.
	ttl, ok := got["ttl"]
	if !ok {
		t.Fatalf("ttl key missing; got %v", got)
	}
	if v, _ := ttl.(float64); int(v) != 600 {
		t.Errorf("ttl=%v want 600", ttl)
	}
}

// TestRenderJSON_TTLZeroPresentForThoughts pins that ttl=0 (the "never
// expire" sentinel from D5.1) is rendered explicitly. Without this,
// consumers can't distinguish "field absent" from "infinite TTL" and
// the mirror snapshot's renderRecord can't reconstruct the on-disk
// `ttl:0` field (BuildThoughtRecord always writes it).
func TestRenderJSON_TTLZeroPresentForThoughts(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
		TTL: 0,
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	ttl, ok := got["ttl"]
	if !ok {
		t.Errorf("ttl key must be present for @thought even when 0; got %v", got)
	}
	if v, _ := ttl.(float64); int(v) != 0 {
		t.Errorf("ttl=%v want 0 (never-expire sentinel)", ttl)
	}
}

// TestRenderJSON_TTLOmittedForNonThoughts: observations, reasons,
// given/learned and the cognition-vocabulary kinds do not carry a
// ttl: field on disk, so the JSON shape must not invent one.
func TestRenderJSON_TTLOmittedForNonThoughts(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", TS: "t", Author: "a", Subject: "x:1",
		Predicate: "is", Object: "y",
		// No TTL field on disk.
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if _, present := got["ttl"]; present {
		t.Errorf("non-thought must NOT emit ttl key; got %v", got["ttl"])
	}
}

// TestRenderJSON_ObservationConfidenceSurfaced (v1.0.4 bug #1 expanded
// scope): observation records carry a `confidence:` field on disk
// (range [0,1] per observation.ParseConfidence). RenderJSON used to
// drop it entirely, so the mirror snapshot path could not reconstruct
// the observation's epistemic weight.
func TestRenderJSON_ObservationConfidenceSurfaced(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "observation", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Predicate: "is", Object: "y",
		Scope: "fleet", Confidence: 0.85,
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	conf, ok := got["confidence"]
	if !ok {
		t.Fatalf("confidence key missing; got %v", got)
	}
	if v, _ := conf.(float64); v != 0.85 {
		t.Errorf("confidence=%v want 0.85", conf)
	}
}

// TestRenderJSON_ConfidenceOmittedForNonObservation: thoughts/reasons/
// given/learned do not have a confidence: field on disk, so the JSON
// shape must not invent one.
func TestRenderJSON_ConfidenceOmittedForNonObservation(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if _, present := got["confidence"]; present {
		t.Errorf("non-observation must NOT emit confidence key; got %v", got["confidence"])
	}
}

// TestRenderJSON_ParentSurfaced (v1.0.4 bug #1 expanded scope): thoughts
// and reasons can carry a `parent:` field on disk pointing at another
// record. Pre-fix it was dropped from JSON, breaking the lineage chain
// through any JSON-mediated round-trip (mirror snapshot, JSONL export).
func TestRenderJSON_ParentSurfaced(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
		Parent: "0-parent-id",
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if got["parent"] != "0-parent-id" {
		t.Errorf("parent=%v want 0-parent-id", got["parent"])
	}
}

// TestRenderJSON_NoParentOmitsKey pins omitempty for parent.
func TestRenderJSON_NoParentOmitsKey(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "a",
		Subject: "x:1", Content: "c", Scope: "fleet",
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if _, present := got["parent"]; present {
		t.Errorf("records without parent must NOT emit parent key; got %v", got["parent"])
	}
}

// TestRenderJSON_ReasonDecisionFromExplicitField (v1.0.4 bug #1
// expanded scope): when a reason record has an explicit `decision:`
// field on disk, RenderJSON must surface it. Falls back to the
// path-derived id for legacy rows where RecallRecord.Decision is
// empty (existing decisionIDFromPath behavior preserved).
func TestRenderJSON_ReasonDecisionFromExplicitField(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "reason", ID: "1-a", TS: "t", Author: "a",
		Content: "step", Scope: "fleet",
		Decision: "1727000000-dec-explicit",
		// No Path set — proves the explicit field wins.
	}
	if err := RenderJSON(&buf, "", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	if got["decision"] != "1727000000-dec-explicit" {
		t.Errorf("decision=%v want 1727000000-dec-explicit (explicit field must win)", got["decision"])
	}
}

// TestEndToEnd_DiskToJSON_PreservesAllFields is the disk→scan→render
// end-to-end regression guard for v1.0.4 bug #1 (expanded scope). It
// writes one record per supported type to a real on-disk substrate
// with every optional field populated, then runs Scan + RenderJSON
// and asserts every disk field round-trips through to the JSON line.
//
// This catches the silent data-loss class the bug reporter flagged:
// a field can be on disk yet missed by EITHER the scanner (drops it
// into RecallRecord{} default) OR RenderJSON (omits the key). Both
// layers need updating in lockstep when a new disk field is added.
func TestEndToEnd_DiskToJSON_PreservesAllFields(t *testing.T) {
	root := t.TempDir()

	// 1) Thought with topics, ttl, parent.
	outboxDir := filepath.Join(root, "live", "outbox", "alice")
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	thoughtRec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-thought"}, {Key: "author", Value: "alice"},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: "test:1"},
		{Key: "content", Value: "exhaustive thought"},
		{Key: "scope", Value: "fleet"},
		{Key: "topics", Value: "alpha,beta"},
		{Key: "ts", Value: "2026-05-22T01:00:00.000000000Z"},
		{Key: "ttl", Value: "600"},
		{Key: "parent", Value: "0-parent"},
	}}
	if err := os.WriteFile(filepath.Join(outboxDir, "1-thought.gdl"), []byte(gdl.RenderLine(thoughtRec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2) Promoted observation with topics, confidence, provenance.
	learnedDir := filepath.Join(root, "learned")
	if err := os.MkdirAll(learnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	obsRec := gdl.Record{Type: "observation", Fields: []gdl.RecordField{
		{Key: "id", Value: "2-obs"}, {Key: "author", Value: "auto-promote"},
		{Key: "subject", Value: "test:2"},
		{Key: "predicate", Value: "has-status"},
		{Key: "object", Value: "active"},
		{Key: "scope", Value: "fleet"},
		{Key: "topics", Value: "color"},
		{Key: "confidence", Value: "0.85"},
		{Key: "ts", Value: "2026-05-22T01:00:01.000000000Z"},
		{Key: "origin", Value: "alice"},
		{Key: "confirmed-by", Value: "bob,carol"},
		{Key: "source", Value: "1-thought"},
	}}
	if err := os.WriteFile(filepath.Join(learnedDir, "2-obs.gdlm"), []byte(gdl.RenderLine(obsRec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3) Reason record with subject, scope, topics, parent, decision.
	reasonDir := filepath.Join(root, "live", "reasoning", "alice")
	if err := os.MkdirAll(reasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reasonRec := gdl.Record{Type: "reason", Fields: []gdl.RecordField{
		{Key: "id", Value: "3-reason"}, {Key: "author", Value: "alice"},
		{Key: "content", Value: "because P implies Q"},
		{Key: "scope", Value: "fleet"},
		{Key: "subject", Value: "test:3"},
		{Key: "topics", Value: "logic"},
		{Key: "parent", Value: "0-parent-reason"},
		{Key: "decision", Value: "1-decision"},
		{Key: "ts", Value: "2026-05-22T01:00:02.000000000Z"},
	}}
	if err := os.WriteFile(filepath.Join(reasonDir, "3-reason.gdl"), []byte(gdl.RenderLine(reasonRec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Scan everything.
	scanned, err := Scan(root, false)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(scanned) < 3 {
		t.Fatalf("expected >=3 records scanned; got %d", len(scanned))
	}

	// Index by id so we can assert each.
	byID := map[string]RecallRecord{}
	for _, r := range scanned {
		byID[r.ID] = r
	}

	// Render through RenderJSON and decode each line.
	var buf bytes.Buffer
	if err := RenderJSON(&buf, "", scanned); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var rendered []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode line: %v", err)
		}
		rendered = append(rendered, m)
	}
	jsonByID := map[string]map[string]interface{}{}
	for _, m := range rendered {
		if id, ok := m["id"].(string); ok {
			jsonByID[id] = m
		}
	}

	// Assert the thought row preserved topics/ttl/parent end-to-end.
	tj := jsonByID["1-thought"]
	if tj == nil {
		t.Fatalf("scanned/rendered thought missing; got=%v", jsonByID)
	}
	if topics, ok := tj["topics"].([]interface{}); !ok || len(topics) != 2 || topics[0] != "alpha" || topics[1] != "beta" {
		t.Errorf("thought.topics lost in round-trip; got %v", tj["topics"])
	}
	if v, _ := tj["ttl"].(float64); int(v) != 600 {
		t.Errorf("thought.ttl lost; got %v", tj["ttl"])
	}
	if tj["parent"] != "0-parent" {
		t.Errorf("thought.parent lost; got %v", tj["parent"])
	}

	// Assert the observation row preserved confidence end-to-end.
	oj := jsonByID["2-obs"]
	if oj == nil {
		t.Fatalf("scanned/rendered observation missing; got=%v", jsonByID)
	}
	if v, _ := oj["confidence"].(float64); v != 0.85 {
		t.Errorf("observation.confidence lost; got %v", oj["confidence"])
	}
	if topics, ok := oj["topics"].([]interface{}); !ok || len(topics) != 1 || topics[0] != "color" {
		t.Errorf("observation.topics lost; got %v", oj["topics"])
	}

	// Assert the reason row preserved subject/scope/topics/parent/decision.
	rj := jsonByID["3-reason"]
	if rj == nil {
		t.Fatalf("scanned/rendered reason missing; got=%v", jsonByID)
	}
	if rj["subject"] != "test:3" {
		t.Errorf("reason.subject lost; got %v", rj["subject"])
	}
	if rj["scope"] != "fleet" {
		t.Errorf("reason.scope lost; got %v", rj["scope"])
	}
	if topics, ok := rj["topics"].([]interface{}); !ok || len(topics) != 1 || topics[0] != "logic" {
		t.Errorf("reason.topics lost; got %v", rj["topics"])
	}
	if rj["parent"] != "0-parent-reason" {
		t.Errorf("reason.parent lost; got %v", rj["parent"])
	}
	if rj["decision"] != "1-decision" {
		t.Errorf("reason.decision lost; got %v", rj["decision"])
	}
}

// TestRelativisePath_StripsServerAbsoluteRoot (security audit H2 +
// Bonus): RelativisePath converts an absolute substrate path into the
// root-relative POSIX form emitted by JSON consumers. Operator's
// $HOME / substrate-root layout must never leak across the wire.
//
// Bonus signature change: takes the root as an explicit parameter so
// substrings of common dir names in the root path (e.g. /srv/live-prod)
// don't corrupt the slice. Pre-fix, an operator whose root contained
// "live" as a substring would have leaked a partial path like
// "live-prod/.rufio/live/outbox/..." — substring-search hit the wrong
// occurrence.
func TestRelativisePath_StripsServerAbsoluteRoot(t *testing.T) {
	cases := []struct {
		in   string
		root string
		want string
	}{
		{"/home/operator/projects/acme/live/outbox/alice/1-a.gdl", "/home/operator/projects/acme", "live/outbox/alice/1-a.gdl"},
		{"/var/lib/rufio/learned/cat/file.gdlm", "/var/lib/rufio", "learned/cat/file.gdlm"},
		{"/srv/rufio-prod/given/docs/spec.md", "/srv/rufio-prod", "given/docs/spec.md"},
		{"/tmp/test/live/reasoning/agent-a/1-a.gdl", "/tmp/test", "live/reasoning/agent-a/1-a.gdl"},
		// Already-relative passthrough.
		{"live/outbox/alice/1-a.gdl", "/anything", "live/outbox/alice/1-a.gdl"},
		// Empty passthrough.
		{"", "/anything", ""},
	}
	for _, c := range cases {
		got := RelativisePath(c.in, c.root)
		if got != c.want {
			t.Errorf("RelativisePath(%q, %q) = %q, want %q", c.in, c.root, got, c.want)
		}
	}
}

// TestRelativisePath_RootContainsLiveSubstring (Bonus): pre-fix, the
// substring-search variant would mis-slice paths when the operator's
// substrate root contained "live", "given", or "learned" as a
// directory name (e.g. /srv/live-prod). Threading root through means
// filepath.Rel resolves correctly regardless of root name.
func TestRelativisePath_RootContainsLiveSubstring(t *testing.T) {
	// Root contains "live" as a substring. Pre-fix this leaked
	// "live-prod/.rufio/live/outbox/..." because the substring search
	// hit the FIRST /live/ — the one inside the root path.
	got := RelativisePath("/srv/live-prod/live/outbox/alice/1.gdl", "/srv/live-prod")
	want := "live/outbox/alice/1.gdl"
	if got != want {
		t.Errorf("RelativisePath leaked root substring: got %q want %q", got, want)
	}
	// Same shape with "given" and "learned".
	got2 := RelativisePath("/home/given-ops/projects/x/learned/y.gdlm", "/home/given-ops/projects/x")
	want2 := "learned/y.gdlm"
	if got2 != want2 {
		t.Errorf("RelativisePath leaked root substring 'given': got %q want %q", got2, want2)
	}
}

// TestRenderJSON_PathIsRelative (security audit H2): the on-wire path
// MUST be the canonical root-relative POSIX form, never the absolute
// server filesystem path. Pre-fix this leaked the operator's home
// directory to every authenticated caller.
func TestRenderJSON_PathIsRelative(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "1-a", TS: "t", Author: "alice",
		Subject: "test:1", Content: "c", Scope: "fleet",
		Path: "/home/operator/projects/acme/live/outbox/alice/1-a.gdl",
	}
	if err := RenderJSON(&buf, "/home/operator/projects/acme", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	gotPath, _ := got["path"].(string)
	if gotPath != "live/outbox/alice/1-a.gdl" {
		t.Errorf("path=%q want \"live/outbox/alice/1-a.gdl\" (absolute root must not leak)", gotPath)
	}
	// Belt-and-suspenders: the rendered line must NOT contain the
	// operator's home directory anywhere — not just the path field.
	if strings.Contains(buf.String(), "/home/operator") {
		t.Errorf("rendered JSON leaked /home/operator: %s", buf.String())
	}
}

// TestRenderJSON_PathIsRelative_AllSubstrateRoots covers the three
// canonical top-level dirs.
func TestRenderJSON_PathIsRelative_AllSubstrateRoots(t *testing.T) {
	cases := []struct {
		absPath string
		wantRel string
		recType string
		subject string
	}{
		{"/srv/rufio/given/docs/spec.md", "given/docs/spec.md", "given", ""},
		{"/srv/rufio/learned/cat/x.gdlm", "learned/cat/x.gdlm", "observation", "test:1"},
		{"/srv/rufio/live/outbox/alice/1.gdl", "live/outbox/alice/1.gdl", "thought", "test:1"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		rec := RecallRecord{
			Type: c.recType, ID: "1-a", TS: "t", Author: "alice",
			Subject: c.subject, Content: "c", Scope: "fleet",
			Path: c.absPath,
		}
		if err := RenderJSON(&buf, "/srv/rufio", []RecallRecord{rec}); err != nil {
			t.Fatal(err)
		}
		var got map[string]interface{}
		json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
		if got["path"] != c.wantRel {
			t.Errorf("path=%q want %q", got["path"], c.wantRel)
		}
	}
}

// TestRenderJSON_AllOnDiskFieldsPreserved is the EXHAUSTIVE structural
// regression guard the v1.0.4 bug #1 expanded scope demands. For every
// supported record type, we construct a RecallRecord with every
// optional field populated and assert each one round-trips through
// RenderJSON intact. This is the canary: a future contributor who
// adds a new field to the disk format MUST also update RenderJSON to
// emit it, or this test will catch the silent data loss.
func TestRenderJSON_AllOnDiskFieldsPreserved(t *testing.T) {
	// Each row: (label, RecallRecord, expectedJSONKey→expectedValue map).
	// expectedValue is a checker function so we can compare floats
	// loosely and arrays exactly.
	cases := []struct {
		name string
		rec  RecallRecord
		want map[string]func(v interface{}) bool
	}{
		{
			name: "thought-with-everything",
			rec: RecallRecord{
				Type: "thought", ThoughtType: "hypothesis",
				ID: "1-a", TS: "t", Author: "alice",
				Subject: "test:1", Content: "exhaustive thought",
				Scope: "fleet", Topics: []string{"alpha", "beta"},
				TTL: 600, Parent: "0-parent",
			},
			want: map[string]func(v interface{}) bool{
				"_type":   func(v interface{}) bool { return v == "thought" },
				"type":    func(v interface{}) bool { return v == "hypothesis" },
				"id":      func(v interface{}) bool { return v == "1-a" },
				"ts":      func(v interface{}) bool { return v == "t" },
				"author":  func(v interface{}) bool { return v == "alice" },
				"subject": func(v interface{}) bool { return v == "test:1" },
				"content": func(v interface{}) bool { return v == "exhaustive thought" },
				"scope":   func(v interface{}) bool { return v == "fleet" },
				"topics": func(v interface{}) bool {
					arr, ok := v.([]interface{})
					return ok && len(arr) == 2 && arr[0] == "alpha" && arr[1] == "beta"
				},
				"ttl":    func(v interface{}) bool { f, _ := v.(float64); return int(f) == 600 },
				"parent": func(v interface{}) bool { return v == "0-parent" },
			},
		},
		{
			name: "observation-with-everything",
			rec: RecallRecord{
				Type: "observation", ID: "2-b", TS: "t", Author: "alice",
				Subject: "test:2", Predicate: "has-status", Object: "active",
				Scope: "fleet", Topics: []string{"color"},
				Confidence: 0.85,
				// Promoted-observation provenance.
				Origin: "alice", ConfirmedBy: []string{"bob", "carol"}, Source: "1-src",
			},
			want: map[string]func(v interface{}) bool{
				"_type":     func(v interface{}) bool { return v == "observation" },
				"id":        func(v interface{}) bool { return v == "2-b" },
				"author":    func(v interface{}) bool { return v == "alice" },
				"subject":   func(v interface{}) bool { return v == "test:2" },
				"predicate": func(v interface{}) bool { return v == "has-status" },
				"object":    func(v interface{}) bool { return v == "active" },
				"scope":     func(v interface{}) bool { return v == "fleet" },
				"topics": func(v interface{}) bool {
					arr, ok := v.([]interface{})
					return ok && len(arr) == 1 && arr[0] == "color"
				},
				"confidence": func(v interface{}) bool { f, _ := v.(float64); return f == 0.85 },
				"origin":     func(v interface{}) bool { return v == "alice" },
				"source":     func(v interface{}) bool { return v == "1-src" },
				"confirmed_by": func(v interface{}) bool {
					arr, ok := v.([]interface{})
					return ok && len(arr) == 2 && arr[0] == "bob" && arr[1] == "carol"
				},
			},
		},
		{
			name: "reason-with-everything",
			rec: RecallRecord{
				Type: "reason", ID: "3-c", TS: "t", Author: "alice",
				Content: "because P implies Q", Scope: "fleet",
				Subject:  "test:3",
				Topics:   []string{"logic"},
				Parent:   "2-parent-reason",
				Decision: "1-decision-id",
			},
			want: map[string]func(v interface{}) bool{
				"_type":   func(v interface{}) bool { return v == "reason" },
				"id":      func(v interface{}) bool { return v == "3-c" },
				"author":  func(v interface{}) bool { return v == "alice" },
				"content": func(v interface{}) bool { return v == "because P implies Q" },
				"scope":   func(v interface{}) bool { return v == "fleet" },
				"subject": func(v interface{}) bool { return v == "test:3" },
				"topics": func(v interface{}) bool {
					arr, ok := v.([]interface{})
					return ok && len(arr) == 1 && arr[0] == "logic"
				},
				"parent":   func(v interface{}) bool { return v == "2-parent-reason" },
				"decision": func(v interface{}) bool { return v == "1-decision-id" },
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := RenderJSON(&buf, "", []RecallRecord{c.rec}); err != nil {
				t.Fatalf("RenderJSON: %v", err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for key, check := range c.want {
				v, present := got[key]
				if !present {
					t.Errorf("missing key %q (full=%v)", key, got)
					continue
				}
				if !check(v) {
					t.Errorf("key %q: value %v did not match expectation (full=%v)", key, v, got)
				}
			}
		})
	}
}
