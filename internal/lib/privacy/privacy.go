// Package privacy is the shared scope:agent visibility + authorization
// predicate, lifted from the stream.Match privacy gate (#139) into a
// package-level helper so every read+write surface enforces the same
// rule. #147 surfaced 4 leaks where stream.Match was not on the path:
// goals list, recall, fleet, confirm/refute.
//
// The predicate is intentionally tiny and duck-typed via a small Record
// interface so different on-disk record types (Thought, Observation,
// Goal, Attention, …) can be passed without forcing a common concrete
// type. Each caller wraps its own struct to satisfy Record — see the
// applications in internal/lib/goal/ReadAllVisible,
// internal/lib/recall/Filter, internal/cli/fleet.go, and
// internal/cli/confirm.go + refute.go.
//
// Anonymous-caller semantics (currentAgent="") preserve the firehose
// path — every record is visible, mirroring the stream.Match opt-in
// rule. This keeps `rufio stream` and admin/test paths unaffected when
// no identity is supplied.
package privacy

// Record is the minimal interface a value must satisfy to flow through
// the privacy predicate. Two fields are load-bearing: the scope of the
// record (the privacy boundary) and its author (the only agent the
// boundary admits). Everything else is a caller concern.
type Record interface {
	GetScope() string
	GetAuthor() string
}

// IsVisible returns true if record should be visible to currentAgent.
//
// Rule: scope:agent records authored by a different agent are NEVER
// visible. All other scopes (deployment, fleet, "") follow their
// existing caller-controlled rules — IsVisible does not gate them.
//
// Anonymous firehose: currentAgent="" returns true unconditionally.
// This matches the stream.Match opt-in semantic — only an identified
// caller asks for privacy filtering; the unidentified path (e.g.
// `rufio stream` from a coordinator, admin/test paths) preserves
// pre-#139 firehose behaviour.
func IsVisible(r Record, currentAgent string) bool {
	if currentAgent == "" {
		return true
	}
	if r.GetScope() == "agent" && r.GetAuthor() != currentAgent {
		return false
	}
	return true
}

// CanWriteAgainst returns true when currentAgent is allowed to write a
// social-validation record (confirm, refute, …) against target.
//
// Rule: scope:agent records are non-author-writeable — only the author
// can confirm/refute their own private thought. Crowd validation
// continues to work for scope:deployment and scope:fleet, the records
// that already carry an explicit "this is open to peers" semantic.
//
// Empty currentAgent is a degenerate case — write paths always resolve
// an identity before calling, but for symmetry with IsVisible the empty
// case here is treated permissively. Callers are responsible for the
// identity-required check upstream (identity.Resolve) so this branch
// is unreachable in production.
func CanWriteAgainst(target Record, currentAgent string) bool {
	if currentAgent == "" {
		return true
	}
	if target.GetScope() == "agent" && target.GetAuthor() != currentAgent {
		return false
	}
	return true
}
