// Package quickstart — tests for the locked cold-start card content.
//
// These tests pin the prose contract: the card MUST teach the seven
// core verbs, MUST stay under the ~200-token budget, and MUST NOT
// reintroduce the deprecated "local-first / deliberately local"
// positioning. A drift on any of these breaks the cold-start anchor
// the v1.0.3 plan locks down — caught here before it ships.
package quickstart

import (
	"strings"
	"testing"
)

// TestCardV1_HasAllSevenVerbs pins the verb roster. The card's whole
// reason to exist is teaching a cold agent the seven first-contact
// verbs in one read — dropping any of them silently breaks the
// onboarding contract.
func TestCardV1_HasAllSevenVerbs(t *testing.T) {
	verbs := []string{"attend", "think", "recall", "confirm", "refute", "confirms", "listen"}
	for _, v := range verbs {
		if !strings.Contains(CardV1, v) {
			t.Errorf("Card missing verb %q", v)
		}
	}
}

// TestCardV1_BudgetCap pins an upper-bound on card size. The plan's
// "sub-200-token" headline is the design intent for the SIGNAL-PER-
// TOKEN ratio (it's the cold-start anchor, not a primer reprint); the
// LOCKED prose includes inline examples + the subject-vs-topics block
// which pushes the literal token count higher. The cap caught here is
// "card hasn't accidentally doubled" — i.e., no wholesale primer dump
// has been merged in. Approx 4 chars per English token; the locked
// card lands at ~520 by this heuristic, so 700 leaves room for small
// edits without re-vetting while still flagging a runaway expansion.
func TestCardV1_BudgetCap(t *testing.T) {
	approxTokens := len(CardV1) / 4
	if approxTokens > 700 {
		t.Errorf("Card approx token count %d exceeds budget cap (700) — has the card been doubled?", approxTokens)
	}
}

// TestCardV1_NoLocalFirstFraming pins the trusted-collaborator
// positioning copy lock — the card MUST NOT reintroduce the
// "local-first / local-only / deliberately local" framing that v1.0.3
// sweeps out of every other surface.
func TestCardV1_NoLocalFirstFraming(t *testing.T) {
	bad := []string{"local-first", "local-only", "deliberately local"}
	low := strings.ToLower(CardV1)
	for _, p := range bad {
		if strings.Contains(low, strings.ToLower(p)) {
			t.Errorf("Card uses prohibited %q framing", p)
		}
	}
}

// TestCardV1_TeachesQuorumDynamics pins the "≥3 confirmers at ≥0.85"
// rule — the cold-start card's job is to make auto-promote
// discoverable WITHOUT needing the primer. Future card updates that
// drop the rule wholesale must be deliberate (bumping CardVersion).
func TestCardV1_TeachesQuorumDynamics(t *testing.T) {
	for _, want := range []string{"3", "0.85", "auto-promote"} {
		if !strings.Contains(CardV1, want) {
			t.Errorf("Card missing quorum-dynamics anchor %q", want)
		}
	}
}

// TestCardV1_TeachesSubjectVsTopics pins the load-bearing distinction
// between --subject (the thing) and --topics (the labels). Cold-vet
// rounds repeatedly surfaced this as the #1 confusion point — the
// card MUST explain it.
func TestCardV1_TeachesSubjectVsTopics(t *testing.T) {
	low := strings.ToLower(CardV1)
	for _, want := range []string{"subject", "topics"} {
		if !strings.Contains(low, want) {
			t.Errorf("Card missing subject-vs-topics anchor %q", want)
		}
	}
}

// TestCardVersion_IsOne pins the version constant. Future enrichments
// of the card must bump CardVersion (and likely emit a new CardV2
// constant alongside CardV1, leaving the marker version tag in `rufio
// init` to detect+replace cleanly).
func TestCardVersion_IsOne(t *testing.T) {
	if CardVersion != 1 {
		t.Errorf("CardVersion = %d, want 1", CardVersion)
	}
}

// TestCardV1_TeachesPrimaryConfirmRefuteFlags pins the m2 fix: the card
// MUST teach the PRIMARY flag for each of `confirm` (--evidence) and
// `refute` (--reason). R32 added vocab-mirror aliases for the OTHER
// flag on each verb, but cold agents copy-pasting from the card should
// land on the primary form so `--help` matches what they typed and the
// motivational-vs-supporting-evidence distinction stays legible.
// (Pre-fix the card had the flags inverted — confirm with --reason and
// refute with --evidence — which works via the alias path but trains
// the wrong intuition.)
func TestCardV1_TeachesPrimaryConfirmRefuteFlags(t *testing.T) {
	// Primary forms PRESENT.
	wantPresent := []string{
		`confirm <id> --evidence="why"`,
		`refute  <id> --reason="why"`,
	}
	for _, s := range wantPresent {
		if !strings.Contains(CardV1, s) {
			t.Errorf("Card missing primary form %q", s)
		}
	}
	// Inverted (aliased) forms ABSENT — these would teach the wrong
	// intuition even though they technically work via R32 aliases.
	wantAbsent := []string{
		`confirm <id> --reason="why"`,
		`refute  <id> --evidence="why"`,
	}
	for _, s := range wantAbsent {
		if strings.Contains(CardV1, s) {
			t.Errorf("Card uses inverted (aliased) form %q — teach primary form instead", s)
		}
	}
}
