// Package output provides TTY-aware writers and the strict --quiet rule
// invariants. Mirrors src/lib/output.ts.
//
// The contract (locked by week-1 reviews):
//   - WriteOut: chatter; suppressed by --quiet.
//   - WriteData: primary output (rows, diff text, daemon events); never
//     suppressed by --quiet. Same logic as WriteJSONL — data is data.
//   - WriteErr: stderr; never suppressed.
//   - WriteJSONL: JSON-encoded line on stdout; never suppressed by --quiet
//     (--json wins over --quiet, matches gh/aws/curl convention).
//
// # H1 aesthetic pass (R25, v1.1)
//
// The package gained a subtle, TTY-gated colour palette and two
// text-mode rendering helpers (RenderRelTime, ShortID) as part of the
// "second nature" round closing the gap between R23 frictionless and R24
// noticings. The palette degrades gracefully:
//
//   - non-tty stdout (`rufio recall | grep`) → raw text, ZERO escapes,
//     ZERO terminal-query probes. R25 flagged the latter as the only
//     "real bug" — a colour-detection probe corrupting piped consumers.
//   - --no-color flag set → raw text.
//   - NO_COLOR env var non-empty → raw text (https://no-color.org).
//   - otherwise → ANSI codes.
//
// Test-only seam: forceColorForTesting lets the test suite assert the
// ANSI shapes deterministically even though `go test` itself sees a
// non-tty stdout. Production code MUST NOT set this.
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RenderOpts mirrors the TS RenderOpts. Used by every command's parseArgs.
type RenderOpts struct {
	JSON    bool
	Quiet   bool
	NoColor bool
}

// forceColorForTesting is a test-only seam. When true, ShouldUseColor
// returns true regardless of tty/--no-color/NO_COLOR. Toggled via
// withForcedColor in the test suite; NEVER set from production code.
var forceColorForTesting bool

// IsTTY reports whether stdout is connected to a terminal.
//
// This is the load-bearing gate for H1a — every colour-emitting code path
// goes through here (via ShouldUseColor). Crucially, the package NEVER
// runs a terminal-query probe (ESC]11;? / ESC[6n) to "detect" the
// background; the probe-shape escapes are what leaked into piped
// consumers in R25. We rely solely on the cheap, side-effect-free
// os.Stdout.Stat() ModeCharDevice check.
func IsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ShouldUseColor honours --no-color, NO_COLOR env, and TTY detection.
// Returns false on ANY of those signals — colour is opt-in, never
// forced, never probed.
func ShouldUseColor(opts RenderOpts) bool {
	if forceColorForTesting {
		return true
	}
	if opts.NoColor || os.Getenv("NO_COLOR") != "" || !IsTTY() {
		return false
	}
	return true
}

// WriteOut writes a chatter line. Suppressed by --quiet (the rule that
// distinguishes chatter from data).
func WriteOut(line string, opts RenderOpts) {
	if opts.Quiet {
		return
	}
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	fmt.Fprint(os.Stdout, line)
}

// WriteData writes primary command output. Never suppressed by --quiet.
// Use this for rows (history), diff text, daemon events, blob bytes —
// anything the user explicitly asked for.
//
// The opts parameter is reserved for future formatting flags (--no-color,
// --json) but quiet is intentionally ignored. Same logic as WriteJSONL.
func WriteData(line string, opts RenderOpts) {
	_ = opts
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	fmt.Fprint(os.Stdout, line)
}

// WriteErr writes to stderr. Always; never suppressed.
func WriteErr(line string) {
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	fmt.Fprint(os.Stderr, line)
}

// WriteJSONL emits a single JSON-encoded line on stdout. Ignores --quiet
// per the "--json wins over --quiet" rule (locked in week-1 Phase 3 review).
func WriteJSONL(obj interface{}, opts RenderOpts) error {
	_ = opts
	bs, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%s\n", bs)
	return nil
}

// wrap is the shared ANSI wrapper. Every colour helper goes through it
// so the "no probe, no codes on non-tty" rule has exactly one
// enforcement point. The raw codes (\x1b[Nm) are the standard 8-colour
// SGR set — robust across every terminal that emits ANSI at all.
func wrap(s, code string, opts RenderOpts) string {
	if !ShouldUseColor(opts) {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Dim renders s faint, only when colour is enabled. Used for the de-
// emphasised columns (timestamps, paths, full ids) that the eye should
// glide past.
func Dim(s string, opts RenderOpts) string { return wrap(s, "2", opts) }

// Bold renders s bold, only when colour is enabled.
func Bold(s string, opts RenderOpts) string { return wrap(s, "1", opts) }

// Cyan renders s in standard cyan (36). Used for short IDs in text mode
// — the colour is bright enough to spot, muted enough not to dominate.
func Cyan(s string, opts RenderOpts) string { return wrap(s, "36", opts) }

// Green renders s in standard green (32). Used for confirmation markers.
func Green(s string, opts RenderOpts) string { return wrap(s, "32", opts) }

// Red renders s in standard red (31). Used for refutation markers and
// the [RETRACTED] prefix.
func Red(s string, opts RenderOpts) string { return wrap(s, "31", opts) }

// Yellow renders s in standard yellow (33). Used to highlight the
// agent's OWN records — helps visually find "what did I write" in a
// long fleet feed.
func Yellow(s string, opts RenderOpts) string { return wrap(s, "33", opts) }

// BoldState renders a state word (pending / active / completed /
// abandoned / closed / [RETRACTED]) in bold + colour. The colour is
// chosen from the word itself so the column is self-explanatory:
//
//   - pending → yellow bold
//   - active / open → green bold
//   - completed → cyan bold
//   - abandoned / closed / [RETRACTED] → red bold
//   - anything else → bold (no colour tint)
//
// The wrap is single-pass so the reset code at the end clears BOTH the
// bold and colour layers — no orphaned escape leaks across rows.
func BoldState(s string, opts RenderOpts) string {
	if !ShouldUseColor(opts) {
		return s
	}
	// Stripped form for the switch — "[RETRACTED]" matches but so does
	// the bare word. Keep the comparison case-sensitive (writers emit
	// lowercase per the writer convention) to avoid accidentally
	// colouring user input that happens to share a state word.
	colour := ""
	switch s {
	case "pending":
		colour = "33" // yellow
	case "active", "open":
		colour = "32" // green
	case "completed":
		colour = "36" // cyan
	case "abandoned", "closed", "[RETRACTED]":
		colour = "31" // red
	}
	if colour == "" {
		return "\x1b[1m" + s + "\x1b[0m"
	}
	return "\x1b[1;" + colour + "m" + s + "\x1b[0m"
}
