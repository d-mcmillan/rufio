package open

import (
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
)

// TestBundle_EmptySubstrate_ReturnsEmptyBundle pins Task 1's scaffolding
// contract: a brand-new substrate (no records, no identity, no daemon)
// must produce a syntactically complete OpenBundle — Subject populated,
// every slice non-nil-but-empty — so the renderer can iterate without
// nil checks regardless of substrate state.
func TestBundle_EmptySubstrate_ReturnsEmptyBundle(t *testing.T) {
	root := t.TempDir()
	b, err := Bundle(root, Params{Subject: "test:1"})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if b.Subject != "test:1" {
		t.Errorf("Subject = %q, want %q", b.Subject, "test:1")
	}
	if len(b.Recall) != 0 || len(b.Thoughts) != 0 || len(b.Fleet) != 0 || len(b.Attention) != 0 {
		t.Errorf("Expected empty sections, got: recall=%d thoughts=%d fleet=%d attention=%d",
			len(b.Recall), len(b.Thoughts), len(b.Fleet), len(b.Attention))
	}
}

// TestBundle_PopulatesIdentity pins that Bundle echoes the caller-supplied
// CurrentAgent into b.Agent. Bundle does not resolve identity itself —
// the CLI front door owns identity resolution so the lib stays pure.
func TestBundle_PopulatesIdentity(t *testing.T) {
	root := t.TempDir()
	b, err := Bundle(root, Params{Subject: "test:1", CurrentAgent: "agent-alice"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Agent != "agent-alice" {
		t.Errorf("Agent = %q, want %q", b.Agent, "agent-alice")
	}
}

// TestBundle_PopulatesDaemonStatus_NotRunning pins that with no heartbeat
// file on disk, b.Daemon reports StateNotRunning. devhealth.Status fails
// closed (missing file → not running) so a cold substrate produces a
// clean "daemon: not running" signal rather than a fabricated "ok".
func TestBundle_PopulatesDaemonStatus_NotRunning(t *testing.T) {
	root := t.TempDir()
	b, err := Bundle(root, Params{Subject: "test:1"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Daemon.State != devhealth.StateNotRunning {
		t.Errorf("Daemon.State = %v, want StateNotRunning", b.Daemon.State)
	}
}

// TestBundle_RecallSectionFiltersBySubject pins that the Recall slot
// contains only records whose subject matches the caller's Params.Subject.
// Reuses recall.Match's exact-subject path (the entity-id form).
func TestBundle_RecallSectionFiltersBySubject(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	seedThought(t, root, "agent-a", freshID(t), "test:1", "on-target", "fleet", nil, ts)
	seedThought(t, root, "agent-a", freshID(t), "test:other", "elsewhere", "fleet", nil, ts)

	b, err := Bundle(root, Params{Subject: "test:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Recall) != 1 {
		t.Errorf("Recall len = %d, want 1", len(b.Recall))
	}
	for _, r := range b.Recall {
		if r.Subject != "test:1" {
			t.Errorf("Recall record with subject=%q leaked past subject filter", r.Subject)
		}
	}
}

// TestBundle_RecallRespectsTopicsFilter_ServerSide pins #180's
// server-side --topics filter is on the path. Records without matching
// topics are excluded — ANY-match against r.Topics, NO implicit "all
// topics" match for unlabeled records.
func TestBundle_RecallRespectsTopicsFilter_ServerSide(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	seedThought(t, root, "agent-a", freshID(t), "test:1", "alpha record", "fleet",
		[]string{"alpha", "beta"}, ts)
	seedThought(t, root, "agent-a", freshID(t), "test:1", "gamma record", "fleet",
		[]string{"gamma"}, ts)

	b, err := Bundle(root, Params{Subject: "test:1", Topics: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Recall) != 1 {
		t.Errorf("Recall len = %d, want 1 (alpha-tagged only)", len(b.Recall))
	}
}

// TestBundle_RecallRespectsSince pins that --since=24h excludes records
// older than the floor. Without a Since the test would see both — with
// Since=24h only the recent record survives.
func TestBundle_RecallRespectsSince(t *testing.T) {
	root := t.TempDir()
	oldTS := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339Nano)
	newTS := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	seedThought(t, root, "agent-a", freshID(t), "test:1", "old", "fleet", nil, oldTS)
	seedThought(t, root, "agent-a", freshID(t), "test:1", "new", "fleet", nil, newTS)

	b, err := Bundle(root, Params{Subject: "test:1", Since: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Recall) != 1 {
		t.Errorf("Recall len = %d, want 1 (within --since=24h only)", len(b.Recall))
	}
}

// TestBundle_ThoughtsListFilteredToSubject pins that the Thoughts slot
// surfaces ONLY records whose subject matches Params.Subject. Thoughts
// is the broader companion to Recall — it includes every @thought type
// (no thought-subtype filter), letting cold agents see the full thought
// history on subject (decision, hypothesis, focus, question, observation
// thought-subtype) without re-running a second recall call.
func TestBundle_ThoughtsListFilteredToSubject(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	seedThought(t, root, "agent-a", freshID(t), "test:1", "alpha", "fleet", nil, ts)
	seedThought(t, root, "agent-a", freshID(t), "test:other", "beta", "fleet", nil, ts)

	b, err := Bundle(root, Params{Subject: "test:1", Since: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Thoughts) != 1 {
		t.Errorf("Thoughts len = %d, want 1", len(b.Thoughts))
	}
	for _, r := range b.Thoughts {
		if r.Subject != "test:1" {
			t.Errorf("Thoughts record with subject=%q leaked", r.Subject)
		}
		if r.Type != "thought" {
			t.Errorf("Thoughts record with type=%q (should be thought only)", r.Type)
		}
	}
}

// TestBundle_FleetSortedByLastSeenDesc pins the fleet section's ordering:
// agents with a more recent @attention record sort first so cold agents
// see the most-recently-engaged peers at the top.
func TestBundle_FleetSortedByLastSeenDesc(t *testing.T) {
	root := t.TempDir()
	seedAttention(t, root, "agent-old", time.Now().Add(-10*time.Hour))
	seedAttention(t, root, "agent-new", time.Now().Add(-1*time.Hour))

	b, err := Bundle(root, Params{Subject: "test:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Fleet) != 2 {
		t.Fatalf("Fleet len = %d, want 2", len(b.Fleet))
	}
	if b.Fleet[0].Agent != "agent-new" {
		t.Errorf("Fleet[0] = %q, want agent-new (most recent)", b.Fleet[0].Agent)
	}
	if b.Fleet[1].Agent != "agent-old" {
		t.Errorf("Fleet[1] = %q, want agent-old", b.Fleet[1].Agent)
	}
}

// TestBundle_AttentionTopThreeFleetAgents pins that the Attention slot
// is capped at the top-3 fleet rows by LastSeen — that bound is the
// agreed read-tax-reduction shape from the cross-harness Run 3 spec.
// Cold agents see the three most-recently-engaged peers' current intent;
// going broader would re-introduce the noise the bundle is meant to cut.
func TestBundle_AttentionTopThreeFleetAgents(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		// Hours-ago ascending: agent-0 most recent, agent-4 oldest.
		agent := "agent-" + string(rune('a'+i))
		seedAttention(t, root, agent, time.Now().Add(-time.Duration(i)*time.Hour))
	}
	b, err := Bundle(root, Params{Subject: "test:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Attention) != 3 {
		t.Errorf("Attention len = %d, want 3 (top-3 fleet)", len(b.Attention))
	}
	// Fleet itself stays uncapped so callers can drill into the full
	// engaged-peer roster.
	if len(b.Fleet) != 5 {
		t.Errorf("Fleet len = %d, want 5 (fleet uncapped)", len(b.Fleet))
	}
}

// TestBundle_PrivacyHidesOtherAgentScopeAgent pins the #147 privacy
// floor: scope:agent records authored by another agent are NEVER visible
// to the current identified caller, AND HiddenPrivateCount surfaces the
// elision so the renderer can show a footer line.
func TestBundle_PrivacyHidesOtherAgentScopeAgent(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	// agent-other writes a private (scope:agent) record on test:1.
	seedThought(t, root, "agent-other", freshID(t), "test:1", "secret", "agent", nil, ts)
	// agent-self writes a public (scope:fleet) record on test:1.
	seedThought(t, root, "agent-self", freshID(t), "test:1", "public", "fleet", nil, ts)

	b, err := Bundle(root, Params{Subject: "test:1", CurrentAgent: "agent-self"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Recall) != 1 {
		t.Errorf("Recall len = %d, want 1 (hide other's scope:agent)", len(b.Recall))
	}
	if b.HiddenPrivateCount < 1 {
		t.Errorf("HiddenPrivateCount = %d, want >=1 (the elided private record)", b.HiddenPrivateCount)
	}
}

// TestBundle_PrivacyOwnScopeAgentVisible pins that an agent can ALWAYS
// see their own scope:agent records — the privacy floor only blocks
// foreign authors. Without this branch the cold-agent first-contact
// workflow (open my private subject) would silently elide my own data.
func TestBundle_PrivacyOwnScopeAgentVisible(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	seedThought(t, root, "agent-self", freshID(t), "test:1", "mine", "agent", nil, ts)

	b, err := Bundle(root, Params{Subject: "test:1", CurrentAgent: "agent-self"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Recall) != 1 {
		t.Errorf("Own scope:agent record should be visible; got len=%d", len(b.Recall))
	}
	if b.HiddenPrivateCount != 0 {
		t.Errorf("HiddenPrivateCount = %d, want 0 (no foreign privacy elision)", b.HiddenPrivateCount)
	}
}

// TestBundle_DefaultScopeFleet_SurfacesOtherAgentsFleetBroadcasts is the
// regression guard for the Task 14 smoke finding: with scope=fleet
// (open's default) alice running `rufio open test:1` must see bob's
// scope=fleet thought on test:1 — same-rank broadcasts are visible to
// everyone, NOT only to the author. The recall.Filter scopePass rule
// (same-rank → same-author-only) is wrong for fleet semantics (fleet IS
// the universal broadcast scope), so effectiveScope() maps "fleet" → ""
// and the privacy.IsVisible (#147) floor takes over instead.
//
// If a future change re-routes scope=fleet through scopePass, this test
// fails loudly and rufio open silently regresses to "show me ONLY my
// own records under the default flag", which destroys its read-dual
// utility.
func TestBundle_DefaultScopeFleet_SurfacesOtherAgentsFleetBroadcasts(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	// Bob writes a fleet-broadcast thought on test:1.
	seedThought(t, root, "agent-bob", freshID(t), "test:1", "bob's broadcast", "fleet", nil, ts)
	// Alice writes one too.
	seedThought(t, root, "agent-alice", freshID(t), "test:1", "alice's broadcast", "fleet", nil, ts)

	// Alice opens test:1 with the default scope=fleet — she MUST see
	// both records.
	b, err := Bundle(root, Params{
		Subject:      "test:1",
		Scope:        "fleet",
		CurrentAgent: "agent-alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Recall) != 2 {
		t.Errorf("Recall len = %d, want 2 (alice's + bob's fleet broadcasts on test:1 must both surface under scope=fleet)", len(b.Recall))
	}
}

// TestBundle_ScopeAgent_NarrowsToOwnAgentScoped pins that an explicit
// --scope=agent DOES narrow the recall section to the caller's own
// agent-scoped records (complement of the regression above). Agent
// scope is the use case scopePass was designed for; effectiveScope()
// passes it through unchanged.
func TestBundle_ScopeAgent_NarrowsToOwnAgentScoped(t *testing.T) {
	root := t.TempDir()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	seedThought(t, root, "agent-alice", freshID(t), "test:1", "alice agent-scoped", "agent", nil, ts)
	seedThought(t, root, "agent-bob", freshID(t), "test:1", "bob agent-scoped", "agent", nil, ts)
	// A fleet broadcast from a third agent — should ALWAYS surface
	// (broader than the filter, per scopePass).
	seedThought(t, root, "agent-carol", freshID(t), "test:1", "carol fleet", "fleet", nil, ts)

	b, err := Bundle(root, Params{
		Subject:      "test:1",
		Scope:        "agent",
		CurrentAgent: "agent-alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Visible: alice's agent-scoped record + carol's fleet (broader).
	// Hidden: bob's agent-scoped (same-rank, foreign author).
	if len(b.Recall) != 2 {
		t.Errorf("Recall len = %d, want 2 (alice's own + carol's fleet); bob's agent-scoped should be hidden", len(b.Recall))
	}
}
