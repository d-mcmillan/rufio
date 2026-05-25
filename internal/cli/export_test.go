package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func setupExportProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{"init"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("init failed: %s", res.Stderr)
	}
	return root
}

func TestExportJSONL_OneRecordPerLine(t *testing.T) {
	root := setupExportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	// Seed 3 thoughts.
	for _, c := range []string{"alpha", "bravo", "charlie"} {
		res := testutil.RunCLI(t, []string{
			"think", "--type=hypothesis", "--subject=test:1",
			"--content=" + c, "--scope=fleet",
		}, root, env)
		if res.Code != 0 {
			t.Fatalf("seed %s failed: %s", c, res.Stderr)
		}
	}
	res := testutil.RunCLI(t, []string{"export", "--format=jsonl"}, root, env)
	if res.Code != 0 {
		t.Fatalf("export failed: %s", res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	count := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		count++
	}
	if count < 3 {
		t.Errorf("expected >= 3 records, got %d (stdout=%s)", count, res.Stdout)
	}
}

func TestExportJSONL_EachLineIsValidJSON(t *testing.T) {
	root := setupExportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	testutil.RunCLI(t, []string{"think", "--type=hypothesis", "--subject=test:1", "--content=alpha", "--scope=fleet"}, root, env)
	res := testutil.RunCLI(t, []string{"export", "--format=jsonl"}, root, env)
	if res.Code != 0 {
		t.Fatalf("export failed: %s", res.Stderr)
	}
	for _, line := range strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line is not valid JSON: %v\nline=%s", err, line)
		}
	}
}

func TestExportJSONL_RespectsPrivacy(t *testing.T) {
	root := setupExportProject(t)
	// Bob writes scope=agent.
	bobEnv := map[string]string{"RUFIO_AGENT_ID": "bob"}
	testutil.RunCLI(t, []string{"think", "--type=hypothesis", "--subject=test:1", "--content=bob secret", "--scope=agent"}, root, bobEnv)
	// Alice exports.
	aliceEnv := map[string]string{"RUFIO_AGENT_ID": "alice"}
	res := testutil.RunCLI(t, []string{"export", "--format=jsonl"}, root, aliceEnv)
	if res.Code != 0 {
		t.Fatalf("export failed: %s", res.Stderr)
	}
	if strings.Contains(res.Stdout, "bob secret") {
		t.Errorf("alice's export leaked bob's scope=agent record: %s", res.Stdout)
	}
}

func TestExportJSONL_InvalidFormat(t *testing.T) {
	root := setupExportProject(t)
	res := testutil.RunCLI(t, []string{"export", "--format=xml"}, root, nil)
	if res.Code != 2 {
		t.Errorf("expected exit 2 for invalid format, got %d (stderr=%s)", res.Code, res.Stderr)
	}
}

func TestExportGDL_RoundTrip(t *testing.T) {
	root := setupExportProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "alice"}
	testutil.RunCLI(t, []string{"think", "--type=hypothesis", "--subject=test:1", "--content=alpha", "--scope=fleet"}, root, env)
	res := testutil.RunCLI(t, []string{"export", "--format=gdl"}, root, env)
	if res.Code != 0 {
		t.Fatalf("export gdl failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, "@thought") {
		t.Errorf("gdl export should contain @thought records, got: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "alpha") {
		t.Errorf("gdl export should contain the content alpha, got: %s", res.Stdout)
	}
}
