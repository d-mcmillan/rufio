package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func diffFixture(t *testing.T) string {
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

func TestRufioDiff_TextDiff(t *testing.T) {
	workdir := diffFixture(t)
	file := filepath.Join(workdir, "given", "policy.md")
	_ = os.WriteFile(file, []byte("Refund threshold: $500\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/policy.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("Refund threshold: $1000\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/policy.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/policy.md@v1", "given/policy.md@v2"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `(?m)^--- given/policy\.md@v1`)
	mustMatch(t, r.Stdout, `(?m)^\+\+\+ given/policy\.md@v2`)
	mustMatch(t, r.Stdout, `@@ `)
	mustMatch(t, r.Stdout, `(?m)^-Refund threshold: \$500`)
	mustMatch(t, r.Stdout, `(?m)^\+Refund threshold: \$1000`)
}

func TestRufioDiff_IdenticalEmpty(t *testing.T) {
	workdir := diffFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "x.md"), []byte("stable\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/x.md"}, workdir, nil)
	_ = testutil.RunCLI(t, []string{"push", "given/x.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/x.md@v1", "given/x.md@v2"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if r.Stdout != "" {
		t.Errorf("expected empty stdout for identical content; got %q", r.Stdout)
	}
}

func TestRufioDiff_RejectsCrossPaths(t *testing.T) {
	workdir := diffFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "a.md"), []byte("alpha\n"), 0o644)
	_ = os.WriteFile(filepath.Join(workdir, "given", "b.md"), []byte("beta\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/a.md"}, workdir, nil)
	_ = testutil.RunCLI(t, []string{"push", "given/b.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/a.md@v1", "given/b.md@v1"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `(?i)same path`)
}

func TestRufioDiff_MissingVersion(t *testing.T) {
	workdir := diffFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "x.md"), []byte("x\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/x.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/x.md@v1", "given/x.md@v999"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `(?i)no version`)
}

func TestRufioDiff_BinaryFallback(t *testing.T) {
	workdir := diffFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "blob.bin"), []byte{0x00, 0x01, 0x02}, 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/blob.bin"}, workdir, nil)
	_ = os.WriteFile(filepath.Join(workdir, "given", "blob.bin"), []byte{0x00, 0xff, 0xfe}, 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/blob.bin"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/blob.bin@v1", "given/blob.bin@v2"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `(?i)Binary files .* differ`)
}

func TestRufioDiff_RequiresExplicitSelectors(t *testing.T) {
	workdir := diffFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "x.md"), []byte("x\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/x.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/x.md", "given/x.md@v1"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("expected exit 2 for usage error; got %d", r.Code)
	}
	mustMatch(t, r.Stderr, `(?i)explicit version selector`)
}

func TestRufioDiff_RequiresTwoArgs(t *testing.T) {
	workdir := diffFixture(t)
	r := testutil.RunCLI(t, []string{"diff", "given/x.md@v1"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
}

func TestRufioDiff_RejectsUnknownFlag(t *testing.T) {
	workdir := diffFixture(t)
	r := testutil.RunCLI(t, []string{"diff", "given/x.md@v1", "given/x.md@v2", "--bogus"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2", r.Code)
	}
}

func TestRufioDiff_QuietStillEmitsDiff(t *testing.T) {
	// Strict --quiet rule: diff text is data; --quiet must not suppress.
	workdir := diffFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/doc.md@v1", "given/doc.md@v2", "--quiet"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `(?m)^-v1`)
	mustMatch(t, r.Stdout, `(?m)^\+v2`)
}

func TestRufioDiff_JSONShape(t *testing.T) {
	workdir := diffFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"diff", "given/doc.md@v1", "given/doc.md@v2", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &obj); err != nil {
		t.Fatalf("json: %v", err)
	}
	if obj["binary"] != false {
		t.Errorf("binary: got %v, want false", obj["binary"])
	}
	if obj["identical"] != false {
		t.Errorf("identical: got %v, want false", obj["identical"])
	}
	if _, ok := obj["diff"].(string); !ok {
		t.Errorf("diff: not a string")
	}
}
