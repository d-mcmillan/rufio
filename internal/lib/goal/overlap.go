package goal

import (
	"regexp"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// entityRegex matches entity-id patterns like customer:5821, vendor:acme,
// or org:cloud-platform:database (D18.1). This is the same pattern that
// thought.ValidateSubject accepts (sans the ^...$ anchors so it can match
// inside free-text goal statements via FindAllString).
var entityRegex = regexp.MustCompile(`[a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+`)

// ExtractEntities returns the set of distinct entity-id strings found in
// the goal statement free-text. Order: first-occurrence-wins. Empty input
// or no matches returns nil. Per D18.1.
//
// Multi-segment entities like org:cloud-platform:database are captured as
// the full colon-joined token — regexp uses greedy matching on the
// repeated (:[a-zA-Z0-9_-]+)+ group.
func ExtractEntities(statement string) []string {
	if statement == "" {
		return nil
	}
	matches := entityRegex.FindAllString(statement, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

// OverlapPair represents a single (entity, peerGoal) match found by
// FindOverlaps. Each pair indicates the peerGoal shares the given
// entity-id with the newly-written goal.
type OverlapPair struct {
	// Entity is the shared entity-id (e.g., "customer:5821") that
	// appears in both the new goal's statement and PeerGoal.Statement.
	Entity string
	// PeerGoal is the other agent's active goal that overlaps. Always
	// has PeerGoal.Author != the new goal's Author (self-suppression,
	// D18.2) and PeerGoal.State == StateActive (D18.3).
	PeerGoal Goal
}

// FindOverlaps returns the list of (entity, peerGoal) pairs where:
//   - peerGoal.Author != newGoal.Author (self-suppression per D18.2)
//   - peerGoal.State == StateActive (D18.3)
//   - peerGoal.ID != newGoal.ID (defensive: don't self-match if the
//     newGoal happens to appear in the existing slice)
//   - intersection of ExtractEntities(newGoal.Statement) and
//     ExtractEntities(peerGoal.Statement) is non-empty
//
// Each (entity, peer) match yields one entry; if two entities overlap
// between the same goal pair, you get two entries (caller groups for
// audit-record building per D18.8).
//
// Order: iteration over `existing` preserved; within a peer goal,
// entities are emitted in the order they appear in the new goal's
// statement (first-occurrence-wins from ExtractEntities).
func FindOverlaps(newGoal Goal, existing []Goal) []OverlapPair {
	newEntities := ExtractEntities(newGoal.Statement)
	if len(newEntities) == 0 || len(existing) == 0 {
		return nil
	}
	// Set membership for the new goal's entities so peer-side intersection
	// is O(n+m) rather than O(n*m).
	newSet := make(map[string]struct{}, len(newEntities))
	for _, e := range newEntities {
		newSet[e] = struct{}{}
	}

	var out []OverlapPair
	for _, peer := range existing {
		if peer.Author == newGoal.Author {
			continue
		}
		if peer.State != StateActive {
			continue
		}
		if peer.ID == newGoal.ID {
			continue
		}
		peerEntities := ExtractEntities(peer.Statement)
		if len(peerEntities) == 0 {
			continue
		}
		// Iterate over the new goal's entities (not the peer's) so the
		// emitted Entity order is stable with respect to the source goal
		// — that's the natural reading order for the audit log.
		peerSet := make(map[string]struct{}, len(peerEntities))
		for _, e := range peerEntities {
			peerSet[e] = struct{}{}
		}
		for _, e := range newEntities {
			if _, ok := peerSet[e]; !ok {
				continue
			}
			if _, ok := newSet[e]; !ok {
				// Unreachable — e was just drawn from newEntities — but
				// keep the guard explicit for clarity.
				continue
			}
			out = append(out, OverlapPair{Entity: e, PeerGoal: peer})
		}
	}
	return out
}

// BuildOverlapRecord returns the @goal-overlap gdl.Record per D18.4.
// Field order locked at to, from, entity, target-goal, source-goal, ts.
//
//   - to: recipient agent (the inbox owner; either the new-goal author or
//     a peer whose active goal collided on entity).
//   - from: the new-goal author (the agent whose write triggered the
//     overlap scan).
//   - entity: the shared entity-id (e.g. "customer:5821") that appears in
//     both the source goal's statement and the target goal's statement.
//   - source-goal: the new goal's id (the write that triggered the scan).
//   - target-goal: the peer's pre-existing active goal id.
//   - ts: detection timestamp.
func BuildOverlapRecord(to, from, entity, sourceGoalID, targetGoalID, ts string) gdl.Record {
	return gdl.Record{Type: "goal-overlap", Fields: []gdl.RecordField{
		{Key: "to", Value: to},
		{Key: "from", Value: from},
		{Key: "entity", Value: entity},
		{Key: "target-goal", Value: targetGoalID},
		{Key: "source-goal", Value: sourceGoalID},
		{Key: "ts", Value: ts},
	}}
}
