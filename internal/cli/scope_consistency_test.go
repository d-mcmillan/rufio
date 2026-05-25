// Package cli — tests for verb-pattern consistency around --scope (#125).
//
// Defect being guarded: `rufio think`/`observe`/`recall`/`listen`/`stream`/
// `goal` all accept --scope, but `rufio attend` + `rufio reason` reject it
// with "unknown flag: --scope". An agent who learned the --scope pattern
// from any other verb and reasonably extended it to attend/reason got an
// error AND (worse) didn't realize their record wasn't written with the
// scope they intended.
//
// These tests lock the contract:
//
//	(a) `attend --scope=fleet|deployment|agent` is accepted, and the
//	    written live/attention/<agent>.gdl carries `scope:<value>`.
//	(b) `attend` with no --scope flag defaults the on-disk scope to fleet
//	    (attention is broadcast — fleet is the symmetric default to think's
//	    explicit-required scope; we don't break the require-on-think
//	    contract, just give attend a sane default since attend is the
//	    discovery primitive).
//	(c) `reason --scope=...` is symmetric to attend's contract.
//	(d) Privacy: when alice writes an attention with scope:agent, bob's
//	    `rufio fleet` MUST NOT include alice's row (today fleet only
//	    redacts entities/topics — the row remains, leaking presence + the
//	    intent string). With scope:agent attention, the whole row is hidden
//	    from non-self callers, mirroring the privacy.IsVisible rule.
//	(e) Privacy: when alice writes a reason with scope:agent against a
//	    fleet-scoped decision, bob's `rufio lineage <decision>` MUST NOT
//	    include alice's @reason step. Bob's own reasoning at ANY scope
//	    remains visible to him.
//
// Tests run the runAttend/runReason CLI handlers directly (the same
// convention as the sibling dev_*_test.go files) — that's where the
// flag-plumbing-to-record decision lives, and avoids spinning the full
// Cobra parser for a unit check.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// scopeTestProject scaffolds a minimal rufio project at t.TempDir() with
// rufio.gdl + an identity.local.gdl pinning agent. Returns the root path.
// Mirrors the layout FindProjectRoot expects (rufio.gdl marker) plus the
// identity file Resolve reads.
func scopeTestProject(t *testing.T, agent string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte(""), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
		t.Fatalf("mkdir .rufio: %v", err)
	}
	idFile := filepath.Join(root, ".rufio", "identity.local.gdl")
	rec := gdl.Record{Type: "identity", Fields: []gdl.RecordField{
		{Key: "agent", Value: agent},
		{Key: "set-at", Value: versioning.NowISO()},
	}}
	if err := os.WriteFile(idFile, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	// Ensure RUFIO_AGENT_ID doesn't shadow the local file.
	t.Setenv("RUFIO_AGENT_ID", "")
	return root
}

// readAttentionScope reads live/attention/<agent>.gdl, parses the
// @attention record, and returns the `scope:` field value (empty when the
// field is absent — pre-#125 records have no scope).
func readAttentionScope(t *testing.T, root, agent string) string {
	t.Helper()
	bs, err := os.ReadFile(filepath.Join(root, "live", "attention", agent+".gdl"))
	if err != nil {
		t.Fatalf("read attention: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse attention: %v", err)
	}
	if len(records) != 1 || records[0].Type != "attention" {
		t.Fatalf("expected 1 @attention record, got %d: %q", len(records), string(bs))
	}
	return records[0].Get("scope")
}

// TestAttend_AcceptsScopeFlag asserts contract (a): --scope=fleet is
// accepted and the written record carries scope:fleet.
func TestAttend_AcceptsScopeFlag(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runAttend(root, attendArgs{
		Intent:   "debugging auth",
		Entities: "customer:5821",
		Scope:    "fleet",
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runAttend: unexpected error %v", err)
	}
	if got := readAttentionScope(t, root, "alice"); got != "fleet" {
		t.Errorf("attention scope = %q, want %q", got, "fleet")
	}
}

// TestAttend_DefaultsScopeFleet asserts contract (b): no --scope flag
// defaults the on-disk scope to fleet.
func TestAttend_DefaultsScopeFleet(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runAttend(root, attendArgs{
		Intent:   "debugging auth",
		Entities: "customer:5821",
		// Scope intentionally omitted.
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runAttend: unexpected error %v", err)
	}
	if got := readAttentionScope(t, root, "alice"); got != "fleet" {
		t.Errorf("attention scope (default) = %q, want %q", got, "fleet")
	}
}

// TestAttend_AcceptsScopeAgent asserts contract (a) for the most
// restrictive enum value — scope:agent records are the privacy-floor
// case the rest of #147 already understands.
func TestAttend_AcceptsScopeAgent(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runAttend(root, attendArgs{
		Intent:   "private session",
		Entities: "customer:5821",
		Scope:    "agent",
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runAttend: unexpected error %v", err)
	}
	if got := readAttentionScope(t, root, "alice"); got != "agent" {
		t.Errorf("attention scope = %q, want %q", got, "agent")
	}
}

// reasonScopeOf walks live/reasoning/<agent>/*.gdl and returns the scope
// field of the first @reason record found. Used by the reason tests.
func reasonScopeOf(t *testing.T, root, agent string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "live", "reasoning", agent, "*.gdl"))
	if err != nil {
		t.Fatalf("glob reasoning: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no reasoning files under live/reasoning/%s/", agent)
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read reasoning: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse reasoning: %v", err)
	}
	for _, r := range records {
		if r.Type == "reason" {
			return r.Get("scope")
		}
	}
	t.Fatalf("no @reason record in %s", matches[0])
	return ""
}

// TestReason_AcceptsScopeFlag asserts contract (c): --scope=fleet is
// accepted and the written @reason carries scope:fleet.
func TestReason_AcceptsScopeFlag(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runReason(root, reasonArgs{
		Content: "we should refund based on policy 4.2",
		Scope:   "fleet",
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runReason: unexpected error %v", err)
	}
	if got := reasonScopeOf(t, root, "alice"); got != "fleet" {
		t.Errorf("reason scope = %q, want %q", got, "fleet")
	}
}

// TestReason_DefaultsScopeFleet asserts the no-flag default is fleet —
// symmetric with TestAttend_DefaultsScopeFleet so cold agents can
// predict.
func TestReason_DefaultsScopeFleet(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runReason(root, reasonArgs{
		Content: "default scope test",
		// Scope intentionally omitted.
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runReason: unexpected error %v", err)
	}
	if got := reasonScopeOf(t, root, "alice"); got != "fleet" {
		t.Errorf("reason scope (default) = %q, want %q", got, "fleet")
	}
}

// TestReason_AcceptsScopeAgent — scope:agent is the privacy-relevant
// value; assert the flag plumbs through to disk.
func TestReason_AcceptsScopeAgent(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runReason(root, reasonArgs{
		Content: "private reasoning",
		Scope:   "agent",
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runReason: unexpected error %v", err)
	}
	if got := reasonScopeOf(t, root, "alice"); got != "agent" {
		t.Errorf("reason scope = %q, want %q", got, "agent")
	}
}

// TestAttend_ScopeAgent_HiddenInFleetFromOtherAgents asserts contract (d):
// bob's `rufio fleet` does NOT surface alice's scope:agent attention.
// Verified via collectAgents+redact (the path runFleet takes), inspecting
// whether alice appears as a row when bob is the currentAgent.
func TestAttend_ScopeAgent_HiddenInFleetFromOtherAgents(t *testing.T) {
	root := scopeTestProject(t, "alice")
	// alice writes a scope:agent attention.
	if err := runAttend(root, attendArgs{
		Intent:   "private session",
		Entities: "customer:5821",
		Scope:    "agent",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("alice runAttend: %v", err)
	}
	// Reset identity to bob — same project root, different caller.
	t.Setenv("RUFIO_AGENT_ID", "bob")

	rows, err := collectAgents(root)
	if err != nil {
		t.Fatalf("collectAgents: %v", err)
	}
	rows = redactPrivateAttentionFields(rows, "bob")

	for _, r := range rows {
		if r.Agent == "alice" {
			t.Errorf("alice's scope:agent attention must be hidden from bob in fleet; got row %+v", r)
		}
	}

	// Sanity: alice CAN still see her own scope:agent attention.
	rowsSelf, err := collectAgents(root)
	if err != nil {
		t.Fatalf("collectAgents (self): %v", err)
	}
	rowsSelf = redactPrivateAttentionFields(rowsSelf, "alice")
	foundSelf := false
	for _, r := range rowsSelf {
		if r.Agent == "alice" {
			foundSelf = true
			if r.Intent != "private session" {
				t.Errorf("alice's own row intent = %q, want %q", r.Intent, "private session")
			}
		}
	}
	if !foundSelf {
		t.Errorf("alice's scope:agent attention must remain visible to alice in her own fleet view")
	}
}

// TestReason_ScopeAgent_HiddenInLineageFromOtherAgents asserts contract
// (e): bob lineages a fleet-scoped decision; alice's scope:agent reason
// against that decision must NOT appear in bob's chain. Bob's own
// reasoning at any scope remains visible.
func TestReason_ScopeAgent_HiddenInLineageFromOtherAgents(t *testing.T) {
	root := scopeTestProject(t, "alice")

	// Seed a decision authored by alice with type:decision so lineage
	// will look it up. Write a minimal @thought record directly under
	// live/outbox/alice/<id>.gdl — using the lib package keeps the
	// on-disk shape identical to a real `rufio think --type=decision`.
	decisionID := "1747000000000-dec123"
	dec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID:      decisionID,
		Author:  "alice",
		Type:    "decision",
		Subject: "customer:5821",
		Content: "approve refund",
		Scope:   "fleet",
		TS:      versioning.NowISO(),
	})
	if err := thought.Write(root, "alice", decisionID, []gdl.Record{dec}); err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	// alice writes a scope:agent reason against the decision.
	if err := runReason(root, reasonArgs{
		Content:  "private rationale alice",
		Decision: decisionID,
		Scope:    "agent",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("alice runReason: %v", err)
	}

	// Switch identity to bob. bob writes a fleet-scoped reason on the
	// same decision so bob's own row anchors the visibility check.
	t.Setenv("RUFIO_AGENT_ID", "bob")
	if err := runReason(root, reasonArgs{
		Content:  "fleet rationale bob",
		Decision: decisionID,
		Scope:    "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("bob runReason: %v", err)
	}

	// Bob now lineages the decision. Read the rendered tree from
	// runLineage's columnar path via captureStdout.
	out := captureStdout(t, func() {
		if err := runLineage(root, decisionID, output.RenderOpts{Quiet: true}); err != nil {
			t.Fatalf("runLineage: %v", err)
		}
	})

	if strings.Contains(out, "private rationale alice") {
		t.Errorf("alice's scope:agent reason must NOT appear in bob's lineage; got:\n%s", out)
	}
	if !strings.Contains(out, "fleet rationale bob") {
		t.Errorf("bob's own fleet reason must appear in bob's lineage; got:\n%s", out)
	}
}
