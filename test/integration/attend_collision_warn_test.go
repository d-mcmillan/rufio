package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Issue #108: a cold agent that inherits an existing persisted identity can
// silently stomp another agent's attention record. Fix option (c): keep the
// overwrite semantic, but emit a stderr warning when a pre-existing
// attention record is being overwritten. Stdout (including --json) must
// stay clean; --quiet must suppress the warning.

func TestAttendCollisionWarn_NoPriorRecord_NoStderr(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=first run",
		"--entities=customer:1",
	}, root, env)

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if strings.Contains(res.Stderr, "previous attention record") {
		t.Errorf("fresh-substrate first attend leaked a collision warning to stderr: %q", res.Stderr)
	}
}

func TestAttendCollisionWarn_PriorRecord_StderrShowsTSAndIntent(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	first := testutil.RunCLI(t, []string{
		"attend",
		"--intent=A",
		"--entities=customer:1",
		"--json",
	}, root, env)
	if first.Code != 0 {
		t.Fatalf("first: exit=%d stderr=%q", first.Code, first.Stderr)
	}
	// Pull the ts from the first call's JSON stdout — that's the value
	// the second call should echo back in its stderr warning.
	var firstPayload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(first.Stdout)), &firstPayload); err != nil {
		t.Fatalf("first stdout not JSON: %v\n%q", err, first.Stdout)
	}
	firstTS, _ := firstPayload["ts"].(string)
	if firstTS == "" {
		t.Fatalf("first call did not return a ts: %v", firstPayload)
	}

	second := testutil.RunCLI(t, []string{
		"attend",
		"--intent=B",
		"--entities=customer:2",
	}, root, env)
	if second.Code != 0 {
		t.Fatalf("second: exit=%d stderr=%q", second.Code, second.Stderr)
	}

	if !strings.Contains(second.Stderr, "previous attention record") {
		t.Errorf("stderr missing 'previous attention record' marker: %q", second.Stderr)
	}
	if !strings.Contains(second.Stderr, `"A"`) {
		t.Errorf("stderr missing prior intent \"A\": %q", second.Stderr)
	}
	if !strings.Contains(second.Stderr, firstTS) {
		// Fall back to the date prefix derived from the actual firstTS
		// (YYYY-MM-DD). Avoids a year-rollover false-pass past 2026-12-31.
		datePrefix := firstTS[:10]
		if !strings.Contains(second.Stderr, datePrefix) {
			t.Errorf("stderr missing prior ts %q (or date prefix %q): %q", firstTS, datePrefix, second.Stderr)
		}
	}
	// Format invariant: single-line warning, no `[warn]`-style prefix.
	if strings.Contains(second.Stderr, "[warn") || strings.Contains(second.Stderr, "[WARN") {
		t.Errorf("stderr uses a bracketed prefix it should not: %q", second.Stderr)
	}
}

func TestAttendCollisionWarn_QuietSuppresses(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	first := testutil.RunCLI(t, []string{
		"attend",
		"--intent=A",
		"--entities=customer:1",
	}, root, env)
	if first.Code != 0 {
		t.Fatalf("first: exit=%d stderr=%q", first.Code, first.Stderr)
	}

	second := testutil.RunCLI(t, []string{
		"attend",
		"--intent=B",
		"--entities=customer:2",
		"--quiet",
	}, root, env)
	if second.Code != 0 {
		t.Fatalf("second: exit=%d stderr=%q", second.Code, second.Stderr)
	}
	if strings.Contains(second.Stderr, "previous attention record") {
		t.Errorf("--quiet did not suppress collision warning: %q", second.Stderr)
	}
}

func TestAttendCollisionWarn_JSONMode_StderrStillWarns_StdoutCleanJSON(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	first := testutil.RunCLI(t, []string{
		"attend",
		"--intent=A",
		"--entities=customer:1",
	}, root, env)
	if first.Code != 0 {
		t.Fatalf("first: exit=%d stderr=%q", first.Code, first.Stderr)
	}

	second := testutil.RunCLI(t, []string{
		"attend",
		"--intent=B",
		"--entities=customer:2",
		"--json",
	}, root, env)
	if second.Code != 0 {
		t.Fatalf("second: exit=%d stderr=%q", second.Code, second.Stderr)
	}

	if !strings.Contains(second.Stderr, "previous attention record") {
		t.Errorf("--json did not preserve stderr warning: %q", second.Stderr)
	}

	stdout := strings.TrimSpace(second.Stdout)
	if strings.Contains(stdout, "\n") {
		t.Errorf("expected single JSONL line on stdout, got embedded newlines: %q", stdout)
	}
	if strings.Contains(stdout, "previous attention record") {
		t.Errorf("collision warning leaked into stdout: %q", stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not parseable JSON: %v\nstdout=%q", err, stdout)
	}
	if got["_type"] != "attend-set" {
		t.Errorf("_type=%v, want attend-set", got["_type"])
	}
}
