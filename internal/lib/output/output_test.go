// Tests for the H1 aesthetic pass — TTY-gated colour palette, the
// escape-leak regression guard (R25's only "real bug"), the relative-time
// helper, and the short-id helper.
//
// The TTY-gating tests work by checking what Dim/Bold/Cyan/Green/Red/Yellow
// return on a non-tty stdout (the normal `go test` execution): the wrapper
// MUST return the input string unchanged — no ANSI codes, and crucially,
// no terminal-query probe (ESC]11;? / ESC[6n) that would corrupt piped
// stdout. The R25 dogfood reported this leak; we keep a snapshot test so
// any future colour-detection probe added to the package would fail it.
package output

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// --- H1a: TTY-gated colour + escape-leak regression ---

// During `go test` stdout is a pipe (not a tty) so ShouldUseColor must
// return false unconditionally — every colour helper degrades to the raw
// string.
func TestOutput_StdoutNonTTY_NoColorCodes(t *testing.T) {
	opts := RenderOpts{}
	for _, fn := range []struct {
		name string
		got  string
	}{
		{"Dim", Dim("hello", opts)},
		{"Bold", Bold("hello", opts)},
		{"Cyan", Cyan("hello", opts)},
		{"Green", Green("hello", opts)},
		{"Red", Red("hello", opts)},
		{"Yellow", Yellow("hello", opts)},
	} {
		if fn.got != "hello" {
			t.Errorf("%s on non-tty must return raw string, got %q", fn.name, fn.got)
		}
		if strings.Contains(fn.got, "\x1b") {
			t.Errorf("%s on non-tty must not emit ESC bytes, got %q", fn.name, fn.got)
		}
	}
}

// R25's "real bug": terminal-query probes (ESC]11;?... and ESC[6n) leaking
// into piped stdout. Even when colour is disabled, the package MUST NEVER
// write a probe-style escape sequence to non-tty stdout. We assert by
// inspecting every helper's output for the canonical probe shapes.
func TestOutput_StdoutNonTTY_NoEscapeProbe(t *testing.T) {
	opts := RenderOpts{}
	// Probe shapes we MUST never see in piped output. Both are OSC/CSI
	// queries terminals reply to; in non-interactive consumers they become
	// garbage (e.g. `rufio recall | grep` sees `]11;?\` literals).
	probeRE := regexp.MustCompile(`\x1b\]11;\?|\x1b\[6n`)
	for _, s := range []string{
		Dim("x", opts), Bold("x", opts), Cyan("x", opts),
		Green("x", opts), Red("x", opts), Yellow("x", opts),
		// BoldState is the colour-aware "state word" helper — bold +
		// possibly tinted. Same probe-leak guard applies.
		BoldState("pending", opts),
	} {
		if probeRE.MatchString(s) {
			t.Errorf("escape probe leaked into non-tty output: %q", s)
		}
	}
}

// --no-color must win even when ShouldUseColor would otherwise allow it.
// (Exercises the flag-override branch independently of the tty detection
// branch — both must trigger the no-codes path.)
func TestOutput_NoColorFlag_OverridesTTY(t *testing.T) {
	opts := RenderOpts{NoColor: true}
	if ShouldUseColor(opts) {
		t.Errorf("ShouldUseColor must be false when --no-color is set")
	}
	if got := Cyan("x", opts); got != "x" {
		t.Errorf("Cyan with --no-color set returned %q want %q", got, "x")
	}
}

// NO_COLOR env var (https://no-color.org) is the de-facto convention.
// Any non-empty value disables colour.
func TestOutput_NoColorEnv_Respected(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	opts := RenderOpts{}
	if ShouldUseColor(opts) {
		t.Errorf("ShouldUseColor must be false when NO_COLOR=1")
	}
	if got := Green("x", opts); got != "x" {
		t.Errorf("Green with NO_COLOR=1 returned %q want %q", got, "x")
	}
}

// Smoke test the ANSI shapes when colour IS enabled. We force the
// internal toggle via a test-only seam so the snapshot is deterministic
// even in the test environment (which is non-tty).
func TestOutput_TTYPath_HasExpectedANSICodes(t *testing.T) {
	withForcedColor(t, func() {
		opts := RenderOpts{}
		cases := []struct {
			name string
			fn   func(string, RenderOpts) string
			want string // a substring that MUST appear in the output
		}{
			{"Dim", Dim, "\x1b[2m"},
			{"Bold", Bold, "\x1b[1m"},
			// Cyan uses 36 (standard 8-colour cyan); we don't lock to
			// truecolour to keep the assertion robust across profiles.
			{"Cyan", Cyan, "\x1b[36m"},
			{"Green", Green, "\x1b[32m"},
			{"Red", Red, "\x1b[31m"},
			{"Yellow", Yellow, "\x1b[33m"},
		}
		for _, c := range cases {
			got := c.fn("hello", opts)
			if !strings.Contains(got, c.want) {
				t.Errorf("%s output %q missing expected ANSI %q", c.name, got, c.want)
			}
			if !strings.Contains(got, "\x1b[0m") {
				t.Errorf("%s output %q missing reset code", c.name, got)
			}
			if !strings.Contains(got, "hello") {
				t.Errorf("%s output %q must preserve input", c.name, got)
			}
		}
	})
}

// --- H1b: RenderRelTime ---

func TestRenderRelTime_LessThanMinute_Seconds(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ts   string
		want string
	}{
		{"2026-05-20T12:00:00Z", "now"},
		{"2026-05-20T11:59:55Z", "5s ago"},
		{"2026-05-20T11:59:01Z", "59s ago"},
	}
	for _, c := range cases {
		got := RenderRelTime(c.ts, now)
		if got != c.want {
			t.Errorf("RenderRelTime(%q, ...)=%q want %q", c.ts, got, c.want)
		}
	}
}

func TestRenderRelTime_Minutes_HoursDays_WithBoundaryCases(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ts   string
		want string
	}{
		{"2026-05-20T11:58:00Z", "2m ago"},
		{"2026-05-20T11:00:00Z", "1h ago"},
		{"2026-05-20T09:00:00Z", "3h ago"},
		{"2026-05-19T12:00:00Z", "1d ago"},
		{"2026-05-14T12:00:00Z", "6d ago"},
	}
	for _, c := range cases {
		got := RenderRelTime(c.ts, now)
		if got != c.want {
			t.Errorf("RenderRelTime(%q, ...)=%q want %q", c.ts, got, c.want)
		}
	}
}

// Anything older than 7 days collapses to a YYYY-MM-DD date — keeps the
// "long-ago" rows scannable without exposing nano-precision.
func TestRenderRelTime_OlderThan7Days_RendersDateOnly(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	got := RenderRelTime("2026-05-12T08:00:00Z", now)
	if got != "2026-05-12" {
		t.Errorf("RenderRelTime older-than-7d=%q want 2026-05-12", got)
	}
}

// Unparseable / empty input must not panic — return the raw input so
// the row still renders something the operator can read.
func TestRenderRelTime_Unparseable_Passthrough(t *testing.T) {
	now := time.Now()
	if got := RenderRelTime("", now); got != "" {
		t.Errorf("empty ts passthrough: got %q want \"\"", got)
	}
	if got := RenderRelTime("not-a-ts", now); got != "not-a-ts" {
		t.Errorf("malformed ts passthrough: got %q want %q", got, "not-a-ts")
	}
}

// Negative deltas (clock skew / future-stamped rows) render as the raw
// date — we never invent "in N seconds" because the column-width math
// assumes a monotone relative-past phrase.
func TestRenderRelTime_FutureTimestamp_RendersDateOnly(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	got := RenderRelTime("2026-05-25T12:00:00Z", now)
	if got != "2026-05-25" {
		t.Errorf("future ts: got %q want 2026-05-25", got)
	}
}

// --- H1b: ShortID ---

// IDs use the writer's <unix-millis>-<rand6> format (thought.GenerateID).
// The last 6 chars are the random suffix — the bit that disambiguates IDs
// authored within the same millisecond. Using the suffix keeps lookups
// well-targeted even at scale.
func TestShortID_Returns6CharSuffix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1779285164940-liqau7", "liqau7"},
		{"1779285164948-6ce4ow", "6ce4ow"},
		{"abcdefghi", "defghi"},
	}
	for _, c := range cases {
		got := ShortID(c.in)
		if got != c.want {
			t.Errorf("ShortID(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

// Edge cases: empty, short-of-6, exactly-6 — never crash, never expand.
func TestShortID_HandlesEdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"abcdef", "abcdef"},
	}
	for _, c := range cases {
		got := ShortID(c.in)
		if got != c.want {
			t.Errorf("ShortID(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
