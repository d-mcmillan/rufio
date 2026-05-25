// Package cli — unit tests for the primer body returned by buildPrimer().
//
// These tests pin the v1.0.6.2 fixes for findings D1 and D2 from the
// 3-Claude free-will quorum demo on v1.0.6.1.
//
//   - D1 (HIGH): the primer's `--entities=` examples must demonstrate the
//     `namespace:local` form, NOT bare ids. In the demo all three Claude
//     agents copied an `--entities=freewill` pattern out of context, hit
//     the validator's `invalid entity id ... must match [a-z][a-z0-9-]*
//     (:[a-zA-Z0-9_-]+)+` on their FIRST command, and each picked a
//     DIFFERENT namespace workaround — fragmenting the entity graph
//     across `topic:freewill` and `concept:freewill`. The primer must
//     teach the namespaced form by example so the first command lands.
//
//   - D2 (MED): the primer must explicitly teach the `think --parent=<id>`
//     workflow for reasoning on a HYPOTHESIS (where `reason --decision=`
//     would error with `thought ... is type "hypothesis", not 'decision'`).
//     Pre-fix the primer said "--decision targets a decision, NEVER a
//     hypothesis" but did not name the alternative verb agents should
//     use, leaving them stranded mid-protocol.
//
// Sibling integration coverage lives in test/integration/init_primer_test.go
// (TestPrimer_ExampleCommandsParse) which actually execs the binary against
// every primer example — the higher-order "all docs commands parse" net.
package cli

import (
	"regexp"
	"strings"
	"testing"
)

// TestBuildPrimer_NoBareIDEntityExamples asserts every `--entities=` (and
// `--subject=`) example in the primer body uses the `namespace:local`
// form. A bare token like `--entities=freewill` is rejected by the
// validator (exit 2) on first contact — D1 from the v1.0.6.1 demo.
//
// The scan ignores angle-bracketed placeholders (`<csv>`, `<your-subject>`,
// `<entity>`) because those are documented template variables, not
// literal commands the agent is meant to copy verbatim.
func TestBuildPrimer_NoBareIDEntityExamples(t *testing.T) {
	primer := buildPrimer()

	// Match `--entities=<value>` or `--subject=<value>`. Value runs up to
	// whitespace, backtick, quote, or end-of-string. We then check that
	// every CSV item in the value either is an angle-bracket placeholder
	// or contains a `:` colon (namespace:local form).
	re := regexp.MustCompile(`--(?:entities|subject)=([^\s\` + "`" + `"',]+(?:,[^\s\` + "`" + `"',]+)*)`)
	matches := re.FindAllStringSubmatch(primer, -1)
	if len(matches) == 0 {
		t.Fatalf("primer contains no --entities=/--subject= examples — guard cannot run")
	}

	for _, m := range matches {
		raw := m[1]
		for _, item := range strings.Split(raw, ",") {
			if item == "" {
				continue
			}
			// Angle-bracket placeholder, e.g. <csv>, <entity>, <your-subject>.
			if strings.HasPrefix(item, "<") && strings.HasSuffix(item, ">") {
				continue
			}
			// Ellipsis placeholder for "fill in your value" prose — not
			// a literal bare id. The primer uses `--subject=...` inside
			// multi-flag command shapes when the focus is on a SIBLING
			// flag (e.g. `--content` or `--parent=`), not subject.
			if item == "..." || item == "…" {
				continue
			}
			// Namespaced id — has at least one colon.
			if strings.Contains(item, ":") {
				continue
			}
			t.Errorf(
				"primer has bare-id (no `:`) entity/subject example %q (D1) — full match %q. "+
					"Entity ids must be namespace:local form (e.g. topic:freewill, customer:5821).",
				item, m[0],
			)
		}
	}
}

// TestBuildPrimer_TeachesEntityNamespacingByExample asserts the primer
// includes the explicit guidance paragraph: bare ids like `freewill` are
// rejected — use `topic:freewill` or `concept:freewill`. This is the
// surgical D1 fix: examples alone are insufficient, the rule must be
// named (cold agents skim).
func TestBuildPrimer_TeachesEntityNamespacingByExample(t *testing.T) {
	primer := buildPrimer()

	// The primer already documents the regex + `namespace:local` term
	// (pinned by TestRufioInit_PrimerCorrectionsFixed). D1 adds an
	// explicit before/after example so agents see the pattern they're
	// likely to write being marked WRONG.
	wantPresent := []string{
		// Concrete BAD/GOOD example using the exact `freewill` token
		// from the demo so a future regression triggers loudly.
		"freewill",
		// Both namespace examples named so the cold agent picks one.
		"topic:freewill",
		"concept:freewill",
	}
	for _, frag := range wantPresent {
		if !strings.Contains(primer, frag) {
			t.Errorf("primer missing D1 entity-namespacing example %q", frag)
		}
	}
}

// TestBuildPrimer_TeachesReasonOnHypothesisWorkaround asserts the primer
// surfaces `think --parent=<id>` as the reasoning-chain workflow for a
// HYPOTHESIS (since `reason --decision=` only accepts decision-type
// thoughts). D2 from the v1.0.6.1 demo: agents followed the documented
// `reason --decision=` protocol on a hypothesis id and hit
// `thought ... is type "hypothesis", not 'decision'` mid-protocol.
//
// The fix lives next to the `reason` verb description so an agent
// scanning that section finds the alternative immediately.
func TestBuildPrimer_TeachesReasonOnHypothesisWorkaround(t *testing.T) {
	primer := buildPrimer()

	// The `--parent=` workaround must appear in the same vicinity as
	// the `reason` section so the agent encounters it AT the moment of
	// confusion. A loose-but-tight pin: `--parent=` must appear before
	// the etiquette section, and `think` with `--parent` together must
	// be present.
	wantPresent := []string{
		// The workaround verb + flag, separately so re-ordering text
		// doesn't false-positive.
		"--parent=",
		// And the elevation path (hypothesis -> decision first).
		"think --type=decision",
	}
	for _, frag := range wantPresent {
		if !strings.Contains(primer, frag) {
			t.Errorf("primer missing D2 reason-on-hypothesis workaround %q", frag)
		}
	}

	// `think` and `--parent=` must co-occur in a single line (a
	// command example), not just live in separate paragraphs — that
	// is the cold-agent copy-paste artifact this primer must teach.
	hasThinkParent := regexp.MustCompile(`think[^\n]*--parent=`).MatchString(primer)
	if !hasThinkParent {
		t.Errorf("primer must show `think ... --parent=` as a single command (D2)")
	}

	// And: the section must NOT only say "decision, NEVER hypothesis"
	// without naming the alternative. A primer that leaves the agent
	// stranded mid-protocol is the D2 bug. The `--parent=` must appear
	// in proximity to `reason` (the section the agent reaches for).
	// (Cap at 1000 chars since Go's RE2 caps repeat counts at 1000.)
	if !regexp.MustCompile(`(?s)reason[\s\S]{0,1000}--parent=`).MatchString(primer) {
		t.Errorf("primer mentions reason guidance but does not connect it to the `--parent=` alternative in proximity (D2)")
	}
}

// TestBuildPrimer_NoLegacyBareEntityWorkflowLines asserts the primer does
// NOT contain the unnamespaced-entity example lines that the v1.0.6.1
// demo agents were copying. Pinned negatively so a future doc edit
// cannot quietly reintroduce the foot-gun.
func TestBuildPrimer_NoLegacyBareEntityWorkflowLines(t *testing.T) {
	primer := buildPrimer()

	bad := []string{
		"--entities=freewill,self-reference",
		"--entities=freewill,",
		"--entities=feedback,",
		// `--entities=feedback` at end-of-string / line is the exact
		// D1 footgun the README example used pre-fix. Allow
		// `--entities=cli:feedback` (namespaced) which contains
		// `feedback` but is namespaced — match the BARE form only.
	}
	for _, frag := range bad {
		if strings.Contains(primer, frag) {
			t.Errorf("primer reintroduces D1 bare-id entity example %q", frag)
		}
	}
}
