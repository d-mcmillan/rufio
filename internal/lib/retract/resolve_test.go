// Package retract — R29a tests: short-ID suffix resolver.
//
// In display, `thoughts list` / `recall` text-mode renders the 6-char
// suffix of a thought-id (output.ShortID). The write verbs
// (retract/confirm/refute/lineage/reason --decision) were rejecting that
// same suffix with NoSuchThoughtError because Lookup did an exact
// filename match. Agents had to dump --json to recover the canonical id
// and paste it back — friction R29 cited as the load-bearing asymmetry
// preventing native-feel.
//
// These tests pin the resolver contract: a value matching <unix-millis>-<rand6>
// is a full canonical id (pass-through); a value matching ^[a-z0-9]{6}$
// is a suffix-match across outbox / learned. Ambiguous suffix matches
// surface a typed error that lists candidates with disambiguation
// context. The privacy floor (#147) keeps non-author scope:agent records
// out of the candidate set so existence isn't leaked through
// disambiguation.
package retract

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// resolveSeedOutbox writes a minimal @thought record under
// live/outbox/<author>/<id>.gdl. Content fields are deliberately
// minimal — Resolve never parses outbox files for the unambiguous case
// (it only needs the filename), but parses ARE required for the
// ambiguous-disambiguation path. Caller can override fields when a test
// needs a richer record.
func resolveSeedOutbox(t *testing.T, root, author, id, typ, subject, scope string) {
	t.Helper()
	dir := filepath.Join(root, "live", "outbox", author)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	line := "@thought|id:" + id + "|author:" + author +
		"|type:" + typ + "|subject:" + subject +
		"|content:test|scope:" + scope + "|ts:2026-05-20T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write outbox: %v", err)
	}
}

// TestResolve_FullID_PassesThrough is the regression guard: callers that
// already pass the canonical <ms>-<rand6> id keep working unchanged.
func TestResolve_FullID_PassesThrough(t *testing.T) {
	root := t.TempDir()
	resolveSeedOutbox(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	got, err := Resolve(root, "1779321385406-jbgs5l", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "1779321385406-jbgs5l" {
		t.Errorf("Resolve full id = %q, want unchanged", got)
	}
}

// TestResolve_ShortIDSuffix_OutboxMatch is the core R29a happy path:
// the 6-char suffix `jbgs5l` (what `thoughts list` displayed) resolves
// to the canonical id of the matching outbox record.
func TestResolve_ShortIDSuffix_OutboxMatch(t *testing.T) {
	root := t.TempDir()
	resolveSeedOutbox(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	got, err := Resolve(root, "jbgs5l", "")
	if err != nil {
		t.Fatalf("Resolve short id: %v", err)
	}
	if got != "1779321385406-jbgs5l" {
		t.Errorf("Resolve short id = %q, want %q", got, "1779321385406-jbgs5l")
	}
}

// TestResolve_ShortIDSuffix_NoMatch surfaces the canonical
// NoSuchThoughtError so existing CLI error wrapping is preserved.
func TestResolve_ShortIDSuffix_NoMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live", "outbox"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := Resolve(root, "abcdef", "")
	var noSuch *rufioerr.NoSuchThoughtError
	if !errors.As(err, &noSuch) {
		t.Fatalf("Resolve miss: want *NoSuchThoughtError, got %T %v", err, err)
	}
	if noSuch.ID != "abcdef" {
		t.Errorf("NoSuchThoughtError.ID = %q, want %q", noSuch.ID, "abcdef")
	}
}

// TestResolve_AmbiguousShortID_ListsCandidates seeds two thoughts with
// the same 6-char suffix (theoretically rare, but the failure mode is
// load-bearing — silent first-match would write to the wrong record).
// The error must name BOTH candidates with author + type + subject so
// the agent can disambiguate.
func TestResolve_AmbiguousShortID_ListsCandidates(t *testing.T) {
	root := t.TempDir()
	resolveSeedOutbox(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")
	resolveSeedOutbox(t, root, "bob", "1779321444221-jbgs5l", "hypothesis", "retry-pattern", "fleet")

	_, err := Resolve(root, "jbgs5l", "")
	var ambig *rufioerr.AmbiguousIDError
	if !errors.As(err, &ambig) {
		t.Fatalf("ambiguous Resolve: want *AmbiguousIDError, got %T %v", err, err)
	}
	if ambig.Short != "jbgs5l" {
		t.Errorf("AmbiguousIDError.Short = %q, want %q", ambig.Short, "jbgs5l")
	}
	if len(ambig.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(ambig.Candidates))
	}
	// Render check: error message must include BOTH full ids so the
	// agent can pick by eye without re-running.
	msg := ambig.Error()
	if !strings.Contains(msg, "1779321385406-jbgs5l") || !strings.Contains(msg, "1779321444221-jbgs5l") {
		t.Errorf("AmbiguousIDError message missing one or both candidate ids:\n%s", msg)
	}
	// Disambiguation context: author + type + subject must surface so
	// the agent can pick without re-querying.
	if !strings.Contains(msg, "alice") || !strings.Contains(msg, "bob") {
		t.Errorf("AmbiguousIDError message missing author context:\n%s", msg)
	}
}

// TestResolve_ShortIDSuffix_PrivacyFloor: bob's resolution of a 6-char
// suffix that matches alice's scope:agent record must NOT surface it as
// a candidate (#147 — non-author scope:agent records are invisible to
// the privacy floor; the resolver must respect that). Bob still gets
// NoSuchThoughtError, not a leak of "alice has a private record with
// that suffix."
func TestResolve_ShortIDSuffix_PrivacyFloor(t *testing.T) {
	root := t.TempDir()
	// Alice's record is scope:agent — only alice can see/write against.
	resolveSeedOutbox(t, root, "alice", "1779321385406-jbgs5l", "hypothesis", "personal", "agent")

	// bob resolves the suffix → no candidates after privacy filter.
	_, err := Resolve(root, "jbgs5l", "bob")
	var noSuch *rufioerr.NoSuchThoughtError
	if !errors.As(err, &noSuch) {
		t.Fatalf("bob -> alice's scope:agent suffix: want *NoSuchThoughtError, got %T %v", err, err)
	}
	// Alice resolving the same suffix DOES find her own record (she's
	// the author).
	got, err := Resolve(root, "jbgs5l", "alice")
	if err != nil {
		t.Fatalf("alice -> own scope:agent: %v", err)
	}
	if got != "1779321385406-jbgs5l" {
		t.Errorf("alice -> own = %q, want canonical id", got)
	}
}

// TestResolve_ShortIDSuffix_LearnedRecord: observation IDs under
// learned/<subject>/<id>.gdlm must also resolve from a 6-char suffix.
// #150 made learned/ retractable, so the resolver must walk it too.
func TestResolve_ShortIDSuffix_LearnedRecord(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "learned", "customer", "5821")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir learned: %v", err)
	}
	id := "1779321999999-zzzzzz"
	line := "@observation|id:" + id + "|author:alice|subject:customer:5821|predicate:tier|object:gold|scope:fleet|ts:2026-05-20T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdlm"), []byte(line), 0o644); err != nil {
		t.Fatalf("write learned: %v", err)
	}
	got, err := Resolve(root, "zzzzzz", "")
	if err != nil {
		t.Fatalf("Resolve learned suffix: %v", err)
	}
	if got != id {
		t.Errorf("Resolve learned suffix = %q, want %q", got, id)
	}
}
