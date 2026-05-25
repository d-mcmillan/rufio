// H1b — relative-time and short-id helpers for text-mode rendering.
//
// RFC3339Nano ate 27 cols on every text row; 20-char ids ate another
// chunk. Combined: ~40 cols per row reclaimed when both go. JSON mode
// keeps the full precision (machines need it); humans get the friendly
// form.
package output

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// RenderRelTime turns an RFC3339Nano timestamp into a relative-from-now
// phrase suitable for the leading column of a row.
//
// Output shapes:
//
//   - "now"          (delta < 1s)
//   - "Ns ago"       (1s ≤ delta < 60s)
//   - "Nm ago"       (1m ≤ delta < 60m)
//   - "Nh ago"       (1h ≤ delta < 24h)
//   - "Nd ago"       (1d ≤ delta ≤ 7d)
//   - "YYYY-MM-DD"   (> 7d OR future-stamped OR unparseable)
//
// The >7d fallthrough exists because at that scale, the absolute date is
// easier to scan than "9d ago" — also matches how humans naturally
// describe long-past events.
//
// Future-stamped rows (clock skew, races between writers) collapse to
// the date form rather than inventing a "-Ns ago" (or "in N seconds"):
// the column-width math assumes a monotone relative-past phrase, and a
// future stamp on a "past records" view is itself a signal worth
// surfacing the bare date for.
//
// Empty / unparseable input returns the raw string — never panics. The
// row still renders SOMETHING readable, and the operator sees the
// malformed value directly.
func RenderRelTime(ts string, now time.Time) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Try a couple of forgiving fallbacks: RFC3339 with no nano
		// fraction, and the bare date form already emitted by ScanGiven.
		if t2, e2 := time.Parse(time.RFC3339, ts); e2 == nil {
			t = t2
		} else if t3, e3 := time.Parse("2006-01-02", ts); e3 == nil {
			t = t3
		} else {
			return ts
		}
	}
	delta := now.Sub(t)
	if delta < 0 {
		// Future-stamped: collapse to the date form.
		return t.UTC().Format("2006-01-02")
	}
	if delta < time.Second {
		return "now"
	}
	if delta < time.Minute {
		return fmt.Sprintf("%ds ago", int(delta.Seconds()))
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	}
	days := int(delta / (24 * time.Hour))
	if days <= 7 {
		return fmt.Sprintf("%dd ago", days)
	}
	return t.UTC().Format("2006-01-02")
}

// ShortID returns the last 6 chars of a rufio id. IDs are
// `<unix-millis>-<rand6>` (thought.GenerateID) so the suffix is the
// random component — the bit that disambiguates IDs minted in the same
// millisecond. Six chars is enough disambiguation at corpus scale (we
// emit 30+ bits of entropy in that suffix).
//
// Edge cases:
//
//   - empty input          → empty output (never panic)
//   - len(id) <= 6         → return as-is (don't pad, don't truncate
//     past the start)
//   - len(id) >  6         → last 6 bytes
//
// All UTF-8 considerations are intentionally ignored — the id format is
// ASCII by design (writer enforces [a-z0-9-]).
func ShortID(id string) string {
	if len(id) <= 6 {
		return id
	}
	return id[len(id)-6:]
}

// UseFullIDs reports whether the caller has opted into full-form ids in
// text mode (e.g. for piping to scripts that consume the wire format).
// Today the opt-in is RUFIO_FULL_IDS=1 — kept env-driven so every read
// command picks it up without each one re-declaring the flag.
//
// We check for ANY non-empty value (matching the NO_COLOR convention)
// so RUFIO_FULL_IDS=1 / =true / =yes all work without a parser.
func UseFullIDs() bool {
	v := strings.TrimSpace(os.Getenv("RUFIO_FULL_IDS"))
	return v != "" && v != "0" && strings.ToLower(v) != "false"
}

// FormatID is the convenience wrapper that picks short vs full based on
// the UseFullIDs() env signal. Returns the id unchanged when it is
// shorter than the short-form length (degenerate / non-rufio ids stay
// intact). The Dim wrapper is NOT applied here — callers compose colour
// on top of FormatID at the row-render site.
func FormatID(id string) string {
	if UseFullIDs() {
		return id
	}
	return ShortID(id)
}
