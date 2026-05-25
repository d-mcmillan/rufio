package cli_test

import (
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func setupImportProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{"init"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("init failed: %s", res.Stderr)
	}
	return root
}

func TestImportJSONL_WritesRecords(t *testing.T) {
	root := setupImportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	stdin := `{"_type":"thought","author":"alice","type":"hypothesis","subject":"test:1","content":"imported alpha","scope":"fleet"}
{"_type":"thought","author":"alice","type":"hypothesis","subject":"test:1","content":"imported bravo","scope":"fleet"}
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, root, env, stdin)
	if res.Code != 0 {
		t.Fatalf("import failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "wrote 2") {
		t.Errorf("expected 'wrote 2' in stderr; got %s", res.Stderr)
	}
	// Verify via recall.
	rec := testutil.RunCLI(t, []string{"recall"}, root, env)
	if !strings.Contains(rec.Stdout, "imported alpha") {
		t.Errorf("expected imported alpha in recall; got %s", rec.Stdout)
	}
}

func TestImportJSONL_RejectsInvalidShape(t *testing.T) {
	root := setupImportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	stdin := `{"_type":"thought","author":"alice","content":"ok"}
this is not json
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, root, env, stdin)
	if res.Code != 2 {
		t.Errorf("expected exit 2 for malformed input, got %d (stderr=%s)", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "line 2") {
		t.Errorf("error should mention line 2; got %s", res.Stderr)
	}
}

func TestImportJSONL_ValidateOnlyDoesNotWrite(t *testing.T) {
	root := setupImportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	stdin := `{"_type":"thought","author":"alice","type":"hypothesis","subject":"test:1","content":"validate but do not write","scope":"fleet"}
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl", "--validate-only"}, root, env, stdin)
	if res.Code != 0 {
		t.Fatalf("validate-only failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "validated 1") {
		t.Errorf("expected 'validated 1' in stderr; got %s", res.Stderr)
	}
	rec := testutil.RunCLI(t, []string{"recall"}, root, env)
	if strings.Contains(rec.Stdout, "validate but do not write") {
		t.Errorf("validate-only should NOT have written; recall=%s", rec.Stdout)
	}
}

func TestImportJSONL_AssignsNewIDs(t *testing.T) {
	root := setupImportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	stdin := `{"_type":"thought","author":"alice","type":"hypothesis","subject":"test:1","content":"id reassign test","scope":"fleet","id":"OLD-ID-1234"}
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, root, env, stdin)
	if res.Code != 0 {
		t.Fatalf("import failed: %s", res.Stderr)
	}
	rec := testutil.RunCLI(t, []string{"recall", "--json"}, root, env)
	if !strings.Contains(rec.Stdout, "id reassign test") {
		t.Errorf("imported record missing from recall; got %s", rec.Stdout)
	}
	if strings.Contains(rec.Stdout, "OLD-ID-1234") {
		t.Errorf("imported record should get fresh id; old id leaked: %s", rec.Stdout)
	}
}

// TestImportJSONL_PartialFailure_ExitsNonZero (v1.0.4 bug #2). Before
// this fix, a JSONL pipeline that contained any unattributable record
// (missing author + no fallback identity, OR missing _type, OR an
// unsupported type) silently dropped the offending lines and exited 0.
// "wrote N" understated reality — pipelines could lose data without
// any machine-readable signal beyond grepping stderr.
//
// New contract (Option B from the bug report — preserve "write what
// you can", but signal partial failure via exit code):
//   - Every parseable record we CAN write → written.
//   - Every record we can't write (no author + no identity, no _type,
//     unsupported type) → stderr message + counted as skipped.
//   - At least one skip → exit 1 (UsageError-equivalent so pipelines
//     can `|| handle-failure`).
//   - Final stderr line: "import: wrote N, skipped M".
func TestImportJSONL_PartialFailure_ExitsNonZero(t *testing.T) {
	root := setupImportProject(t)
	// No RUFIO_AGENT_ID — so the no-author record has no fallback
	// identity and will be skipped.
	stdin := `{"_type":"thought","author":"alice","type":"hypothesis","subject":"test:1","content":"valid one","scope":"fleet"}
{"_type":"thought","type":"hypothesis","subject":"test:1","content":"missing author","scope":"fleet"}
{"_type":"thought","author":"alice","type":"hypothesis","subject":"test:1","content":"valid two","scope":"fleet"}
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, root, nil, stdin)
	if res.Code == 0 {
		t.Fatalf("expected non-zero exit when records were skipped, got 0 (stderr=%s)", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "wrote 2") {
		t.Errorf("stderr should report wrote 2; got %s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "skipped 1") {
		t.Errorf("stderr should report skipped 1; got %s", res.Stderr)
	}
	// The two valid records should still be on disk.
	rec := testutil.RunCLI(t, []string{"recall"}, root, map[string]string{"RUFIO_AGENT_ID": "alice"})
	if !strings.Contains(rec.Stdout, "valid one") || !strings.Contains(rec.Stdout, "valid two") {
		t.Errorf("the two valid records must still be written despite the skip; recall=%s", rec.Stdout)
	}
}

// TestImportJSONL_AllSkipped_ExitsNonZero pins the all-bad-records
// corner: no records written, exit non-zero, useful stderr.
func TestImportJSONL_AllSkipped_ExitsNonZero(t *testing.T) {
	root := setupImportProject(t)
	stdin := `{"_type":"thought","type":"hypothesis","content":"missing author 1"}
{"_type":"thought","type":"hypothesis","content":"missing author 2"}
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, root, nil, stdin)
	if res.Code == 0 {
		t.Fatalf("expected non-zero exit when every record was skipped, got 0 (stderr=%s)", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "wrote 0") {
		t.Errorf("stderr should report wrote 0; got %s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "skipped 2") {
		t.Errorf("stderr should report skipped 2; got %s", res.Stderr)
	}
}

// TestImportJSONL_UnsupportedType_CountedAsSkip pins that import does
// not silently absorb records of types it can't write (channel-message,
// goal, summon, etc. — which have dedicated cognition verbs as their
// only write path). They count as skips and contribute to exit 1.
func TestImportJSONL_UnsupportedType_CountedAsSkip(t *testing.T) {
	root := setupImportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	stdin := `{"_type":"thought","author":"alice","type":"hypothesis","subject":"test:1","content":"ok","scope":"fleet"}
{"_type":"channel-message","author":"alice","content":"unsupported by import"}
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, root, env, stdin)
	if res.Code == 0 {
		t.Errorf("unsupported record types must count as skipped (exit non-zero); got 0 (stderr=%s)", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "skipped 1") {
		t.Errorf("stderr should report skipped 1; got %s", res.Stderr)
	}
}

// TestImportJSONL_AuthorFallback_DoesNotCountAsSkip — when the record
// has no author but the shell has RUFIO_AGENT_ID set, the substitution
// fires and the record IS written. Should NOT count as a skip.
func TestImportJSONL_AuthorFallback_DoesNotCountAsSkip(t *testing.T) {
	root := setupImportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	stdin := `{"_type":"thought","type":"hypothesis","subject":"test:1","content":"fallback to env","scope":"fleet"}
`
	res := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, root, env, stdin)
	if res.Code != 0 {
		t.Fatalf("RUFIO_AGENT_ID fallback must NOT count as skip; got exit %d (stderr=%s)", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "wrote 1") {
		t.Errorf("stderr should report wrote 1; got %s", res.Stderr)
	}
	rec := testutil.RunCLI(t, []string{"recall"}, root, env)
	if !strings.Contains(rec.Stdout, "fallback to env") {
		t.Errorf("record should be on disk under alice; recall=%s", rec.Stdout)
	}
}

func TestImportJSONL_ExportRoundTrip(t *testing.T) {
	srcRoot := setupImportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	for _, c := range []string{"roundtrip alpha", "roundtrip bravo"} {
		testutil.RunCLI(t, []string{"think", "--type=hypothesis", "--subject=test:1", "--content=" + c, "--scope=fleet"}, srcRoot, env)
	}
	exp := testutil.RunCLI(t, []string{"export", "--format=jsonl"}, srcRoot, env)
	if exp.Code != 0 {
		t.Fatalf("export failed: %s", exp.Stderr)
	}

	dstRoot := setupImportProject(t)
	imp := testutil.RunCLIWithStdin(t, []string{"import", "--format=jsonl"}, dstRoot, env, exp.Stdout)
	if imp.Code != 0 {
		t.Fatalf("import failed: %s", imp.Stderr)
	}
	rec := testutil.RunCLI(t, []string{"recall"}, dstRoot, env)
	for _, want := range []string{"roundtrip alpha", "roundtrip bravo"} {
		if !strings.Contains(rec.Stdout, want) {
			t.Errorf("missing %q after roundtrip; got %s", want, rec.Stdout)
		}
	}
}
