package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestOpen_ValidSubject_Exit0_EmptySubstrate pins that on a freshly
// initialised project (no thoughts, no fleet activity) `rufio open` still
// exits 0 — empty sections are not an error condition. Locked at exit-0
// per the cross-harness Run 3 spec.
func TestOpen_ValidSubject_Exit0_EmptySubstrate(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "test:1"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "OPEN test:1") {
		t.Errorf("stdout missing OPEN header; got %q", res.Stdout)
	}
}

// TestOpen_RequiresSubject pins exit 2 when no subject arg is supplied
// — Cobra's ExactArgs(1) lands the argument-validation failure on the
// front door.
func TestOpen_RequiresSubject(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open"}, root, nil)
	if res.Code != 2 {
		t.Errorf("exit = %d, want 2 (subject required)", res.Code)
	}
}

// TestOpen_ThoughtIDArg_HintsAtLineage pins the cross-verb breadcrumb:
// a thought-id-shaped argument is rejected (exit 2) and the error
// message must mention `lineage` so the cold agent learns the right
// verb. Locked at exit 2 per UsageError semantics.
func TestOpen_ThoughtIDArg_HintsAtLineage(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "1779345848015-cxkzz1"}, root, nil)
	if res.Code != 2 {
		t.Errorf("exit = %d, want 2 (thought-id-shaped subject)", res.Code)
	}
	if !strings.Contains(res.Stderr, "lineage") {
		t.Errorf("stderr should hint at rufio lineage; got %q", res.Stderr)
	}
}

// TestOpen_EmptySubstrate_NoActivityFallback pins that when the
// substrate has no activity on the subject, the renderer prints a single
// `(no activity for <subject>)` line below the DAEMON header — so the
// caller sees an honest "nothing here" without 4 lines of empty section
// headers.
func TestOpen_EmptySubstrate_NoActivityFallback(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "test:fresh"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "no activity for test:fresh") {
		t.Errorf("expected no-activity fallback; got %q", res.Stdout)
	}
}

// TestOpen_TextOutput_OmitsEmptySections pins the read-tax-reduction
// rule: empty sections produce NO header (RECALL/THOUGHTS/etc.) — the
// renderer emits a single `(no activity for <subject>)` fallback line
// after the OPEN+DAEMON headers, which always render because they
// describe substrate state, not subject activity.
func TestOpen_TextOutput_OmitsEmptySections(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "test:1"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	for _, header := range []string{"RECALL", "THOUGHTS", "ATTENTION", "FLEET"} {
		if strings.Contains(res.Stdout, header) {
			t.Errorf("Empty section %q should be omitted; stdout: %s", header, res.Stdout)
		}
	}
	if !strings.Contains(res.Stdout, "no activity for test:1") {
		t.Errorf("Empty bundle should show 'no activity' summary; stdout: %s", res.Stdout)
	}
}

// TestOpen_TextOutput_HeaderMinimal pins the locked header format:
// `OPEN <subject> (agent=<id>, scope=<scope>)` — NO flag echo (no
// since/limit/topics). The recall section's age column already verifies
// recency and the hidden-count footer surfaces privacy filtering, so
// echoing every flag would be noise.
func TestOpen_TextOutput_HeaderMinimal(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t,
		[]string{"open", "test:1", "--since=1h", "--topics=alpha", "--limit=10"},
		root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	firstLine := strings.SplitN(res.Stdout, "\n", 2)[0]
	want := "OPEN test:1 (agent=agent-alice, scope=fleet)"
	if firstLine != want {
		t.Errorf("Header = %q, want %q (no flag-echo)", firstLine, want)
	}
}

// TestOpen_TextOutput_OrderingCanonical pins the section order:
// OPEN → DAEMON → FLEET → ATTENTION → RECALL → THOUGHTS. State-first,
// activity-second.
func TestOpen_TextOutput_OrderingCanonical(t *testing.T) {
	root := initProject(t)
	// Seed all the substrate state we need to render every section.
	if r := testutil.RunCLI(t, []string{
		"attend", "--intent=open ordering test", "--entities=test:1", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"}); r.Code != 0 {
		t.Fatalf("seed attend: %s", r.Stderr)
	}
	if r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=test:1",
		"--content=hello", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"}); r.Code != 0 {
		t.Fatalf("seed think: %s", r.Stderr)
	}
	res := testutil.RunCLI(t, []string{"open", "test:1"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	idxOPEN := strings.Index(res.Stdout, "OPEN ")
	idxDAEMON := strings.Index(res.Stdout, "DAEMON:")
	idxFLEET := strings.Index(res.Stdout, "FLEET\n")
	idxATTN := strings.Index(res.Stdout, "ATTENTION\n")
	idxRECALL := strings.Index(res.Stdout, "RECALL\n")
	idxTHOUGHTS := strings.Index(res.Stdout, "THOUGHTS\n")
	if !(idxOPEN >= 0 && idxOPEN < idxDAEMON) {
		t.Errorf("OPEN must come before DAEMON; stdout:\n%s", res.Stdout)
	}
	if !(idxDAEMON < idxFLEET) {
		t.Errorf("DAEMON must come before FLEET; stdout:\n%s", res.Stdout)
	}
	if !(idxFLEET < idxATTN) {
		t.Errorf("FLEET must come before ATTENTION; stdout:\n%s", res.Stdout)
	}
	if !(idxATTN < idxRECALL) {
		t.Errorf("ATTENTION must come before RECALL; stdout:\n%s", res.Stdout)
	}
	if !(idxRECALL < idxTHOUGHTS) {
		t.Errorf("RECALL must come before THOUGHTS; stdout:\n%s", res.Stdout)
	}
}

// TestOpen_TextOutput_HiddenCountFooter pins the privacy-elision
// footer position + format. Bottom position: it's a render-fact about
// the output, not a substrate-fact about the subject.
func TestOpen_TextOutput_HiddenCountFooter(t *testing.T) {
	root := initProject(t)
	// agent-other writes a private (scope:agent) thought on test:1.
	if r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=test:1",
		"--content=secret", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-other"}); r.Code != 0 {
		t.Fatalf("seed agent-other think: %s", r.Stderr)
	}
	// agent-self runs open and should see the elision footer.
	res := testutil.RunCLI(t, []string{"open", "test:1"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-self"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "private record") || !strings.Contains(lastLine, "hidden by privacy floor") {
		t.Errorf("Last line should be hidden-count footer; got %q\nfull: %s", lastLine, res.Stdout)
	}
}

// TestOpen_TextOutput_NoColorIsClean pins that --no-color produces an
// ANSI-clean stdout. The renderer is ANSI-free by construction (no
// output.Cyan/Green/Bold wrappers in renderOpenText); this test functions
// as a regression guard so a future renderer that adds colour also wires
// the --no-color flag through correctly.
func TestOpen_TextOutput_NoColorIsClean(t *testing.T) {
	root := initProject(t)
	if r := testutil.RunCLI(t, []string{
		"attend", "--intent=no-color test", "--entities=test:1", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"}); r.Code != 0 {
		t.Fatalf("seed attend: %s", r.Stderr)
	}
	if r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=test:1",
		"--content=hello", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"}); r.Code != 0 {
		t.Fatalf("seed think: %s", r.Stderr)
	}
	res := testutil.RunCLI(t, []string{"open", "test:1", "--no-color"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if strings.Contains(res.Stdout, "\x1b[") {
		t.Errorf("--no-color stdout must be ANSI-clean; got %q", res.Stdout)
	}
}

// TestOpen_TextOutput_ContentTruncatedAt80 pins the 80-col content cap on
// row content snippets. Long content lands with a "..." suffix; consumers
// who need the full string use --json (the JSON transport never truncates).
func TestOpen_TextOutput_ContentTruncatedAt80(t *testing.T) {
	root := initProject(t)
	long := strings.Repeat("x", 200)
	if r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=test:1",
		"--content=" + long, "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"}); r.Code != 0 {
		t.Fatalf("seed think: %s", r.Stderr)
	}
	res := testutil.RunCLI(t, []string{"open", "test:1"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if strings.Contains(res.Stdout, long) {
		t.Errorf("text output should NOT contain the full 200-char content; got %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "...") {
		t.Errorf("text output should mark truncation with '...'; got %q", res.Stdout)
	}
}

// TestOpen_JSONOutput_StableKeyset pins the locked Task 10 JSON shape:
// every top-level key the consumers contract on must be present.
func TestOpen_JSONOutput_StableKeyset(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "test:1", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	for _, key := range []string{
		"_type", "_version", "subject", "agent", "daemon",
		"fleet", "recall", "thoughts", "attention", "hidden_private_count",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing key %q in JSON output; full: %s", key, res.Stdout)
		}
	}
	if got["_type"] != "open" {
		t.Errorf("_type = %v, want open", got["_type"])
	}
}

// TestOpen_JSON_EmptySectionsAreEmptyArrays_NotNull pins JSON stability:
// empty sections serialize as `[]`, NOT null. Consumers can range over
// them without nil-checks.
func TestOpen_JSON_EmptySectionsAreEmptyArrays_NotNull(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "test:1", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	// Raw-string scan for `"fleet":null` etc — json.Unmarshal would
	// happily coerce both null and [] into nil maps, so the test would
	// pass on a buggy renderer. We assert on the on-the-wire shape.
	for _, key := range []string{"fleet", "recall", "thoughts", "attention"} {
		if strings.Contains(res.Stdout, `"`+key+`":null`) {
			t.Errorf("%s is null in JSON; want empty array []\nstdout: %s", key, res.Stdout)
		}
	}
}

// TestOpen_JSON_DaemonSubShape pins the daemon shape: an OBJECT with
// {running: bool, heartbeat: string}. NOT a bool. Object shape is
// extensible — future fields (pid, uptime, version) can land without
// breaking consumers.
func TestOpen_JSON_DaemonSubShape(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "test:1", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	daemon, ok := got["daemon"].(map[string]interface{})
	if !ok {
		t.Fatalf("daemon = %T, want object\nstdout: %s", got["daemon"], res.Stdout)
	}
	if _, ok := daemon["running"].(bool); !ok {
		t.Errorf("daemon.running = %T, want bool", daemon["running"])
	}
	// heartbeat may be "" when daemon isn't running, but it MUST be present
	// and string-typed.
	if _, ok := daemon["heartbeat"].(string); !ok {
		t.Errorf("daemon.heartbeat = %T, want string", daemon["heartbeat"])
	}
}

// TestOpen_JSON_NeverTruncates pins that --json carries the FULL content
// of every record, regardless of the 80-col text-mode cap. Consumers who
// want truncation perform it client-side; the wire format is canonical.
func TestOpen_JSON_NeverTruncates(t *testing.T) {
	root := initProject(t)
	long := strings.Repeat("y", 200)
	if r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=test:1",
		"--content=" + long, "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"}); r.Code != 0 {
		t.Fatalf("seed think: %s", r.Stderr)
	}
	res := testutil.RunCLI(t, []string{"open", "test:1", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, long) {
		t.Errorf("--json must carry the FULL 200-char content un-truncated; got %q", res.Stdout)
	}
}

// TestOpen_JSON_FullIDs pins that --json emits full thought IDs regardless
// of RUFIO_FULL_IDS. The short-id rendering applies to text mode only —
// JSON is the canonical transport for downstream consumers, so identifiers
// must be unambiguous on the wire.
func TestOpen_JSON_FullIDs(t *testing.T) {
	root := initProject(t)
	if r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=test:1",
		"--content=fingerprint", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-alice"}); r.Code != 0 {
		t.Fatalf("seed think: %s", r.Stderr)
	}
	res := testutil.RunCLI(t, []string{"open", "test:1", "--json"}, root,
		// Explicitly leave RUFIO_FULL_IDS unset — JSON ignores the env.
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	thoughts, ok := got["thoughts"].([]interface{})
	if !ok || len(thoughts) == 0 {
		t.Fatalf("thoughts missing/empty in JSON: %s", res.Stdout)
	}
	first, ok := thoughts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("thoughts[0] not an object: %T", thoughts[0])
	}
	// open.JSONPayload projects every row through RecallRowJSON which
	// uses lowercase keys (id, type, author, ...). The same helper backs
	// the MCP tool, so this assertion doubles as a fidelity-contract
	// guard: if the key here is ever wrong, both transports diverge in
	// the same direction.
	id, _ := first["id"].(string)
	// Full IDs are `<unix-millis>-<rand6>` — minimum length comfortably
	// above the 6-char short form. Use a presence + dash test for stability.
	if len(id) < 8 || !strings.Contains(id, "-") {
		t.Errorf("JSON thought id = %q, want full <millis>-<rand6> shape", id)
	}
}

// TestOpen_JSON_VersionPrefix pins `_version: 1` for the first ship.
// Bump on any breaking change to the JSON schema.
func TestOpen_JSON_VersionPrefix(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"open", "test:1", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(res.Stdout), &got)
	v, _ := got["_version"].(float64)
	if v != 1 {
		t.Errorf("_version = %v (%T), want 1", got["_version"], got["_version"])
	}
}
