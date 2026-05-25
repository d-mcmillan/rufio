package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// mustThink invokes `rufio think` as the named agent and returns the
// resulting thought-id by diffing the outbox before/after. Used by the
// thoughts list tests to seed thoughts whose ts is current.
func mustThink(t *testing.T, root, agent, content string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "outbox", agent, "*.gdl")
	before, _ := filepath.Glob(pattern)
	set := make(map[string]bool, len(before))
	for _, p := range before {
		set[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"think",
		"--type=hypothesis",
		"--subject=customer:1",
		"--content=" + content,
		"--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("think %s: exit=%d stderr=%q", agent, res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	for _, p := range after {
		if !set[p] {
			return strings.TrimSuffix(filepath.Base(p), ".gdl")
		}
	}
	t.Fatalf("think did not produce a new file under %s", pattern)
	return ""
}

// writeRawThought lays an @thought record directly into the outbox of
// the named agent — bypassing `rufio think`. Used for the --since test
// where we need a thought with an artificially old ts.
//
// The encoded line follows the BuildThoughtRecord field order so it
// parses cleanly via gdl.ParseDocument.
func writeRawThought(t *testing.T, root, agent, id, content, ts string) {
	t.Helper()
	dir := filepath.Join(root, "live", "outbox", agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir outbox/%s: %v", agent, err)
	}
	// Order matches thought.BuildThoughtRecord: id|author|type|subject|
	// content|scope|ts|ttl.
	line := strings.Join([]string{
		"@thought",
		"id:" + id,
		"author:" + agent,
		"type:hypothesis",
		`subject:customer\:1`,
		"content:" + content,
		"scope:agent",
		"ts:" + ts,
		"ttl:0",
	}, "|") + "\n"
	path := filepath.Join(dir, id+".gdl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write raw thought: %v", err)
	}
}

// TestThoughtsList_HappyPath_AcrossAgents seeds two thoughts from
// different agents and asserts both appear in `thoughts list` output
// sorted desc by ts (later author first).
func TestThoughtsList_HappyPath_AcrossAgents(t *testing.T) {
	root := initProject(t)
	id1 := mustThink(t, root, "agent-a", "first thought from a")
	// Sleep 5ms so the second thought's ts is strictly later — enough
	// to be unambiguous via RFC3339Nano.
	time.Sleep(5 * time.Millisecond)
	id2 := mustThink(t, root, "agent-b", "second thought from b")

	// H1c: pin RUFIO_FULL_IDS=1 so id literals match in assertions
	// (text mode now shortens ids by default). The short-form is
	// proven in lib + cli unit tests.
	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, map[string]string{"RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id1) {
		t.Errorf("stdout missing id1=%s\n%s", id1, res.Stdout)
	}
	if !strings.Contains(res.Stdout, id2) {
		t.Errorf("stdout missing id2=%s\n%s", id2, res.Stdout)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), res.Stdout)
	}
	// id2 was written second → its ts is greater → first line desc.
	if !strings.Contains(lines[0], id2) {
		t.Errorf("desc sort failed: line 0 should contain id2=%s, got %q", id2, lines[0])
	}
	if !strings.Contains(lines[1], id1) {
		t.Errorf("desc sort failed: line 1 should contain id1=%s, got %q", id1, lines[1])
	}
}

// TestThoughtsList_DedupsOutboxAndInbox copies an outbox thought to a
// peer's inbox (simulating routing) and asserts `thoughts list` does
// not show the same id twice.
func TestThoughtsList_DedupsOutboxAndInbox(t *testing.T) {
	root := initProject(t)
	id := mustThink(t, root, "agent-a", "single thought")

	// Simulate routing: copy outbox/agent-a/<id>.gdl to inbox/agent-b/<id>.gdl.
	src := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")
	dstDir := filepath.Join(root, "live", "inbox", "agent-b")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir inbox/agent-b: %v", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, id+".gdl"), data, 0o644); err != nil {
		t.Fatalf("write inbox copy: %v", err)
	}

	// H1c: pin RUFIO_FULL_IDS=1 so id literals match in assertions
	// (text mode now shortens ids by default). The short-form is
	// proven in lib + cli unit tests.
	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, map[string]string{"RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	count := strings.Count(res.Stdout, id)
	if count != 1 {
		t.Errorf("dedup failed: id %s appears %d times, want 1:\n%s", id, count, res.Stdout)
	}
}

// TestThoughtsList_SinceFilter_FiltersOldThoughts seeds an OLD thought
// (manually written with a stale ts) plus a fresh one via `rufio think`,
// then asserts `--since=1h` returns only the fresh one.
func TestThoughtsList_SinceFilter_FiltersOldThoughts(t *testing.T) {
	root := initProject(t)

	// Write a thought with a ts 2h in the past.
	oldTS := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	oldID := "1000000000000-aaaaaa"
	writeRawThought(t, root, "agent-a", oldID, "ancient thinking", oldTS)

	// Then a fresh thought from a different agent.
	freshID := mustThink(t, root, "agent-b", "fresh thinking")

	res := testutil.RunCLI(t, []string{"thoughts", "list", "--since=1h"}, root, map[string]string{"RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, freshID) {
		t.Errorf("stdout missing fresh id=%s\n%s", freshID, res.Stdout)
	}
	if strings.Contains(res.Stdout, oldID) {
		t.Errorf("stdout includes old id=%s but --since=1h should have filtered it:\n%s", oldID, res.Stdout)
	}
}

// TestThoughtsList_JSONOutput_HasExpectedShape asserts --json emits
// valid JSONL with every D20.4 field present.
func TestThoughtsList_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustThink(t, root, "agent-a", "for-json")

	res := testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 1 {
		t.Fatalf("want 1 JSONL line, got %d:\n%s", len(lines), res.Stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, lines[0])
	}
	checks := map[string]interface{}{
		"_type":    "thought",
		"_version": "1",
		"id":       id,
		"author":   "agent-a",
		"type":     "hypothesis",
		"subject":  "customer:1",
		"content":  "for-json",
		"scope":    "agent",
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s=%v want %v", k, got[k], want)
		}
	}
	if _, ok := got["ts"]; !ok {
		t.Errorf("missing ts field: %v", got)
	}
	if _, ok := got["ttl"]; !ok {
		t.Errorf("missing ttl field: %v", got)
	}
}

// TestThoughtsList_EmptyResult_StdoutEmpty asserts exit 0 + empty
// stdout when no outbox/inbox thoughts exist.
func TestThoughtsList_EmptyResult_StdoutEmpty(t *testing.T) {
	root := initProject(t)
	// H1c: pin RUFIO_FULL_IDS=1 so id literals match in assertions
	// (text mode now shortens ids by default). The short-form is
	// proven in lib + cli unit tests.
	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, map[string]string{"RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout should be empty for no-thoughts case:\n%q", res.Stdout)
	}
}

// TestThoughtsList_NotInProject_Exit1 asserts running outside a project
// surfaces NotInProjectError as exit 1.
func TestThoughtsList_NotInProject_Exit1(t *testing.T) {
	workdir := t.TempDir()
	res := testutil.RunCLI(t, []string{"thoughts", "list"}, workdir, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q (want 'not inside a Rufio project')", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio thoughts:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestThoughtsList_InvalidSince_Exit2 asserts a malformed --since
// surfaces InvalidDurationError as exit 2.
func TestThoughtsList_InvalidSince_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"thoughts", "list", "--since=banana"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--since") {
		t.Errorf("stderr=%q (want '--since' parse error)", res.Stderr)
	}
}
