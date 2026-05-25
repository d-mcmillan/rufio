// Package routing implements the daemon's RoutingHandler engine — the
// 1st of 5 engines per design §2.I. Pure functions; daemon integration
// lives in internal/cli/dev.go.
package routing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// Attention represents one agent's current attention state.
type Attention struct {
	Agent    string
	Entities []string
	Topics   []string
}

// ThoughtForRouting is the subset of @thought fields the matcher cares about.
type ThoughtForRouting struct {
	ID      string
	Author  string
	Subject string
	Topics  []string
}

// ReadAttentions walks live/attention/*.gdl, parses each, returns a map
// keyed by agent name. Missing directory → empty map (not error).
func ReadAttentions(root string) (map[string]Attention, error) {
	dir := filepath.Join(root, "live", "attention")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Attention{}, nil
		}
		return nil, err
	}
	out := make(map[string]Attention, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
			continue
		}
		bs, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			if r.Type != "attention" {
				continue
			}
			agent := r.Get("agent")
			if agent == "" {
				continue
			}
			att := Attention{Agent: agent}
			if v := r.Get("entities"); v != "" {
				att.Entities = strings.Split(v, ",")
			}
			if v := r.Get("topics"); v != "" {
				att.Topics = strings.Split(v, ",")
			}
			out[agent] = att
			break
		}
	}
	return out, nil
}

// MatchRecipients returns agents whose attention matches the thought
// (per D11.1: entities ∩ subject OR topics ∩ topics) excluding the
// thought's author (D11.2). Sorted by agent name (deterministic).
func MatchRecipients(t ThoughtForRouting, attentions map[string]Attention) []string {
	out := make([]string, 0)
	for agent, att := range attentions {
		if agent == t.Author {
			continue
		}
		if matchEntities(t.Subject, att.Entities) || matchTopicsAny(t.Topics, att.Topics) {
			out = append(out, agent)
		}
	}
	sort.Strings(out)
	return out
}

func matchEntities(subject string, entities []string) bool {
	if subject == "" {
		return false
	}
	for _, e := range entities {
		if e == subject {
			return true
		}
	}
	return false
}

func matchTopicsAny(thoughtTopics, attentionTopics []string) bool {
	if len(thoughtTopics) == 0 || len(attentionTopics) == 0 {
		return false
	}
	set := make(map[string]bool, len(attentionTopics))
	for _, t := range attentionTopics {
		set[t] = true
	}
	for _, t := range thoughtTopics {
		if set[t] {
			return true
		}
	}
	return false
}

// RouteThought reads the thought record at thoughtPath, scans
// attentions, identifies recipients, and writes the thought (+ a fresh
// @route marker) to each matching inbox atomically.
//
// Per D11.7 best-effort: per-recipient write failures are logged to
// stderr and other recipients continue. Idempotent (D11.3): if an inbox
// file for the (recipient, thought-id) tuple already exists, the write
// is skipped.
//
// Lock domain: .rufio/locks/inbox-<recipient>.lock per design §4.D.
func RouteThought(root, thoughtPath string) error {
	bs, err := os.ReadFile(thoughtPath)
	if err != nil {
		return err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return err
	}
	if len(records) == 0 || records[0].Type != "thought" {
		// Not a thought record (could be a context-bundle-only file or
		// other content). Nothing to route.
		return nil
	}
	thought := records[0]
	thoughtLine := gdl.RenderLine(thought)
	t := ThoughtForRouting{
		ID:      thought.Get("id"),
		Author:  thought.Get("author"),
		Subject: thought.Get("subject"),
	}
	if v := thought.Get("topics"); v != "" {
		t.Topics = strings.Split(v, ",")
	}

	attentions, err := ReadAttentions(root)
	if err != nil {
		return err
	}
	recipients := MatchRecipients(t, attentions)
	if len(recipients) == 0 {
		return nil
	}

	routeTS := versioning.NowISO()
	for _, recipient := range recipients {
		if err := deliverToInbox(root, recipient, t.ID, t.Author, thoughtLine, routeTS); err != nil {
			// Best-effort: log and continue (D11.7).
			fmt.Fprintf(os.Stderr, "routing: failed to deliver to %s: %v\n", recipient, err)
		}
	}
	return nil
}

// RouteSummon reads the summon record at summonPath, extracts the
// explicit `to:` recipient, and delivers a copy to that agent's inbox
// via deliverToInbox. Idempotent (deliverToInbox skips if the destination
// already exists). Lock domain: .rufio/locks/inbox-<recipient>.lock —
// shared with RouteThought.
//
// Unlike RouteThought, no attention scan is needed: summons name their
// recipient explicitly.
func RouteSummon(root, summonPath string) error {
	bs, err := os.ReadFile(summonPath)
	if err != nil {
		return err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return err
	}
	if len(records) == 0 || records[0].Type != "summon" {
		// Not a summon record. Nothing to route.
		return nil
	}
	summon := records[0]
	summonLine := gdl.RenderLine(summon)
	recipient := summon.Get("to")
	id := summon.Get("id")
	from := summon.Get("from")
	if recipient == "" || id == "" {
		// Malformed record — skip silently. Caller already logs scan errors.
		return nil
	}
	routeTS := versioning.NowISO()
	return deliverToInbox(root, recipient, id, from, summonLine, routeTS)
}

// RouteChannelMessage reads the @channel-message (or legacy @say)
// record at messagePath, looks up the channel's current members via
// channels.LoadMeta, and delivers a copy to each OTHER current member
// via deliverToInbox. The sender is not delivered to.
//
// messagePath has shape live/channels/active/<ch-id>/messages/<msg-id>.gdl;
// chID is parsed from the record's `channel:` field. Idempotent via
// deliverToInbox.
//
// Issue #107 backward-compat (belt-and-suspenders, not a user-visible
// bridge): the dual-token check on line 246 bypasses the early-return
// type-guard so legacy @say records are STILL DELIVERED to the
// inbox file (locked by TestRouteChannelMessage_LegacySayType_StillRoutes).
// It does NOT restore listen visibility — gdl.RenderLine preserves
// the original token, so legacy records land as @say| lines which
// `rufio listen` still filters out via recall.AllTypes. Only the TUI
// projection at watch_panes.go:239 surfaces legacy records to a user.
// v1.0.1 shipped 2026-05-19 + channels have 24h TTL via summon
// expiry, so in-flight @say records age out within ~24h of upgrade.
//
// Race with close: if the channel is closed between message write and
// routing (e.g., catch-up scan finds an orphaned message file in
// active/), channels.LoadMeta surfaces *NoSuchChannelError; this
// function logs+returns nil (best-effort).
func RouteChannelMessage(root, messagePath string) error {
	bs, err := os.ReadFile(messagePath)
	if err != nil {
		return err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return err
	}
	if len(records) == 0 || records[0].Type != "channel-message" {
		// Not a channel-message record. Nothing to route.
		return nil
	}
	say := records[0]
	msgID := say.Get("id")
	chID := say.Get("channel")
	by := say.Get("by")
	if msgID == "" || chID == "" || by == "" {
		// Malformed record — skip silently (parallel to RouteSummon).
		return nil
	}

	meta, err := channels.LoadMeta(root, chID)
	if err != nil {
		var nscErr *rufioerr.NoSuchChannelError
		if errors.As(err, &nscErr) {
			// Catch-up scan may find an orphaned message file whose
			// channel directory was already moved to closed/ (and
			// somehow purged) — best-effort skip.
			fmt.Fprintf(os.Stderr, "routing: channel %s gone, skipping message %s\n", chID, msgID)
			return nil
		}
		return err
	}
	if meta.Closed {
		// Race with close: catch-up shouldn't deliver to a closed channel.
		fmt.Fprintf(os.Stderr, "routing: channel %s closed, skipping message %s\n", chID, msgID)
		return nil
	}

	sayLine := gdl.RenderLine(say)
	routeTS := versioning.NowISO()
	for _, member := range meta.CurrentMembers() {
		if member == by {
			continue // don't echo to sender
		}
		if err := deliverToInbox(root, member, msgID, by, sayLine, routeTS); err != nil {
			// Best-effort: log and continue (parallel to RouteThought).
			fmt.Fprintf(os.Stderr, "routing: failed to deliver channel-message to %s: %v\n", member, err)
		}
	}
	return nil
}

// RouteGoalOverlap reads the new goal at newGoalPath, scans all other
// agents' active goals for entity-id intersection, and writes a
// @goal-overlap notification file to BOTH the new-goal author's inbox AND
// each overlapping peer's inbox (D18.5).
//
// Per D18.6/D18.8: one inbox file per (source-goal, target-goal) pair,
// named "<source-goal>-overlap-<target-goal>.gdl". The "-overlap-"
// separator distinguishes overlap notifications from thought/summon/
// channel-message inbox deliveries (which use just the source id). Each
// file contains N @goal-overlap records, one per shared entity.
//
// Idempotent (D18.7): if the destination file already exists, the write
// is skipped. Best-effort delivery (parallel to RouteThought/Summon/
// ChannelMessage): per-recipient errors are logged to stderr and other
// recipients continue.
//
// Lock domain (D18.10): .rufio/locks/inbox-<recipient>.lock — shared
// with thought/summon/channel routing.
func RouteGoalOverlap(root, newGoalPath string) error {
	bs, err := os.ReadFile(newGoalPath)
	if err != nil {
		return err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return err
	}
	if len(records) == 0 || records[0].Type != "goal" {
		// Not a @goal record. Nothing to scan.
		return nil
	}
	newGoalRec := records[0]
	newGoalID := newGoalRec.Get("id")
	newAuthor := newGoalRec.Get("author")
	statement := newGoalRec.Get("statement")
	if newGoalID == "" || newAuthor == "" {
		// Malformed record — skip silently (parallel to RouteSummon).
		return nil
	}

	// Reconstruct just enough of the Goal struct for FindOverlaps. State
	// is forced to StateActive: the new goal is by definition active —
	// it was just written to live/goals/active/<id>.gdl.
	newGoal := goal.Goal{
		ID:        newGoalID,
		Author:    newAuthor,
		Statement: statement,
		State:     goal.StateActive,
	}

	existing, err := goal.ReadAll(root)
	if err != nil {
		return err
	}

	pairs := goal.FindOverlaps(newGoal, existing)
	if len(pairs) == 0 {
		return nil
	}

	// Group by (PeerGoal.Author, PeerGoal.ID) so each (source, target)
	// pair produces a single inbox file with N records inside (D18.8).
	type peerKey struct{ author, id string }
	groups := make(map[peerKey][]goal.OverlapPair)
	for _, p := range pairs {
		k := peerKey{author: p.PeerGoal.Author, id: p.PeerGoal.ID}
		groups[k] = append(groups[k], p)
	}

	// Stable iteration order for determinism (parallel to
	// MatchRecipients' sort.Strings call). Sort the peer keys.
	keys := make([]peerKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].author != keys[j].author {
			return keys[i].author < keys[j].author
		}
		return keys[i].id < keys[j].id
	})

	ts := versioning.NowISO()
	for _, k := range keys {
		ps := groups[k]
		// Notify the peer (target-goal author).
		if err := deliverOverlapFile(root, k.author, newAuthor, k.id, newGoalID, ps, ts); err != nil {
			fmt.Fprintf(os.Stderr, "routing: failed to deliver goal-overlap to peer %s: %v\n", k.author, err)
		}
		// Notify the new-goal author (self) so they see the collision too.
		if err := deliverOverlapFile(root, newAuthor, newAuthor, k.id, newGoalID, ps, ts); err != nil {
			fmt.Fprintf(os.Stderr, "routing: failed to deliver goal-overlap to self %s: %v\n", newAuthor, err)
		}
	}
	return nil
}

// deliverOverlapFile writes a single inbox file containing N @goal-overlap
// records — one per entity in pairs (D18.8). All records share the same
// (source-goal, target-goal, from, to, ts); they differ only in the
// `entity:` field.
//
// Filename: <sourceGoalID>-overlap-<targetGoalID>.gdl in the recipient's
// inbox dir. Idempotent (D18.7): if the destination file already exists,
// the write is skipped — both before and after lock acquisition (mirrors
// deliverToInbox's double-check pattern).
//
// Lock domain (D18.10): .rufio/locks/inbox-<recipient>.lock — the same
// lock used by deliverToInbox so concurrent thought/summon/channel-
// message routing to the same recipient is serialised.
func deliverOverlapFile(root, recipient, fromAuthor, targetGoalID, sourceGoalID string, pairs []goal.OverlapPair, ts string) error {
	inboxDir := filepath.Join(root, "live", "inbox", recipient)
	fname := sourceGoalID + "-overlap-" + targetGoalID + ".gdl"
	target := filepath.Join(inboxDir, fname)
	if _, err := os.Stat(target); err == nil {
		return nil // already delivered; idempotent skip
	}

	lockDir := filepath.Join(root, ".rufio", "locks", "inbox-"+recipient+".lock")
	_, err := fslock.WithLock(lockDir, 0, func() (struct{}, error) {
		// Re-check inside the lock — another routing pass may have
		// just landed the file between Stat and lock acquisition.
		if _, statErr := os.Stat(target); statErr == nil {
			return struct{}{}, nil
		}
		if err := os.MkdirAll(inboxDir, 0o755); err != nil {
			return struct{}{}, err
		}
		var body strings.Builder
		for _, p := range pairs {
			rec := goal.BuildOverlapRecord(recipient, fromAuthor, p.Entity, sourceGoalID, targetGoalID, ts)
			body.WriteString(gdl.RenderLine(rec))
			body.WriteString("\n")
		}
		tmp := target + ".tmp"
		// Best-effort cleanup so a failed WriteFile/Rename never strands
		// the overlap-file tmp under live/inbox/ (#141). Success path:
		// Rename already moved tmp, so this Remove is a no-op. The
		// double-checked idempotency skip above is unchanged.
		defer func() { _ = os.Remove(tmp) }()
		if err := os.WriteFile(tmp, []byte(body.String()), 0o644); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, os.Rename(tmp, target)
	})
	return err
}

// deliverToInbox writes (or skips) the inbox file for a single recipient.
// Returns nil and skips when the destination file already exists
// (idempotency per D11.3).
func deliverToInbox(root, recipient, thoughtID, author, thoughtLine, routeTS string) error {
	inboxDir := filepath.Join(root, "live", "inbox", recipient)
	target := filepath.Join(inboxDir, thoughtID+".gdl")
	if _, err := os.Stat(target); err == nil {
		return nil // already delivered; idempotent skip
	}

	lockDir := filepath.Join(root, ".rufio", "locks", "inbox-"+recipient+".lock")
	_, err := fslock.WithLock(lockDir, 0, func() (struct{}, error) {
		// Re-check inside the lock — another routing pass may have
		// just landed the file between Stat and lock acquisition.
		if _, statErr := os.Stat(target); statErr == nil {
			return struct{}{}, nil
		}
		if err := os.MkdirAll(inboxDir, 0o755); err != nil {
			return struct{}{}, err
		}
		routeRec := gdl.Record{Type: "route", Fields: []gdl.RecordField{
			{Key: "to", Value: recipient},
			{Key: "from", Value: author},
			{Key: "ts", Value: routeTS},
		}}
		contents := thoughtLine + "\n" + gdl.RenderLine(routeRec) + "\n"
		tmp := target + ".tmp"
		// Best-effort cleanup so a failed WriteFile/Rename never strands
		// <thought-id>.gdl.tmp under live/inbox/ (#141). Success path:
		// Rename already moved tmp, so this Remove is a no-op. The
		// double-checked idempotency skip above is unchanged.
		defer func() { _ = os.Remove(tmp) }()
		if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, os.Rename(tmp, target)
	})
	return err
}
