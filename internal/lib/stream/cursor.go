package stream

// Cursor surface for the streaming CLI (`rufio listen` / `rufio stream`)
// — shares the SAME opaque (canonicalTS,path) wire format as the MCP
// `Poll` tool so an SDK consumer can hand a value from either surface
// back to the other without parsing or reformatting.
//
// Public symbols here are thin re-exports of poll.go's unexported
// helpers. The MCP tool keeps using the internal lowercase names; the
// streaming CLI must call the uppercase ones. Both reduce to the same
// base64(canonicalTS + "\x00" + path) byte sequence — no parallel
// encoder, no drift risk.

import (
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// CursorRecord is the JSONL wire shape periodic next_cursor emission
// uses on stdout. It is deliberately distinct from stream.Event (the
// "_type":"cursor" tag disambiguates) so consumers can route on a
// single field. Value is the opaque pass-back token; TS is the
// canonical-TS hint for telemetry/logging only (consumers MUST NOT
// parse Value to derive a time).
type CursorRecord struct {
	Type  string `json:"_type"` // always "cursor"
	Value string `json:"value"` // opaque base64(canonicalTS\x00path)
	TS    string `json:"ts"`    // canonical TS of the last emitted event (hint only)
}

// EncodeCursor produces the opaque resume token for (canonicalTS, path).
// CALLERS MUST PASS THE CANONICAL ts (versioning.CanonicalTS): the raw
// NowISO RFC3339Nano form is variable-width and NOT lexically
// chronological. See poll.go's encodeCursor for the full rationale.
//
// CursorOf is the convenience shim for "give me the cursor that points
// at this event"; prefer CursorOf in caller code so the canonicalisation
// stays internal.
func EncodeCursor(canonicalTS, path string) string {
	return encodeCursor(canonicalTS, path)
}

// DecodeCursor is the inverse of EncodeCursor. An empty cursor decodes
// to ("", "", nil) — the "from-the-beginning" sentinel. A structurally
// invalid cursor returns fmt.Errorf("invalid cursor"). Re-exports
// poll.go's decodeCursor unchanged so the MCP and CLI surfaces agree
// on what counts as malformed.
func DecodeCursor(c string) (canonicalTS, path string, err error) {
	return decodeCursor(c)
}

// CursorOf returns the opaque cursor token pointing AT (not after) the
// given event. Pass this back to EmitCatchUpFrom / WatchAndEmitFrom /
// Poll as the FromCursor / cursor argument; those callers treat it as
// "resume strictly AFTER this event".
//
// Internal canonicalisation: the event's raw TS is normalised to the
// canonical fixed-9-digit form before encoding so a same-second event
// with variable-width fractions can't be silently mis-ordered across a
// page boundary (the C1 regression that lives on poll.go).
func CursorOf(ev Event) string {
	return encodeCursor(versioning.CanonicalTS(ev.TS), ev.Path)
}
