// Integration tests for `rufio swarm spawn`. Build the binary once,
// invoke it against t.TempDir() projects, and assert on stdout/stderr/
// exit code + on-disk effects. No mocks (CLAUDE.md §Testing).
package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestSwarmSpawn_HappyPath_WritesNRecords spawns 3 support agents and
// asserts: stdout has 3 lines (support-001/002/003), .rufio/swarm.local.gdl
// exists with 3 @spawned records.
func TestSwarmSpawn_HappyPath_WritesNRecords(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=3"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	wantLines := []string{"support-001", "support-002", "support-003"}
	if len(lines) != len(wantLines) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(wantLines), res.Stdout)
	}
	for i, want := range wantLines {
		if lines[i] != want {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], want)
		}
	}

	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "swarm.local.gdl"))
	if err != nil {
		t.Fatalf("read swarm.local.gdl: %v", err)
	}
	got := string(bs)
	for _, agent := range wantLines {
		if !strings.Contains(got, "agent:"+agent) {
			t.Errorf("on-disk file missing agent:%s\n%s", agent, got)
		}
	}
	// Sanity-check the persona field landed too.
	if !strings.Contains(got, "persona:support") {
		t.Errorf("on-disk file missing persona:support\n%s", got)
	}
}

// TestSwarmSpawn_JSONOutput_ShapeAndCount asserts --json emits one
// `_type=spawned-agent` JSONL object per added agent with the locked
// field set.
func TestSwarmSpawn_JSONOutput_ShapeAndCount(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=qa-bot", "--count=2", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 2 {
		t.Fatalf("got %d JSONL lines, want 2:\n%s", len(lines), res.Stdout)
	}
	wantAgents := []string{"qa-bot-001", "qa-bot-002"}
	for i, line := range lines {
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("invalid JSON on line %d: %v\n%q", i, err, line)
		}
		if got["_type"] != "spawned-agent" {
			t.Errorf("line %d _type = %v, want spawned-agent", i, got["_type"])
		}
		if got["_version"] != "1" {
			t.Errorf("line %d _version = %v, want 1", i, got["_version"])
		}
		if got["persona"] != "qa-bot" {
			t.Errorf("line %d persona = %v, want qa-bot", i, got["persona"])
		}
		if got["agent"] != wantAgents[i] {
			t.Errorf("line %d agent = %v, want %s", i, got["agent"], wantAgents[i])
		}
		if _, ok := got["ts"].(string); !ok {
			t.Errorf("line %d missing ts string: %v", i, got)
		}
	}
}

// TestSwarmSpawn_SubsequentCallAppends asserts that running spawn twice
// appends (not overwrites) and that the second batch starts at
// max(existing seq)+1 (D21.4 + NextSeq semantics).
func TestSwarmSpawn_SubsequentCallAppends(t *testing.T) {
	root := initProject(t)
	if r := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=2"}, root, nil); r.Code != 0 {
		t.Fatalf("first spawn: %s", r.Stderr)
	}
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=3"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("second spawn exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	wantSecondBatch := []string{"support-003", "support-004", "support-005"}
	if len(lines) != len(wantSecondBatch) {
		t.Fatalf("second batch: got %d lines, want %d:\n%s", len(lines), len(wantSecondBatch), res.Stdout)
	}
	for i, want := range wantSecondBatch {
		if lines[i] != want {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], want)
		}
	}

	// File should contain all 5 ids.
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "swarm.local.gdl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(bs)
	for _, agent := range []string{"support-001", "support-002", "support-003", "support-004", "support-005"} {
		if !strings.Contains(got, "agent:"+agent) {
			t.Errorf("file missing agent:%s\n%s", agent, got)
		}
	}
}

// TestSwarmSpawn_DistinctPersonas_IndependentSequences asserts that
// each persona has its own sequence counter (NextSeq is scoped by
// persona, per the NextSeq docstring + D21.4).
func TestSwarmSpawn_DistinctPersonas_IndependentSequences(t *testing.T) {
	root := initProject(t)
	if r := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=2"}, root, nil); r.Code != 0 {
		t.Fatalf("seed support: %s", r.Stderr)
	}
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=qa", "--count=2"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("qa spawn exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	want := []string{"qa-001", "qa-002"}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Errorf("qa batch: got %v, want %v", lines, want)
	}
}

// TestSwarmSpawn_MissingPersona_Exit2 asserts that omitting --persona
// (default empty string) surfaces *InvalidPersonaError exit 2.
func TestSwarmSpawn_MissingPersona_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--count=1"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q, want 2", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--persona must not be empty") {
		t.Errorf("stderr=%q (want '--persona must not be empty')", res.Stderr)
	}
}

// TestSwarmSpawn_InvalidPersona_Exit2 asserts a persona that fails the
// agent-id regex (uppercase, leading digit, etc.) exits 2.
func TestSwarmSpawn_InvalidPersona_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=Foo", "--count=1"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q, want 2", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, `--persona "Foo"`) {
		t.Errorf("stderr=%q (want quoted persona value)", res.Stderr)
	}
}

// TestSwarmSpawn_MissingCount_Exit2 asserts that omitting --count
// (default 0) surfaces *InvalidCountError exit 2.
func TestSwarmSpawn_MissingCount_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q, want 2", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--count 0:") {
		t.Errorf("stderr=%q (want '--count 0: must be between 1 and 50')", res.Stderr)
	}
}

// TestSwarmSpawn_CountAboveCap_Exit2 asserts --count=51 exits 2.
func TestSwarmSpawn_CountAboveCap_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=51"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q, want 2", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--count 51:") {
		t.Errorf("stderr=%q (want '--count 51: must be between 1 and 50')", res.Stderr)
	}
}

// TestSwarmSpawn_NegativeCount_Exit2 asserts --count=-1 exits 2.
func TestSwarmSpawn_NegativeCount_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=-1"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q, want 2", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--count -1:") {
		t.Errorf("stderr=%q (want '--count -1: must be between 1 and 50')", res.Stderr)
	}
}

// TestSwarmSpawn_NotInProject_Exit1 asserts running outside a Rufio
// project surfaces NotInProjectError as exit 1 (after both flag
// validators pass — so this confirms the path order in runSwarmSpawn).
func TestSwarmSpawn_NotInProject_Exit1(t *testing.T) {
	workdir := mkProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=1"}, workdir, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q, want 1", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q (want 'not inside a Rufio project')", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio swarm spawn:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestSwarmSpawn_QuietDoesNotSuppressRows asserts --quiet still emits
// the agent-id lines (rows are data, not chatter — same convention as
// summons/fleet/recall).
func TestSwarmSpawn_QuietDoesNotSuppressRows(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=2", "--quiet"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 2 {
		t.Errorf("got %d lines under --quiet, want 2 (rows are data):\n%s", len(lines), res.Stdout)
	}
}

// TestSwarmSpawn_GitignoreCoversFile asserts the on-disk file path is
// under .rufio/ (gitignored umbrella per D21.5 + D21.10). The init
// command lays down a .gitignore; we just confirm the file's parent.
func TestSwarmSpawn_GitignoreCoversFile(t *testing.T) {
	root := initProject(t)
	if r := testutil.RunCLI(t, []string{"swarm", "spawn", "--persona=support", "--count=1"}, root, nil); r.Code != 0 {
		t.Fatalf("spawn: %s", r.Stderr)
	}
	// File must exist at .rufio/swarm.local.gdl exactly (not at the
	// repo root, not under a different umbrella).
	if _, err := os.Stat(filepath.Join(root, ".rufio", "swarm.local.gdl")); err != nil {
		t.Errorf("expected .rufio/swarm.local.gdl: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "swarm.local.gdl")); !os.IsNotExist(err) {
		t.Errorf("file leaked to repo root: err=%v", err)
	}
}
