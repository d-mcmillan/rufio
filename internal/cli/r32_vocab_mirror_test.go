// Package cli — R32 vocab-mirror tests for the three reducible STOPs the
// R32 cold-agent vet surfaced. The friction was paired-verb prose flags:
// confirm canonicalises --evidence, refute canonicalises --reason, goal
// complete canonicalises --outcome — and a cold agent typing the sibling
// verb's word got rejected at each verb.
//
// Option α (aliasing): each verb keeps its canonical word but ALSO accepts
// the paired-verb word as an alias. On-disk record SHAPE is unchanged —
// aliasing affects flag-parsing, not the wire. Mutual-exclusion when the
// user passes both (error before any filesystem touch).
//
// Aliasing matrix:
//
//	confirm --evidence   (canonical)         confirm --reason   (alias → evidence)
//	refute  --reason     (canonical, req'd)  refute  --evidence (alias → reason
//	                                                              ONLY when reason
//	                                                              is empty; the
//	                                                              long-standing
//	                                                              optional evidence
//	                                                              field stays put
//	                                                              when both are
//	                                                              set)
//	goal complete --outcome (canonical)      goal complete --reason   (alias)
//	goal abandon  --reason  (canonical)      goal abandon  --outcome  (alias)
//
// Tests invoke the Cobra commands directly so flag-parsing is exercised
// end-to-end (the aliasing logic lives there). On-disk verification reads
// the rendered @confirm/@refute/@goal-complete/@goal-abandon records and
// asserts the canonical field name (evidence/reason/outcome) — not the
// alias word — landed on disk.
//
// RED-first by design — these tests pin the contract precisely so the
// green code can be diff'd cleanly.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// r32ConfirmTargetProject sets up a rufio project with a seeded thought
// AND agent identity pinned to viewer. The seeded thought is fleet-scoped
// so any viewer (incl. non-author) may confirm/refute against it (the
// privacy floor admits crowd validation for non-agent scopes).
func r32ConfirmTargetProject(t *testing.T, viewer, author, thoughtID string) string {
	t.Helper()
	root := shortIDProject(t, viewer)
	seedThought(t, root, author, thoughtID, "decision", "svc:auth", "fleet")
	return root
}

// readConfirmsFile returns the full text of live/confirms/<id>.gdl.
// Used to grep for canonical field names (evidence:/reason:) on the
// rendered records.
func readConfirmsFile(t *testing.T, root, thoughtID string) string {
	t.Helper()
	bs, err := os.ReadFile(filepath.Join(root, "live", "confirms", thoughtID+".gdl"))
	if err != nil {
		t.Fatalf("read live/confirms/%s.gdl: %v", thoughtID, err)
	}
	return string(bs)
}

// readGoalRecord returns the rendered text of the goal at the given
// state (completed|abandoned). The goal lib writes one file per goal at
// live/goals/<state>/<id>.gdl with the @goal record plus an appended
// @goal-complete | @goal-abandon audit record.
func readGoalRecord(t *testing.T, root, state, goalID string) string {
	t.Helper()
	bs, err := os.ReadFile(filepath.Join(root, "live", "goals", state, goalID+".gdl"))
	if err != nil {
		t.Fatalf("read live/goals/%s/%s.gdl: %v", state, goalID, err)
	}
	return string(bs)
}

// ---------------------------------------------------------------------
// confirm — canonical --evidence; accept --reason as alias.
// ---------------------------------------------------------------------

// TestConfirm_AcceptsReasonAsAlias_OnDiskCanonicalEvidence — running
// `rufio confirm <id> --reason="..."` MUST succeed and write the prose to
// the canonical `evidence:` field on disk. The cold agent typed --reason
// (the refute word) at confirm and got rejected; aliasing eliminates that
// STOP without losing the on-disk semantic.
func TestConfirm_AcceptsReasonAsAlias_OnDiskCanonicalEvidence(t *testing.T) {
	const thoughtID = "1779321385406-jbgs5l"
	root := r32ConfirmTargetProject(t, "bob", "alice", thoughtID)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewConfirmCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{thoughtID, "--reason=this matches the spec"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("confirm --reason: unexpected error %v (out: %s)", err, buf.String())
	}

	body := readConfirmsFile(t, root, thoughtID)
	if !strings.Contains(body, "@confirm") {
		t.Fatalf("confirm record missing in %s", body)
	}
	if !strings.Contains(body, "evidence:this matches the spec") {
		t.Errorf("confirm --reason MUST land as evidence: on disk; got: %q", body)
	}
	// And the alias word must NOT appear as a record field.
	if strings.Contains(body, "reason:this matches the spec") {
		t.Errorf("on-disk @confirm record leaked alias word `reason:`; got: %q", body)
	}
}

// TestConfirm_CanonicalEvidenceStillWorks — regression guard: the
// canonical --evidence flag MUST keep working.
func TestConfirm_CanonicalEvidenceStillWorks(t *testing.T) {
	const thoughtID = "1779321385406-jbgs5l"
	root := r32ConfirmTargetProject(t, "bob", "alice", thoughtID)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewConfirmCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{thoughtID, "--evidence=canonical path"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("confirm --evidence: %v (out: %s)", err, buf.String())
	}

	body := readConfirmsFile(t, root, thoughtID)
	if !strings.Contains(body, "evidence:canonical path") {
		t.Errorf("canonical --evidence missing from record: %q", body)
	}
}

// TestConfirm_BothFlagsAtOnce_Errors — passing both --evidence and
// --reason to confirm MUST error (mutual exclusion). The error comes
// from Cobra's MarkFlagsMutuallyExclusive — no filesystem touch. Tested
// via the Cobra command directly so the SilenceErrors override (HandleError
// would os.Exit) doesn't reach the test process.
func TestConfirm_BothFlagsAtOnce_Errors(t *testing.T) {
	const thoughtID = "1779321385406-jbgs5l"
	root := r32ConfirmTargetProject(t, "bob", "alice", thoughtID)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewConfirmCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{thoughtID, "--evidence=a", "--reason=b"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("confirm --evidence + --reason MUST error; got nil (out: %s)", buf.String())
	}
	// Sanity: no confirms file should have been created.
	if _, statErr := os.Stat(filepath.Join(root, "live", "confirms", thoughtID+".gdl")); statErr == nil {
		t.Errorf("mutual-exclusion error MUST be raised before any FS touch; confirms file was written")
	}
}

// ---------------------------------------------------------------------
// refute — canonical --reason (required); accept --evidence as alias
// WHEN --reason is empty (the long-standing optional evidence field
// stays distinct when both are set).
// ---------------------------------------------------------------------

// TestRefute_AcceptsEvidenceAsAlias_OnDiskCanonicalReason — `rufio refute
// <id> --evidence="..."` (with no --reason) MUST be accepted, with the
// evidence value promoted to the canonical `reason:` field on disk. The
// cold agent typed --evidence (the confirm word) at refute and got
// rejected; aliasing absorbs that STOP.
func TestRefute_AcceptsEvidenceAsAlias_OnDiskCanonicalReason(t *testing.T) {
	const thoughtID = "1779321385406-jbgs5l"
	root := r32ConfirmTargetProject(t, "bob", "alice", thoughtID)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewRefuteCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{thoughtID, "--evidence=the spec says otherwise"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("refute --evidence (alias for reason): %v (out: %s)", err, buf.String())
	}

	body := readConfirmsFile(t, root, thoughtID)
	if !strings.Contains(body, "@refute") {
		t.Fatalf("refute record missing in %s", body)
	}
	if !strings.Contains(body, "reason:the spec says otherwise") {
		t.Errorf("refute --evidence (alias) MUST land as reason: on disk; got: %q", body)
	}
}

// TestRefute_CanonicalReasonStillWorks — regression guard: the canonical
// --reason flag MUST keep working.
func TestRefute_CanonicalReasonStillWorks(t *testing.T) {
	const thoughtID = "1779321385406-jbgs5l"
	root := r32ConfirmTargetProject(t, "bob", "alice", thoughtID)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewRefuteCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{thoughtID, "--reason=disagree per policy"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("refute --reason canonical: %v (out: %s)", err, buf.String())
	}

	body := readConfirmsFile(t, root, thoughtID)
	if !strings.Contains(body, "reason:disagree per policy") {
		t.Errorf("canonical --reason missing from @refute record: %q", body)
	}
}

// TestRefute_BothReasonAndEvidence_StaysCanonicalSplit — when BOTH
// --reason and --evidence are passed to refute, they keep their
// long-standing distinct meaning: reason = required prose, evidence =
// optional supporting facts. Aliasing only kicks in when reason is empty.
// Locks the pre-R32 contract so the alias logic doesn't quietly fold them.
func TestRefute_BothReasonAndEvidence_StaysCanonicalSplit(t *testing.T) {
	const thoughtID = "1779321385406-jbgs5l"
	root := r32ConfirmTargetProject(t, "bob", "alice", thoughtID)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewRefuteCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{thoughtID, "--reason=motivation", "--evidence=facts"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("refute --reason + --evidence: %v (out: %s)", err, buf.String())
	}
	body := readConfirmsFile(t, root, thoughtID)
	if !strings.Contains(body, "reason:motivation") {
		t.Errorf("@refute reason field missing: %q", body)
	}
	if !strings.Contains(body, "evidence:facts") {
		t.Errorf("@refute evidence field missing: %q", body)
	}
}

// ---------------------------------------------------------------------
// goal complete — canonical --outcome; accept --reason as alias.
// ---------------------------------------------------------------------

// TestGoalComplete_AcceptsReasonAsAlias_OnDiskCanonicalOutcome — `rufio
// goal complete <id> --reason="..."` MUST succeed and write the prose to
// the canonical `outcome:` field on the @goal-complete record. The cold
// agent typed --reason (the abandon word) at complete and got rejected.
func TestGoalComplete_AcceptsReasonAsAlias_OnDiskCanonicalOutcome(t *testing.T) {
	const goalID = "1779000000000-cmplta"
	root := shortIDProject(t, "alice")
	seedActiveGoal(t, root, goalID, "alice", "ship the thing", "")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	// Build the parent goal cmd so the subcommand wires up correctly.
	cmd := NewGoalCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"complete", goalID, "--reason=shipped to prod"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("goal complete --reason: %v (out: %s)", err, buf.String())
	}

	body := readGoalRecord(t, root, "completed", goalID)
	if !strings.Contains(body, "@goal-complete") {
		t.Fatalf("missing @goal-complete in %s", body)
	}
	if !strings.Contains(body, "outcome:shipped to prod") {
		t.Errorf("goal complete --reason MUST land as outcome: on disk; got: %q", body)
	}
	// Canonical-only on-disk: alias word must not appear on the @goal-complete record.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "@goal-complete") && strings.Contains(line, "reason:shipped to prod") {
			t.Errorf("@goal-complete record leaked alias word `reason:`; line: %q", line)
		}
	}
}

// TestGoalComplete_CanonicalOutcomeStillWorks — regression guard.
func TestGoalComplete_CanonicalOutcomeStillWorks(t *testing.T) {
	const goalID = "1779000000000-canon1"
	root := shortIDProject(t, "alice")
	seedActiveGoal(t, root, goalID, "alice", "ship the thing", "")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewGoalCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"complete", goalID, "--outcome=canonical path"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("goal complete --outcome canonical: %v (out: %s)", err, buf.String())
	}
	body := readGoalRecord(t, root, "completed", goalID)
	if !strings.Contains(body, "outcome:canonical path") {
		t.Errorf("canonical --outcome missing from @goal-complete: %q", body)
	}
}

// TestGoalComplete_BothFlagsAtOnce_Errors — passing both --outcome and
// --reason to goal complete MUST error (mutual exclusion). Cobra-level.
func TestGoalComplete_BothFlagsAtOnce_Errors(t *testing.T) {
	const goalID = "1779000000000-bothfl"
	root := shortIDProject(t, "alice")
	seedActiveGoal(t, root, goalID, "alice", "ship the thing", "")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewGoalCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"complete", goalID, "--outcome=a", "--reason=b"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("goal complete --outcome + --reason MUST error; got nil (out: %s)", buf.String())
	}
	if _, statErr := os.Stat(filepath.Join(root, "live", "goals", "completed", goalID+".gdl")); statErr == nil {
		t.Errorf("mutual-exclusion error MUST be raised before any FS move; completed file exists")
	}
}

// ---------------------------------------------------------------------
// goal abandon — canonical --reason; accept --outcome as alias (paired-
// verb symmetry with goal complete).
// ---------------------------------------------------------------------

// TestGoalAbandon_AcceptsOutcomeAsAlias_OnDiskCanonicalReason —
// `rufio goal abandon <id> --outcome="..."` MUST succeed and write the
// prose to the canonical `reason:` field on the @goal-abandon record.
// Symmetric counterpart to goal complete's --reason-alias.
func TestGoalAbandon_AcceptsOutcomeAsAlias_OnDiskCanonicalReason(t *testing.T) {
	const goalID = "1779000000000-abdaal"
	root := shortIDProject(t, "alice")
	seedActiveGoal(t, root, goalID, "alice", "ship the thing", "")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewGoalCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"abandon", goalID, "--outcome=deprioritised this quarter"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("goal abandon --outcome (alias): %v (out: %s)", err, buf.String())
	}

	body := readGoalRecord(t, root, "abandoned", goalID)
	if !strings.Contains(body, "@goal-abandon") {
		t.Fatalf("missing @goal-abandon in %s", body)
	}
	if !strings.Contains(body, "reason:deprioritised this quarter") {
		t.Errorf("goal abandon --outcome MUST land as reason: on disk; got: %q", body)
	}
}

// TestGoalAbandon_CanonicalReasonStillWorks — regression guard.
func TestGoalAbandon_CanonicalReasonStillWorks(t *testing.T) {
	const goalID = "1779000000000-abdcan"
	root := shortIDProject(t, "alice")
	seedActiveGoal(t, root, goalID, "alice", "ship the thing", "")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewGoalCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"abandon", goalID, "--reason=scope cut"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("goal abandon --reason canonical: %v (out: %s)", err, buf.String())
	}
	body := readGoalRecord(t, root, "abandoned", goalID)
	if !strings.Contains(body, "reason:scope cut") {
		t.Errorf("canonical --reason missing from @goal-abandon: %q", body)
	}
}

// TestGoalAbandon_BothFlagsAtOnce_Errors — passing both --reason and
// --outcome to goal abandon MUST error (mutual exclusion). Cobra-level.
func TestGoalAbandon_BothFlagsAtOnce_Errors(t *testing.T) {
	const goalID = "1779000000000-abdbth"
	root := shortIDProject(t, "alice")
	seedActiveGoal(t, root, goalID, "alice", "ship the thing", "")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewGoalCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"abandon", goalID, "--reason=a", "--outcome=b"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("goal abandon --reason + --outcome MUST error; got nil (out: %s)", buf.String())
	}
	if _, statErr := os.Stat(filepath.Join(root, "live", "goals", "abandoned", goalID+".gdl")); statErr == nil {
		t.Errorf("mutual-exclusion error MUST be raised before any FS move; abandoned file exists")
	}
}

// ---------------------------------------------------------------------
// --help discoverability — each paired-verb prose flag MUST surface in
// the corresponding verb's --help output so a cold agent who DOES read
// help sees both forms.
// ---------------------------------------------------------------------

// TestConfirm_HelpShowsAliasFlag — confirm's help text must mention
// --reason as an accepted alias for --evidence.
func TestConfirm_HelpShowsAliasFlag(t *testing.T) {
	cmd := NewConfirmCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("confirm --help: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "--evidence") {
		t.Errorf("confirm --help missing --evidence; got:\n%s", out)
	}
	if !strings.Contains(out, "--reason") {
		t.Errorf("confirm --help missing --reason alias; got:\n%s", out)
	}
}

// TestRefute_HelpShowsAliasFlag — refute already documents --reason and
// --evidence (long-standing); test pins both stay listed.
func TestRefute_HelpShowsAliasFlag(t *testing.T) {
	cmd := NewRefuteCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("refute --help: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"--reason", "--evidence"} {
		if !strings.Contains(out, want) {
			t.Errorf("refute --help missing %q; got:\n%s", want, out)
		}
	}
}

// TestGoalComplete_HelpShowsAliasFlag — goal complete must list both
// --outcome and the new --reason alias.
func TestGoalComplete_HelpShowsAliasFlag(t *testing.T) {
	cmd := NewGoalCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"complete", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("goal complete --help: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"--outcome", "--reason"} {
		if !strings.Contains(out, want) {
			t.Errorf("goal complete --help missing %q; got:\n%s", want, out)
		}
	}
}

// TestGoalAbandon_HelpShowsAliasFlag — goal abandon must list both
// --reason and the new --outcome alias.
func TestGoalAbandon_HelpShowsAliasFlag(t *testing.T) {
	cmd := NewGoalCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"abandon", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("goal abandon --help: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"--reason", "--outcome"} {
		if !strings.Contains(out, want) {
			t.Errorf("goal abandon --help missing %q; got:\n%s", want, out)
		}
	}
}
