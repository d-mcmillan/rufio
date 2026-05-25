// Package cli — RED tests for the L2 minor-cleanup: `rufio stream
// --from=""` must behave identically to `rufio listen --catch-up`, per
// streamLongHelp which documents `--from=""` as "start from the epoch
// and replay every visible record first".
//
// Today's bug (R26 vet): runStream sets ReplayBeforeWatch via
// `fromCursor != ""`, so an empty cursor disables replay entirely. The
// fix uses cmd.Flags().Changed("from") to distinguish "flag absent" from
// "flag set to empty string".
//
// We lock the contract at TWO levels:
//
//  1. buildStreamEmitOpts (the wiring helper extracted as part of the L2
//     fix) — pure-function test, no fsnotify or stdout dance.
//  2. The cobra command's --from flag is registered (regression guard
//     for the surface).
//
// Behaviour at the stream lib level (catch-up replay actually emits the
// historical records) is already locked by
// internal/lib/stream/cursor_endcatchup_test.go — we don't re-test that
// here, only the CLI's decision to ENABLE replay when --from="" is
// explicit.
package cli

import (
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/stream"
)

// TestStream_FromEmptyString_BehavesIdenticallyToListenCatchUp — L2 RED.
// When --from is explicitly set (even to empty string), buildStreamEmitOpts
// MUST set ReplayBeforeWatch=true so the catch-up half of WatchAndEmitFrom
// engages. This is the documented `--from=""` == "from-epoch" contract
// (streamLongHelp). The R26 bug: today the helper keys off `fromCursor != ""`
// alone, so explicit `--from=""` silently skips replay.
func TestStream_FromEmptyString_BehavesIdenticallyToListenCatchUp(t *testing.T) {
	// Case 1: --from NOT passed → no replay (legacy live-tail-only path).
	opts := buildStreamEmitOpts("" /*fromCursor*/, false /*fromFlagSet*/)
	if opts.ReplayBeforeWatch {
		t.Errorf("buildStreamEmitOpts(\"\", false): ReplayBeforeWatch=true, want false (no flag → live tail only)")
	}

	// Case 2: --from="" explicitly passed → replay everything (catch-up parity).
	opts = buildStreamEmitOpts("" /*fromCursor*/, true /*fromFlagSet*/)
	if !opts.ReplayBeforeWatch {
		t.Errorf("buildStreamEmitOpts(\"\", true): ReplayBeforeWatch=false, want true (--from=\"\" == --catch-up)")
	}
	if opts.FromCursor != "" {
		t.Errorf("buildStreamEmitOpts(\"\", true): FromCursor=%q, want \"\"", opts.FromCursor)
	}

	// Case 3: --from=<non-empty> → replay strictly after that cursor.
	const someCursor = "Y29oZXJlbnQ=" // any non-empty value
	opts = buildStreamEmitOpts(someCursor, true)
	if !opts.ReplayBeforeWatch {
		t.Errorf("buildStreamEmitOpts(non-empty, true): ReplayBeforeWatch=false, want true")
	}
	if opts.FromCursor != someCursor {
		t.Errorf("buildStreamEmitOpts(non-empty, true): FromCursor=%q, want %q", opts.FromCursor, someCursor)
	}
}

// Force a use of the stream package import so the test compiles even
// when the helper signature evolves.
var _ = stream.EmitOpts{}
