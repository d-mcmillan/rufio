package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// findPendingSummonFile returns the single .gdl path under
// live/summons/pending/. Fails the test if zero or more than one match.
func findPendingSummonFile(t *testing.T, root string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "summons", "pending", "*.gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob pending summons: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 pending summon file, got %d (%v)", len(matches), matches)
	}
	return matches[0]
}

func TestSummon_HappyPath_WritesPendingFile(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
		"--intent=churn signals",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	path := findPendingSummonFile(t, root)
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pending file: %v", err)
	}
	// Note: GDL render escapes colons in values, so the on-disk form
	// is `topic:customer\:5821`, not `topic:customer:5821`.
	for _, want := range []string{
		"@summon|",
		"from:agent-a",
		"to:agent-b",
		`topic:customer\:5821`,
		"intent:churn signals",
		"ttl:86400",
	} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("file missing %q.\n%s", want, bs)
		}
	}
}

func TestSummon_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
		"--intent=churn signals",
		"--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "summon" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["from"] != "agent-a" {
		t.Errorf("from=%v", got["from"])
	}
	if got["to"] != "agent-b" {
		t.Errorf("to=%v", got["to"])
	}
	if got["topic"] != "customer:5821" {
		t.Errorf("topic=%v", got["topic"])
	}
	if got["intent"] != "churn signals" {
		t.Errorf("intent=%v", got["intent"])
	}
	// ttl is a JSON number → float64 after Unmarshal.
	ttl, ok := got["ttl"].(float64)
	if !ok {
		t.Errorf("ttl is not a number: %T %v", got["ttl"], got["ttl"])
	}
	if ttl != 86400 {
		t.Errorf("ttl=%v, want 86400", ttl)
	}
}

func TestSummon_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
		"--intent=churn signals",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio summon:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	// H3d (#125): echo prefix normalized "summoned:" → "summon: ".
	if !strings.Contains(res.Stdout, "summon: id=") {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "to=agent-b") {
		t.Errorf("missing to= in confirmation: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "topic=customer:5821") {
		t.Errorf("missing topic= in confirmation: %q", res.Stdout)
	}
}

func TestSummon_MissingTopic_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--intent=churn signals",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--topic must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestSummon_InvalidTopic_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=1foo",
		"--intent=churn signals",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, `--topic "1foo": must match`) {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestSummon_MissingIntent_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--intent must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestSummon_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
		"--intent=churn signals",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio summon:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestSummon_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
		"--intent=churn signals",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}
