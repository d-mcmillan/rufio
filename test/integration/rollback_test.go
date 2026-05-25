package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func rollbackFixture(t *testing.T) string {
	t.Helper()
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init", "test"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("init failed: %s", r.Stderr)
	}
	if err := os.MkdirAll(filepath.Join(workdir, "given"), 0o755); err != nil {
		t.Fatal(err)
	}
	return workdir
}

func TestRufioRollback_RoundTrip(t *testing.T) {
	workdir := rollbackFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1 content\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2 content\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"rollback", "given/doc.md@v1"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `v3`)
	mustMatch(t, r.Stdout, `rolled-back-from:v1`)

	pull := testutil.RunCLI(t, []string{"pull", "given/doc.md"}, workdir, nil)
	if pull.Stdout != "v1 content\n" {
		t.Errorf("post-rollback pull: got %q, want %q", pull.Stdout, "v1 content\n")
	}
}

func TestRufioRollback_RolledBackFromOnDisk(t *testing.T) {
	workdir := rollbackFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = testutil.RunCLI(t, []string{"rollback", "given/doc.md@v1"}, workdir, nil)

	refs, _ := os.ReadFile(filepath.Join(workdir, ".rufio", "refs", "given", "doc.md.gdl"))
	mustMatch(t, string(refs), `version:3.*rolled_back_from:1`)
}

func TestRufioRollback_NonExistentVersion(t *testing.T) {
	workdir := rollbackFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "doc.md"), []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"rollback", "given/doc.md@v999"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `(?i)no version`)
}

func TestRufioRollback_StageDraft(t *testing.T) {
	workdir := rollbackFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"rollback", "given/doc.md@v1", "--stage=draft"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `stage:draft`)
}

func TestRufioRollback_JSONShape(t *testing.T) {
	workdir := rollbackFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"rollback", "given/doc.md@v1", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &obj); err != nil {
		t.Fatalf("json: %v", err)
	}
	if int(obj["version"].(float64)) != 3 {
		t.Errorf("version: got %v, want 3", obj["version"])
	}
	if int(obj["rolledBackFrom"].(float64)) != 1 {
		t.Errorf("rolledBackFrom: got %v, want 1", obj["rolledBackFrom"])
	}
}

func TestRufioRollback_RejectsBarePath(t *testing.T) {
	workdir := rollbackFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "x.md"), []byte("x\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/x.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"rollback", "given/x.md"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("expected exit 2, got %d", r.Code)
	}
	mustMatch(t, r.Stderr, `(?i)version selector`)
}

func TestRufioRollback_RejectsStageSelector(t *testing.T) {
	workdir := rollbackFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "x.md"), []byte("x\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/x.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"rollback", "given/x.md@live"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("expected exit 2, got %d", r.Code)
	}
	mustMatch(t, r.Stderr, `(?i)version selector|@vN`)
}

func TestRufioRollback_RejectsUnknownFlag(t *testing.T) {
	workdir := rollbackFixture(t)
	r := testutil.RunCLI(t, []string{"rollback", "given/x.md@v1", "--bogus"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2", r.Code)
	}
}
