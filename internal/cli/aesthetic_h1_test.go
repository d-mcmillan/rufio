// Package cli — H1 aesthetic-pass CLI tests (R25 v1.1 craft round).
//
// These exercise the full end-to-end render path of `thoughts list` and
// `recall` so we know the renderer wires the new RelTime + ShortID helpers
// AND that --json keeps the full RFC3339Nano + full ids intact.
//
// Pattern: seed a tempdir with a few hand-written *.gdl files, set the
// agent identity via env, run the relevant `runX` function under
// captureStdout, parse the output, and assert.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// h1Corpus seeds a minimal substrate with one thought and one observation,
// authored within the last few minutes so the RelTime renderer produces a
// human "Ns ago" / "Nm ago" string (NOT a fall-through to a YYYY-MM-DD).
//
// Returns the corpus root so the caller can pass it as cwd.
func h1Corpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// rufio.gdl marks the project root so paths.FindProjectRoot succeeds.
	mustWrite(t, filepath.Join(root, "rufio.gdl"), "@project name:test\n")
	mustMkdir(t, filepath.Join(root, "live", "outbox", "agent-a"))
	mustMkdir(t, filepath.Join(root, "learned", "agent-a"))
	// A recent (<7 days, but past) timestamp — exact value doesn't
	// matter, but it MUST be in the past so the renderer emits "Xm ago".
	// Use a fixed value within the test minute; RenderRelTime uses
	// time.Now() so anything just-now → "Ns ago" / "1m ago".
	thoughtPath := filepath.Join(root, "live", "outbox", "agent-a", "1779285164940-liqau7.gdl")
	mustWrite(t, thoughtPath,
		"@thought|id:1779285164940-liqau7|author:agent-a|type:decision|subject:customer\\:42|content:choose option B|scope:agent|ts:2026-05-20T12\\:00\\:00Z|ttl:0\n",
	)
	obsPath := filepath.Join(root, "learned", "agent-a", "obs-bbbbbb.gdlm")
	mustWrite(t, obsPath,
		"@observation|id:obs-bbbbbb|author:agent-a|subject:customer\\:42|predicate:status|object:active|scope:agent|ts:2026-05-20T11\\:00\\:00Z\n",
	)
	return root
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rfc3339NanoRE matches a full RFC3339Nano timestamp (the long form we are
// REPLACING in text mode but preserving in --json mode).
var rfc3339NanoRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z`)

// TestThoughtsList_TextMode_UsesRelativeTime is the H1b text-mode gate
// for `thoughts list`: in non-JSON mode the first column must be the
// human-friendly relative time (`Nm ago` / `Nh ago` / `Nd ago` / a date),
// NOT the full RFC3339Nano stamp that ate 27 columns on every row.
//
// We accept either "(now|Ns|Nm|Nh|Nd ago)" or a YYYY-MM-DD passthrough
// (for >7-day-old rows / unparseable input). What we MUST NOT see is the
// long-form `2026-05-20T12:00:00Z`.
func TestThoughtsList_TextMode_UsesRelativeTime(t *testing.T) {
	root := h1Corpus(t)
	opts := output.RenderOpts{}
	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, opts); err != nil {
			t.Fatal(err)
		}
	})
	if out == "" {
		t.Fatal("no output from thoughts list")
	}
	if rfc3339NanoRE.MatchString(out) {
		t.Errorf("text mode must NOT emit RFC3339Nano timestamps; got:\n%s", out)
	}
}

// TestThoughtsList_JSONMode_UsesFullTime asserts the wire contract is
// preserved — humans get the friendly form, machines get full precision.
func TestThoughtsList_JSONMode_UsesFullTime(t *testing.T) {
	root := h1Corpus(t)
	opts := output.RenderOpts{JSON: true}
	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, opts); err != nil {
			t.Fatal(err)
		}
	})
	if !rfc3339NanoRE.MatchString(out) {
		t.Errorf("--json must preserve full RFC3339Nano; got:\n%s", out)
	}
	// And the JSON must parse cleanly with `ts` populated as the
	// long-form string.
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		if ts, _ := got["ts"].(string); !rfc3339NanoRE.MatchString(ts) {
			t.Errorf("JSON ts not RFC3339Nano: %q", ts)
		}
	}
}

// TestRecall_TextMode_UsesShortIDs asserts the H1b id-shortening: text
// mode emits the 6-char suffix (e.g. `liqau7`, `bbbbbb`) and NOT the full
// 20-char id by default.
func TestRecall_TextMode_UsesShortIDs(t *testing.T) {
	root := h1Corpus(t)
	opts := output.RenderOpts{}
	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{}, opts); err != nil {
			t.Fatal(err)
		}
	})
	// The full id MUST NOT appear in text mode by default.
	if strings.Contains(out, "1779285164940-liqau7") {
		t.Errorf("text recall must shorten ids; got long id in:\n%s", out)
	}
	// The short suffix MUST appear.
	if !strings.Contains(out, "liqau7") {
		t.Errorf("text recall missing the 6-char suffix `liqau7`:\n%s", out)
	}
}

// TestRecall_JSONMode_UsesFullIDs asserts --json keeps the full
// machine-precision id intact (wire contract).
func TestRecall_JSONMode_UsesFullIDs(t *testing.T) {
	root := h1Corpus(t)
	opts := output.RenderOpts{JSON: true}
	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{}, opts); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1779285164940-liqau7") {
		t.Errorf("--json must preserve full id; got:\n%s", out)
	}
}

// TestThoughtsList_FullIDsFlag_OverridesShortening lets advanced users
// opt back into long-form IDs in text mode (when piping to scripts that
// know the wire format). The flag is `--full-ids`.
func TestThoughtsList_FullIDsFlag_OverridesShortening(t *testing.T) {
	root := h1Corpus(t)
	t.Setenv("RUFIO_FULL_IDS", "1")
	opts := output.RenderOpts{}
	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, opts); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1779285164940-liqau7") {
		t.Errorf("--full-ids (via RUFIO_FULL_IDS=1) must keep long ids in text mode; got:\n%s", out)
	}
}
