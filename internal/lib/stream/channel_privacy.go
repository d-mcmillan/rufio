package stream

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
)

// passesAll is the single visibility predicate every stream entry
// point routes through. It composes Match (existing type/scope/
// scope:agent privacy) with channelMembershipVisible (the channel-
// privacy fix). Callers MUST pass the same metaCache across a single
// EmitCatchUp / Poll / WatchAndEmit invocation so a channel's meta
// is read at most once per call.
//
// Use this instead of Match in any new stream code; existing Match
// call sites in this package have all been migrated.
func passesAll(root string, ev Event, p FilterParams, cache *metaCache) bool {
	if !Match(ev, p) {
		return false
	}
	return channelMembershipVisible(root, ev, p.CurrentAgent, cache)
}

// channelMembershipVisible enforces the channel-membership floor on
// channel-message events. Channels are 2-party (opener + target);
// only those two identities are entitled to read messages on the
// channel — even after one has left and the channel has been closed
// (the audit trail surfaces via channels.IsEverMember).
//
// Audit (post-v1.0.5 channel-privacy regression): the v1.0.5 listen
// surface walked live/channels/active/*/messages/ and applied only
// scope-based privacy. channel-message records carry no `scope`
// field — visibility is channel-membership, not scope. Result: any
// authenticated identity could read every channel's messages via
// `rufio listen --types=channel-message`. The fix is this predicate,
// called alongside Match in every stream entry point.
//
// Semantics:
//   - Non-channel-message events: always visible (not our concern).
//   - currentAgent == "" (anonymous local stdio / firehose mode):
//     always visible — matches the existing Match convention where
//     anonymous = firehose. The CLI listen path always resolves a
//     real identity (no anonymous --as for listen by design); the
//     anonymous case only fires in tests, in `rufio stream`, and in
//     admin tooling that bypasses identity resolution.
//   - Member (opener OR target via channels.IsEverMember): visible.
//   - Non-member: hidden.
//
// Failure modes (closing membership-check failures conservatively):
//   - Cannot derive channel id from path → hide. A path that doesn't
//     match the canonical layout is either a synthetic test record
//     or a malformed substrate; either way we don't have a
//     membership predicate to consult.
//   - Cannot load channel meta (missing / corrupt) → hide. Better to
//     drop a single event than to leak it; the catch-up cursor still
//     advances past the offending record.
//
// Performance: we cache loaded meta per-call-graph via metaCache so a
// channel with 1000 messages doesn't pay 1000 disk reads on
// EmitCatchUp. The cache is scoped to one call only (passed via the
// stream call site) — the channels package's read API doesn't memoise
// and a long-lived cache here would race with the cli's accept/close
// writes.
func channelMembershipVisible(root string, ev Event, currentAgent string, cache *metaCache) bool {
	if ev.Type != "channel-message" {
		return true
	}
	if currentAgent == "" {
		// Anonymous firehose — preserved for tests + stdio mode.
		// Matches the existing Match convention.
		return true
	}
	chID := channelIDFromPath(ev.Path)
	if chID == "" {
		// Path doesn't match canonical layout — hide
		// conservatively.
		return false
	}
	meta, ok := cache.load(root, chID)
	if !ok {
		// Couldn't load meta — hide conservatively.
		return false
	}
	return meta.IsEverMember(currentAgent)
}

// channelIDFromPath extracts the channel id from a canonical
// channel-message path. Layout:
//
//	live/channels/active/<chan-id>/messages/<msg-id>.gdl
//	live/channels/closed/<chan-id>/messages/<msg-id>.gdl
//
// Returns "" for any path that doesn't match either layout. Defensive
// against malformed paths from a malicious mirror or test fixture.
func channelIDFromPath(path string) string {
	// Canonical wire form uses forward slashes. The path is
	// project-root-relative (set by EmitCatchUp's filepath.ToSlash
	// rel), so it always starts with the live/ segment.
	posix := filepath.ToSlash(path)
	const prefixActive = "live/channels/active/"
	const prefixClosed = "live/channels/closed/"
	var rest string
	switch {
	case strings.HasPrefix(posix, prefixActive):
		rest = strings.TrimPrefix(posix, prefixActive)
	case strings.HasPrefix(posix, prefixClosed):
		rest = strings.TrimPrefix(posix, prefixClosed)
	default:
		return ""
	}
	// rest is now `<chan-id>/messages/<msg-id>.gdl` — the first
	// segment is the channel id. Defense in depth: reject anything
	// that doesn't have the expected `<id>/messages/` structure.
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 3 || parts[1] != "messages" || parts[0] == "" {
		return ""
	}
	return parts[0]
}

// metaCache memoises channels.LoadMeta lookups across a single
// stream call (EmitCatchUp / Poll / WatchAndEmit). Scoped to one
// call so we don't race with concurrent accept/close writes. The
// sentinel `loaded` bool distinguishes "looked up + missing" from
// "not yet looked up" — the former returns false from load() and
// blocks future leak attempts; the latter triggers an actual disk
// read.
type metaCache struct {
	mu sync.Mutex
	// cells[chID] is the cached channel meta. Keys appear ONLY on
	// successful loads — failures are not cached so a transiently-
	// missing meta (mirror-sync arrival race) is retried on the next
	// event. See load() doc for the race rationale.
	cells map[string]*channels.Channel
}

func newMetaCache() *metaCache {
	return &metaCache{cells: make(map[string]*channels.Channel)}
}

// load returns the cached channel meta for chID, loading from disk
// on first lookup. The second return is false when the channel meta
// can't be loaded — caller treats this as "no membership info, hide."
//
// POSITIVE-ONLY caching: a successful load is memoised; a load failure
// is NOT cached. The reason is the mirror-sync arrival-ordering race:
// a `messages/<id>.gdl` event can land before `meta.gdl` is visible
// (the remote daemon writes them in order but the SSE consumer +
// fsnotify ordering on the receiving end can interleave). Pre-fix, a
// load failure inserted a `nil` sentinel that hid every subsequent
// event for that chID — including the case where meta.gdl became
// readable seconds later. cursor_emit.go::WatchAndEmitFrom shares one
// cache for the entire watch lifetime; a single transient miss would
// permanently lose channel visibility for the listener.
//
// The cost of not negative-caching is one disk stat per event when the
// chID has no meta yet. That is bounded by upstream's event rate and
// is well under the cost of the SSE round-trip already in flight.
func (c *metaCache) load(root, chID string) (channels.Channel, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cell, seen := c.cells[chID]; seen {
		return *cell, true
	}
	meta, err := channels.LoadMeta(root, chID)
	if err != nil {
		return channels.Channel{}, false
	}
	c.cells[chID] = &meta
	return meta, true
}
