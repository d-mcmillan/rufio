package integration_test

import (
	"strings"
	"testing"
)

// These tests pin the #122 content-audit corrections. The `rufio primer`
// verb shipped in #127/#168 as the cold-start anchor, but the underlying
// buildPrimer() template still carried five specific factual gaps the
// 2026-05-20 cold-start vet surfaced. Each subtest below pins one gap.
//
// All five tests assert against the RUFIO.md `rufio init` writes (which
// IS the buildPrimer() output) — `rufio primer` emits the same bytes by
// construction (TestPrimer_MatchesRufioMD), so pinning one side covers
// both.

// TestPrimer_NoStaleDocsRefs — gap #1.
//
// The primer used to cite `docs/cli-reference.md` and `docs/primitives.md`,
// but `rufio init` does NOT scaffold a `docs/` directory in the substrate
// it creates. Any cold agent reading the primer in a freshly-initialised
// project followed a dead link. The fix: cite only references that EXIST
// in the substrate (the binary's own --help is always available; the
// primer itself is the canonical onboarding doc).
func TestPrimer_NoStaleDocsRefs(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	for _, bad := range []string{
		"docs/cli-reference.md",
		"docs/primitives.md",
	} {
		if strings.Contains(primer, bad) {
			t.Errorf("primer still cites %q — that file is NOT scaffolded by `rufio init`, so the link is dead from the moment the primer is read", bad)
		}
	}
	// And the corrected primer must point at references that DO exist
	// in any substrate — the binary's help is always present.
	for _, good := range []string{
		"rufio <verb> --help",
		"rufio --help",
	} {
		if !strings.Contains(primer, good) {
			t.Errorf("primer must point at %q (the canonical, always-present reference)", good)
		}
	}
}

// TestPrimer_DocumentsBothIdentityPaths — gap #2.
//
// `rufio whoami` itself instructs "run `rufio identity --as=<id>` or set
// RUFIO_AGENT_ID" — i.e. the binary documents BOTH the persisted-file
// path AND the env-var path. The primer's Session-start section only
// documented the env-var path, leaving cold agents ignorant of the
// persisted path even though the binary they just ran told them about
// it. The fix: document BOTH paths with precedence and a when-to-use.
func TestPrimer_DocumentsBothIdentityPaths(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	for _, frag := range []string{
		// The env-var path stays (and is recommended for transient
		// per-shell sessions).
		"RUFIO_AGENT_ID",
		// The persisted-file path is the addition.
		"rufio identity --as=",
		".rufio/identity.local.gdl",
	} {
		if !strings.Contains(primer, frag) {
			t.Errorf("primer must document the identity-set fragment %q", frag)
		}
	}
}

// TestPrimer_NoRedundantListenAs — gap #3.
//
// `rufio listen --help` says `--as ... (default: current identity)`. When
// RUFIO_AGENT_ID is set (per Session start §1), `--as=$RUFIO_AGENT_ID` is
// redundant — it resolves to the same value the default would. The
// continuous-participation example was copying that noisy form, and
// cold agents propagated it. The fix: drop `--as` from the listen
// example (the env var already pins identity for the shell).
func TestPrimer_NoRedundantListenAs(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	if strings.Contains(primer, "rufio listen --as=$RUFIO_AGENT_ID") {
		t.Errorf("primer still ships the redundant `rufio listen --as=$RUFIO_AGENT_ID` idiom; with RUFIO_AGENT_ID set, --as defaults to current identity and the flag is noise")
	}
	// The corrected form (no --as) must be present in some shape.
	if !strings.Contains(primer, "rufio listen --catch-up") {
		t.Errorf("primer must keep a `rufio listen --catch-up` example (without redundant --as)")
	}
}

// TestPrimer_DevDaemonGuidanceCoversBothCases — gap #4.
//
// The old primer flatly said "do NOT start `rufio dev` yourself — a
// daemon is already running". That guidance came from the swarm-demo
// context (one daemon already running, multiple agents joining). For a
// fresh `rufio init` on an empty dir there is NO daemon — the
// first-mover SHOULD start one. The fix: cover both branches (fresh
// substrate → start it; existing shared substrate → check first, the
// #133 lock-guard refuses a 2nd daemon so the worst case is a friendly
// error not double-routing).
func TestPrimer_DevDaemonGuidanceCoversBothCases(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	// First-mover branch must exist (the most common cold-start case).
	if !strings.Contains(primer, "rufio dev") {
		t.Errorf("primer must teach how to start the daemon (`rufio dev`) for the first-mover case")
	}
	// The lock-guard #133 must be mentioned so an agent knows the worst
	// case of starting a second daemon is a friendly refusal, not
	// double-routing.
	for _, frag := range []string{
		// Either fragment proves the lock-guard is mentioned (the
		// canonical wording or the rufio fleet / ps check).
		"rufio fleet",
	} {
		if !strings.Contains(primer, frag) {
			t.Errorf("primer must teach how to check for an existing daemon (%q) before starting one", frag)
		}
	}
	// The OLD blanket prohibition must be gone — it is false for the
	// first-mover case the cold-start vet exposed.
	if strings.Contains(primer, "Do NOT start `rufio dev` yourself") ||
		strings.Contains(primer, "do NOT start `rufio dev` yourself") {
		t.Errorf("primer still carries the BLANKET `do NOT start rufio dev yourself` rule; the corrected guidance must distinguish first-mover (start it) from shared substrate (check first)")
	}
}

// TestPrimer_DocumentsInitNamePositional — gap #5.
//
// Neither `rufio init --help` nor the init banner explained what `[name]`
// is. Per init.go: it sets the project name in `rufio.gdl`'s `@config`
// record (used in TUI/fleet displays), falling back to the cwd basename.
// The fix: document it in the primer AND in the cmd's Long help (so an
// agent running `rufio init --help` learns what the arg does).
func TestPrimer_DocumentsInitNamePositional(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	if !strings.Contains(primer, "rufio init [name]") &&
		!strings.Contains(primer, "rufio init <name>") {
		t.Errorf("primer must document the `rufio init [name]` positional (what it does, what happens if omitted)")
	}
	// The semantics must be explained — a bare mention without
	// teaching the meaning is the same gap.
	for _, frag := range []string{
		"rufio.gdl",
	} {
		if !strings.Contains(primer, frag) {
			t.Errorf("primer must explain what `[name]` does (e.g. stored in %q)", frag)
		}
	}
}
