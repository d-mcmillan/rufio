// Package cli — R31 polish-pass tests for the three reducible STOPs
// surfaced by the R31 cold-agent vet. All three are #125-cluster siblings
// (verb-pattern consistency). RED-first by design — each test names the
// frictions precisely so the green code can be diff'd cleanly:
//
//	P1 — `identity --as=<id>` breaks the `<noun> <verb> <arg>` pattern.
//	     Fix: `rufio identity set <id>` positional subcommand. Keep --as= for compat.
//	P2 — `think --subject` vs `reason --topics` semantic asymmetry.
//	     Fix: `reason --subject=<single>` canonical; --topics preserved as
//	     record-label legacy (singular cognitive-subject + plural labels
//	     mirrors `observe --subject ... --topics`).
//	P3 — `recall --types=` namespace overlap with thought-type.
//	     Fix: `--types=` accepts record types ONLY; new `--thought-types=`
//	     filters within `--types=thought` by thought-subtype. Passing a
//	     thought-subtype to --types errors with a helpful redirect.
//
// Tests run the runX CLI handlers directly (same convention as
// scope_consistency_test.go and verb_consistency_test.go).
package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// r31Project sets up a minimal rufio project at t.TempDir() WITHOUT a
// pre-pinned identity — P1 tests want a virgin root so `identity set`
// is exercised end-to-end. Mirrors scopeTestProject but skips the
// identity.local.gdl seed. RUFIO_AGENT_ID is force-cleared so the env
// branch doesn't shadow the file-write under test.
func r31Project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte(""), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
		t.Fatalf("mkdir .rufio: %v", err)
	}
	t.Setenv("RUFIO_AGENT_ID", "")
	return root
}

// ---------------------------------------------------------------------
// P1 — identity set <id> positional subcommand.
// ---------------------------------------------------------------------

// TestIdentitySet_PositionalSetsIdentity — `rufio identity set bob` MUST
// persist agent:bob to .rufio/identity.local.gdl, byte-identical to the
// effect of `rufio identity --as=bob`. The positional shape closes the
// #125-cluster gap (every other write verb takes a positional or
// --flag=value; identity was the lone --as=-only odd one out).
func TestIdentitySet_PositionalSetsIdentity(t *testing.T) {
	root := r31Project(t)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewIdentityCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"set", "bob"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("identity set bob: %v", err)
	}

	got, err := identity.ReadLocalFile(root)
	if err != nil {
		t.Fatalf("ReadLocalFile: %v", err)
	}
	if got != "bob" {
		t.Errorf("after `identity set bob`, persisted id = %q, want %q", got, "bob")
	}
}

// TestIdentityAs_FlagFormStillWorks — regression guard: the legacy
// `identity --as=<id>` flag-form MUST keep working after the positional
// subcommand lands. Backward compat is non-negotiable (the brief).
func TestIdentityAs_FlagFormStillWorks(t *testing.T) {
	root := r31Project(t)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewIdentityCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--as=alice"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("identity --as=alice: %v", err)
	}

	got, err := identity.ReadLocalFile(root)
	if err != nil {
		t.Fatalf("ReadLocalFile: %v", err)
	}
	if got != "alice" {
		t.Errorf("after `identity --as=alice`, persisted id = %q, want %q", got, "alice")
	}
}

// TestIdentitySet_NoArg_ErrorsClearly — `rufio identity set` with no
// positional MUST surface a usage-style error (exit 2 territory). Cobra's
// default for a missing required positional is acceptable as long as the
// error mentions an argument is required. The test is exit-code-agnostic
// (Execute returns the error, which we observe directly) — it just pins
// the error happens at all rather than silently no-op'ing.
func TestIdentitySet_NoArg_ErrorsClearly(t *testing.T) {
	root := r31Project(t)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewIdentityCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"set"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("identity set (no arg) MUST error; got nil. output:\n%s", buf.String())
	}
	// Verify nothing was written (identity file MUST NOT exist post-error).
	got, _ := identity.ReadLocalFile(root)
	if got != "" {
		t.Errorf("identity set (no arg) wrote %q to .rufio/identity.local.gdl; want empty", got)
	}
}

// ---------------------------------------------------------------------
// P2 — reason --subject canonical; --topics preserved as record labels.
// ---------------------------------------------------------------------

// TestReason_AcceptsSubjectFlag — `reason --subject=svc:auth ...` MUST
// be accepted by the runReason handler. Pre-fix the flag is rejected
// with "unknown flag: --subject". The shape MUST mirror think/observe
// (singular entity-id form per thought.ValidateSubject).
func TestReason_AcceptsSubjectFlag(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runReason(root, reasonArgs{
		Content: "we should refund per policy 4.2",
		Subject: "customer:5821",
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runReason --subject: %v", err)
	}
}

// TestReason_SubjectInWrittenRecord — `reason --subject=customer:5821`
// MUST persist `subject:customer:5821` on the written @reason record so
// downstream surfaces (recall, lineage subject-headers) can read it.
func TestReason_SubjectInWrittenRecord(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runReason(root, reasonArgs{
		Content: "subject roundtrip",
		Subject: "customer:5821",
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runReason --subject: %v", err)
	}
	got := reasonSubjectOf(t, root, "alice")
	if got != "customer:5821" {
		t.Errorf("@reason subject on disk = %q, want %q", got, "customer:5821")
	}
}

// TestReason_TopicsFlagStillWorks — regression guard: --topics keeps
// working (legacy record-label slot, plural CSV). The two flags coexist:
// --subject is "what this reasoning is about" (singular, per
// thought.ValidateSubject); --topics is record labels (plural CSV).
// Matches the existing `observe --subject ... --topics` shape.
func TestReason_TopicsFlagStillWorks(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runReason(root, reasonArgs{
		Content: "topics legacy guard",
		Topics:  "audit,p1",
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runReason --topics: %v", err)
	}
	// The topics field MUST still be persisted (no behavioural regression).
	got := reasonTopicsOf(t, root, "alice")
	if got != "audit,p1" {
		t.Errorf("@reason topics on disk = %q, want %q", got, "audit,p1")
	}
}

// TestSiblingVerbs_SubjectSemantic_Identical — table-driven guard that
// think/observe/reason all accept --subject the same way (entity-id
// singular form per thought.ValidateSubject). The R31 finding was
// asymmetry; this test pins the symmetric contract going forward.
func TestSiblingVerbs_SubjectSemantic_Identical(t *testing.T) {
	cases := []struct {
		name string
		run  func(root string) error
	}{
		{
			name: "think",
			run: func(root string) error {
				return runThink(root, thinkArgs{
					Type: "hypothesis", Subject: "customer:5821",
					Content: "x",
				}, output.RenderOpts{Quiet: true})
			},
		},
		{
			name: "observe",
			run: func(root string) error {
				return runObserve(root, observeArgs{
					Subject: "customer:5821", Predicate: "is", Object: "x",
				}, output.RenderOpts{Quiet: true})
			},
		},
		{
			name: "reason",
			run: func(root string) error {
				return runReason(root, reasonArgs{
					Content: "x", Subject: "customer:5821",
				}, output.RenderOpts{Quiet: true})
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			root := scopeTestProject(t, "alice")
			if err := tc.run(root); err != nil {
				t.Errorf("%s --subject=customer:5821: unexpected error %v", tc.name, err)
			}
		})
	}
}

// reasonSubjectOf walks live/reasoning/<agent>/ and returns the
// subject field on the first @reason record.
func reasonSubjectOf(t *testing.T, root, agent string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "live", "reasoning", agent, "*.gdl"))
	if err != nil {
		t.Fatalf("glob reasoning: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no reasoning files under live/reasoning/%s/", agent)
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read reasoning: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse reasoning: %v", err)
	}
	for _, r := range records {
		if r.Type == "reason" {
			return r.Get("subject")
		}
	}
	t.Fatalf("no @reason record in %s", matches[0])
	return ""
}

// reasonTopicsOf — same shape as reasonSubjectOf but returns topics
// (CSV).
func reasonTopicsOf(t *testing.T, root, agent string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "live", "reasoning", agent, "*.gdl"))
	if err != nil {
		t.Fatalf("glob reasoning: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no reasoning files under live/reasoning/%s/", agent)
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read reasoning: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse reasoning: %v", err)
	}
	for _, r := range records {
		if r.Type == "reason" {
			return r.Get("topics")
		}
	}
	t.Fatalf("no @reason record in %s", matches[0])
	return ""
}

// ---------------------------------------------------------------------
// P3 — recall --thought-types= filter; --types= rejects thought-subtypes.
// ---------------------------------------------------------------------

// TestRecall_TypesThoughtSubtypes_Filter — `recall --types=thought
// --thought-types=decision` MUST return only decision-subtype thoughts.
// The new --thought-types= flag (CSV) filters within --types=thought by
// thought-subtype enum (decision|hypothesis|focus|question|observation).
func TestRecall_TypesThoughtSubtypes_Filter(t *testing.T) {
	root := scopeTestProject(t, "alice")

	// Seed three thoughts: one decision, one hypothesis, one focus.
	for _, tt := range []string{"decision", "hypothesis", "focus"} {
		if err := runThink(root, thinkArgs{
			Type: tt, Subject: "customer:5821", Content: tt + " content",
		}, output.RenderOpts{Quiet: true}); err != nil {
			t.Fatalf("seed think %s: %v", tt, err)
		}
	}

	// --types=thought --thought-types=decision MUST yield exactly the
	// decision row.
	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types: "thought", ThoughtTypes: "decision",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})

	// The columnar renderer emits one row per record. We assert decision
	// appears and hypothesis/focus do NOT.
	if !strings.Contains(out, "thought:decision") {
		t.Errorf("recall --thought-types=decision missing decision row; out:\n%s", out)
	}
	if strings.Contains(out, "thought:hypothesis") {
		t.Errorf("recall --thought-types=decision leaked hypothesis row; out:\n%s", out)
	}
	if strings.Contains(out, "thought:focus") {
		t.Errorf("recall --thought-types=decision leaked focus row; out:\n%s", out)
	}
}

// TestRecall_TypesThoughtSubtypes_FilterMultiple — `recall --types=thought
// --thought-types=decision,hypothesis` MUST return both subtypes and
// exclude focus.
func TestRecall_TypesThoughtSubtypes_FilterMultiple(t *testing.T) {
	root := scopeTestProject(t, "alice")

	for _, tt := range []string{"decision", "hypothesis", "focus"} {
		if err := runThink(root, thinkArgs{
			Type: tt, Subject: "customer:5821", Content: tt + " content",
		}, output.RenderOpts{Quiet: true}); err != nil {
			t.Fatalf("seed think %s: %v", tt, err)
		}
	}
	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types: "thought", ThoughtTypes: "decision,hypothesis",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall: %v", err)
		}
	})
	if !strings.Contains(out, "thought:decision") {
		t.Errorf("missing decision row; out:\n%s", out)
	}
	if !strings.Contains(out, "thought:hypothesis") {
		t.Errorf("missing hypothesis row; out:\n%s", out)
	}
	if strings.Contains(out, "thought:focus") {
		t.Errorf("leaked focus row; out:\n%s", out)
	}
}

// TestRecall_TypesDecision_ErrorsWithRedirect — `recall --types=decision`
// (passing a thought-subtype to --types=, the namespace this slot is
// reserved for record-types) MUST error with an ACTIONABLE redirect
// pointing the agent to `--types=thought --thought-types=decision`.
//
// Pre-fix this returned *InvalidTypesError with a generic enum dump,
// leaving cold agents to guess the right shape.
func TestRecall_TypesDecision_ErrorsWithRedirect(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runRecall(root, recallArgs{Types: "decision"}, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatalf("runRecall --types=decision MUST error; got nil")
	}
	msg := err.Error()
	// The redirect MUST surface the corrected shape. We check for the
	// substrings that make the redirect actionable: the violating token
	// ("decision"), the corrective shape (`--types=thought`), and the
	// new flag (`--thought-types`).
	for _, want := range []string{"decision", "--types=thought", "--thought-types"} {
		if !strings.Contains(msg, want) {
			t.Errorf("--types=decision error message missing %q; got %q", want, msg)
		}
	}
}

// TestRecall_TypesHypothesis_ErrorsWithRedirect — symmetric to the
// decision case. hypothesis is also a thought-subtype that previously
// collided with --types=. Same redirect contract.
func TestRecall_TypesHypothesis_ErrorsWithRedirect(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runRecall(root, recallArgs{Types: "hypothesis"}, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatalf("runRecall --types=hypothesis MUST error; got nil")
	}
	msg := err.Error()
	for _, want := range []string{"hypothesis", "--types=thought", "--thought-types"} {
		if !strings.Contains(msg, want) {
			t.Errorf("--types=hypothesis error message missing %q; got %q", want, msg)
		}
	}
}

// TestRecall_TypesObservation_OnlyReturnsSPOs — `recall
// --types=observation` MUST return ONLY SPO-triples from learned/ (the
// durable observation namespace), NOT `@thought type:observation`
// records. This is the namespace-disambiguation rule: --types= is for
// RECORD types; thought-subtypes are accessed only via --thought-types
// inside --types=thought.
func TestRecall_TypesObservation_OnlyReturnsSPOs(t *testing.T) {
	root := scopeTestProject(t, "alice")

	// Seed a thought of type:observation (this is a @thought record).
	if err := runThink(root, thinkArgs{
		Type: "observation", Subject: "customer:5821",
		Content: "fleeting observation",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think type=observation: %v", err)
	}
	// Seed a real @observation SPO under learned/.
	if err := runObserve(root, observeArgs{
		Subject: "customer:5821", Predicate: "uses", Object: "policy:refund-2",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed observe: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{Types: "observation"}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall --types=observation: %v", err)
		}
	})
	// MUST contain the SPO row.
	if !strings.Contains(out, "observation") {
		t.Errorf("--types=observation: missing observation row; out:\n%s", out)
	}
	// MUST NOT contain the @thought type:observation row.
	if strings.Contains(out, "thought:observation") {
		t.Errorf("--types=observation: leaked thought type:observation row; out:\n%s", out)
	}
	if strings.Contains(out, "fleeting observation") {
		t.Errorf("--types=observation: leaked thought CONTENT; out:\n%s", out)
	}
}

// TestRecall_TypesThoughtTypeObservation_StillReturnsThoughtsOfThatType
// — symmetric: a thought of type:observation MUST be accessible via
// `--types=thought --thought-types=observation`. This is the corrected
// path the redirect points users to.
func TestRecall_TypesThoughtTypeObservation_StillReturnsThoughtsOfThatType(t *testing.T) {
	root := scopeTestProject(t, "alice")

	if err := runThink(root, thinkArgs{
		Type: "observation", Subject: "customer:5821",
		Content: "fleeting observation",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think type=observation: %v", err)
	}
	if err := runObserve(root, observeArgs{
		Subject: "customer:5821", Predicate: "uses", Object: "policy:refund-2",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed observe: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runRecall(root, recallArgs{
			Types: "thought", ThoughtTypes: "observation",
		}, output.RenderOpts{}); err != nil {
			t.Fatalf("runRecall --types=thought --thought-types=observation: %v", err)
		}
	})
	if !strings.Contains(out, "thought:observation") {
		t.Errorf("--types=thought --thought-types=observation: missing thought:observation row; out:\n%s", out)
	}
	if !strings.Contains(out, "fleeting observation") {
		t.Errorf("--types=thought --thought-types=observation: missing thought content; out:\n%s", out)
	}
}

// TestRecall_ThoughtTypes_InvalidValue_Errors — passing an unknown
// thought-subtype to --thought-types MUST error with an
// InvalidThoughtTypeError-shaped message that lists the allowed enum
// (decision|hypothesis|focus|question|observation). Mirrors the
// existing --types validator's contract.
func TestRecall_ThoughtTypes_InvalidValue_Errors(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runRecall(root, recallArgs{
		Types: "thought", ThoughtTypes: "bogus",
	}, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatalf("runRecall --thought-types=bogus MUST error; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bogus") {
		t.Errorf("--thought-types=bogus error must name the bad value; got %q", msg)
	}
	// MUST cite the allowed enum so the agent can self-correct.
	for _, want := range []string{"decision", "hypothesis"} {
		if !strings.Contains(msg, want) {
			t.Errorf("--thought-types=bogus error missing allowed-enum hint %q; got %q", want, msg)
		}
	}
}

// _ pin: pulls thought into the import set unconditionally so the
// `decision|hypothesis|...` enum stays grep-traceable from this test
// file. (Tests above reference thought via the type strings; the import
// keeps go-vet happy if a future refactor moves the literal references.)
var _ = thought.ValidateType
var _ = errors.New
