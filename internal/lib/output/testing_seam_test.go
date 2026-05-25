// Test-only helpers for the H1 output tests.
package output

import "testing"

// withForcedColor flips the test-only forceColorForTesting flag on for
// the duration of fn, then restores it. Used by the TTY-path ANSI shape
// assertions which would otherwise see a non-tty stdout under `go test`.
func withForcedColor(t *testing.T, fn func()) {
	t.Helper()
	prev := forceColorForTesting
	forceColorForTesting = true
	t.Cleanup(func() { forceColorForTesting = prev })
	fn()
}
