package integration_test

// Issue #123 — friction-reduction PR for the help-text bugs cold agents
// hit when walking `rufio <verb> --help`. Each helper below pins one
// of the five fixes plus two functional regressions for the type
// changes.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestDevHelp_ForceFlagDisplay — `rufio dev --help` previously rendered
// the `--force` flag as `--force rufio dev` because the description
// embedded the literal phrase "`rufio dev`" in backticks, and Cobra
// promotes backticked content to the flag's value-placeholder. The
// fixed help must show `--force` cleanly (no placeholder after it,
// because the flag is a bool) and must NOT contain the leaked phrase.
func TestDevHelp_ForceFlagDisplay(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"dev", "--help"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("rufio dev --help: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	help := r.Stdout + r.Stderr
	if strings.Contains(help, "--force rufio dev") {
		t.Errorf("dev --help still leaks template into flag display:\n%s", help)
	}
	// The clean form: a line beginning with `--force` followed by
	// whitespace (i.e. NOT followed by another identifier as a fake
	// placeholder).
	if !strings.Contains(help, "--force ") {
		t.Errorf("dev --help missing --force flag entirely:\n%s", help)
	}
}

// TestPromoteHelp_ToFlagHidden — `--to` is a v1 dead flag (the parser
// rejects anything other than "live"). Help must not surface it so
// cold agents do not try to use it. v2 can flip Hidden:false when
// staged promotion lands.
//
// v1.0.4 added a global `--token` flag (bearer-token for --server) — its
// rendered form `--token string` matches "--to" as a substring. We use
// a regex with a word boundary to assert the absence of `--to` as its
// own flag rather than as a prefix of another.
func TestPromoteHelp_ToFlagHidden(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"promote", "--help"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("rufio promote --help: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	help := r.Stdout + r.Stderr
	if matched, _ := regexp.MatchString(`--to\b`, help); matched {
		t.Errorf("promote --help still surfaces dead --to flag:\n%s", help)
	}
}

// TestObserveHelp_ConfidenceTypeIsFloat — `--confidence` semantically
// accepts a [0,1] float. Help must render it as `float` (or another
// non-string numeric placeholder) so cold agents know to pass `0.85`
// not the word "high".
func TestObserveHelp_ConfidenceTypeIsFloat(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"observe", "--help"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("rufio observe --help: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	help := r.Stdout + r.Stderr
	if strings.Contains(help, "--confidence string") {
		t.Errorf("observe --help still types --confidence as string:\n%s", help)
	}
	if !strings.Contains(help, "--confidence float") {
		t.Errorf("observe --help should type --confidence as float:\n%s", help)
	}
}

// TestThinkHelp_TTLTypeIsNumeric — `--ttl` accepts whole seconds. Help
// must render a numeric placeholder (int) so cold agents know to pass
// `300` not `5m`.
func TestThinkHelp_TTLTypeIsNumeric(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"think", "--help"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("rufio think --help: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	help := r.Stdout + r.Stderr
	if strings.Contains(help, "--ttl string") {
		t.Errorf("think --help still types --ttl as string:\n%s", help)
	}
	if !strings.Contains(help, "--ttl int") {
		t.Errorf("think --help should type --ttl as int:\n%s", help)
	}
}

// TestDiffHelp_DuplicationDocumented — `rufio diff <path>@v1
// <path>@v2` requires the path to be typed twice. v1 keeps the
// two-arg form (it matches every shell-completion expectation); the
// help text must explicitly say so so cold agents don't infer a
// single-arg shortcut and waste two attempts.
func TestDiffHelp_DuplicationDocumented(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"diff", "--help"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("rufio diff --help: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	help := r.Stdout + r.Stderr
	// Whichever phrasing the fix lands, the help must EXPLICITLY mark
	// the duplication. "same path" + "twice" together is the minimum
	// bar.
	if !strings.Contains(help, "same path") {
		t.Errorf("diff --help should mention \"same path\" requirement:\n%s", help)
	}
	if !strings.Contains(help, "twice") {
		t.Errorf("diff --help should note path must be typed \"twice\":\n%s", help)
	}
}

// TestPushDefaultStageDraft — `--stage` was defaulting to `live`,
// silently skipping the approve/promote workflow. The default
// changes to `draft`. Help must reflect the new default so a cold
// agent reading help understands `push` is a draft-by-default
// action.
func TestPushDefaultStageDraft(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"push", "--help"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("rufio push --help: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	help := r.Stdout + r.Stderr
	if !strings.Contains(help, `default "draft"`) {
		t.Errorf("push --help should show default \"draft\":\n%s", help)
	}
	if strings.Contains(help, `default "live"`) {
		t.Errorf("push --help still shows default \"live\":\n%s", help)
	}
}

// TestObserveAcceptsFloatConfidence — functional regression for the
// type change. `--confidence=0.85` must still produce a successful
// observe and round-trip the value (JSON path is the deterministic
// readback).
func TestObserveAcceptsFloatConfidence(t *testing.T) {
	root := initProject(t)
	r := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y",
		"--scope=agent", "--confidence=0.85", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("observe --confidence=0.85: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	// Cheap-but-sufficient pinning: the JSON payload should contain
	// confidence:0.85.
	if !strings.Contains(r.Stdout, `"confidence":0.85`) {
		t.Errorf("observe JSON missing confidence=0.85:\n%s", r.Stdout)
	}
}

// TestThinkAcceptsNumericTTL — functional regression for the type
// change. `--ttl=300` must still produce a successful think and
// round-trip the value.
func TestThinkAcceptsNumericTTL(t *testing.T) {
	root := initProject(t)
	r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent", "--ttl=300", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("think --ttl=300: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stdout, `"ttl":300`) {
		t.Errorf("think JSON missing ttl=300:\n%s", r.Stdout)
	}
}
