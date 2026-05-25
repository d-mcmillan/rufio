// Package banner — tests for version-prefix handling. Pins the no-double-v
// invariant for the version line so a release-time ldflag injection of
// `v1.0.6` doesn't render `vv1.0.6` (m1 / pre-public polish bundle).
package banner

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// TestNormaliseVersion_StripsLeadingV pins the defensive strip so the
// banner's literal "v" prefix never doubles up when callers pass a
// version that already starts with "v".
func TestNormaliseVersion_StripsLeadingV(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v1.0.6", "1.0.6"},
		{"1.0.6", "1.0.6"},
		{"dev", "dev"},
		{"v0.1.0", "0.1.0"},
		{"", ""},
		// Strip only ONE leading v — not "vv1.0.6" defensively, because that
		// case shouldn't exist in the wild and stripping multiples would
		// mask a genuine bug.
		{"vv1.0.6", "v1.0.6"},
	}
	for _, c := range cases {
		got := normaliseVersion(c.in)
		if got != c.want {
			t.Errorf("normaliseVersion(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// what fn wrote to stdout. We exercise Print/PrintCompact directly so
// the test pins the end-to-end render — format string + helper + caller
// convention — not just the helper in isolation.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	<-done
	os.Stdout = orig
	return buf.String()
}

// TestPrint_VersionLine_NoDoubleVPrefix is the regression guard for m1.
// Pre-fix, the format string `"  %sv%s  ·  rufio.ai%s\n"` ran against a
// version that release builds ldflag in as `v1.0.6`, producing `vv1.0.6`.
// The fix strips a single leading "v" defensively in normaliseVersion so
// the rendered output has exactly ONE "v" prefix regardless of input.
func TestPrint_VersionLine_NoDoubleVPrefix(t *testing.T) {
	// NO_COLOR + redirected stdout pushes Print into the modeNone branch,
	// which uses the same `v%s` format string for the version line — that
	// branch is testable without ANSI noise.
	t.Setenv("NO_COLOR", "1")

	cases := []struct {
		name    string
		version string
		want    string
	}{
		{"release-tag-with-v-prefix", "v1.0.6", "v1.0.6"},
		{"plain-version-no-v", "1.0.6", "v1.0.6"},
		{"dev-build", "dev", "vdev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				Print(Options{Version: c.version, ShowVersion: true})
			})
			// Exactly one "v" before the version number / label.
			if !strings.Contains(out, c.want+"  ·  rufio.ai") {
				t.Errorf("banner output missing %q; got:\n%s", c.want, out)
			}
			// No "vv" anywhere on the version line.
			if strings.Contains(out, "vv") {
				t.Errorf("banner output has double-v prefix; got:\n%s", out)
			}
		})
	}
}

// TestPrintCompact_VersionLine_NoDoubleVPrefix mirrors the Print test
// for the one-line variant used by `rufio dev`. Same defect, same fix.
func TestPrintCompact_VersionLine_NoDoubleVPrefix(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	out := captureStdout(t, func() {
		PrintCompact(Options{Version: "v1.0.6"})
	})
	if !strings.Contains(out, "rufio v1.0.6 ·") {
		t.Errorf("compact banner missing single-v version; got:\n%s", out)
	}
	if strings.Contains(out, "vv1.0.6") {
		t.Errorf("compact banner has double-v prefix; got:\n%s", out)
	}
}
