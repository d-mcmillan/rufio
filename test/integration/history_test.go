package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func historyFixture(t *testing.T) string {
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

func TestRufioHistory_LatestFirst(t *testing.T) {
	workdir := historyFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	// First two intentionally land LIVE — explicit --stage=live now
	// that the bare push default is draft (#123).
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md", "--stage=live"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md", "--stage=live"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v3\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md", "--stage=draft"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"history", "given/doc.md"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	lines := nonEmptyLines(r.Stdout)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	mustMatch(t, lines[0], `v3.*stage:draft`)
	mustMatch(t, lines[1], `v2.*stage:live`)
	mustMatch(t, lines[2], `v1.*stage:live`)
	for _, line := range lines {
		mustMatch(t, line, `^\d{4}-\d{2}-\d{2}T`)
		mustMatch(t, line, `v\d+`)
		mustMatch(t, line, `stage:(draft|staged|live)`)
		mustMatch(t, line, `sha256:[0-9a-f]{12}…`)
		mustMatch(t, line, `author:`)
	}
}

func TestRufioHistory_JSONOnePerLineLatestFirst(t *testing.T) {
	workdir := historyFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"history", "given/doc.md", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	lines := nonEmptyLines(r.Stdout)
	if len(lines) != 2 {
		t.Fatalf("got %d JSON lines, want 2", len(lines))
	}
	var v2 map[string]interface{}
	var v1 map[string]interface{}
	_ = json.Unmarshal([]byte(lines[0]), &v2)
	_ = json.Unmarshal([]byte(lines[1]), &v1)
	if int(v2["version"].(float64)) != 2 {
		t.Errorf("first line version: got %v, want 2", v2["version"])
	}
	if int(v1["version"].(float64)) != 1 {
		t.Errorf("second line version: got %v, want 1", v1["version"])
	}
}

func TestRufioHistory_NoRefsError(t *testing.T) {
	workdir := historyFixture(t)
	r := testutil.RunCLI(t, []string{"history", "given/never-pushed.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `(?i)no refs`)
	mustNotMatch(t, r.Stderr, `rufio history: rufio history:`)
}

func TestRufioHistory_RejectsTraversal(t *testing.T) {
	workdir := historyFixture(t)
	r := testutil.RunCLI(t, []string{"history", "../escape.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `outside the project root`)
}

func TestRufioHistory_RejectsRufioPath(t *testing.T) {
	workdir := historyFixture(t)
	r := testutil.RunCLI(t, []string{"history", ".rufio/refs/foo"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `reserved`)
}

func TestRufioHistory_RejectsUnknownFlag(t *testing.T) {
	workdir := historyFixture(t)
	r := testutil.RunCLI(t, []string{"history", "given/x.md", "--bogus"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2", r.Code)
	}
}

func TestRufioHistory_QuietStillEmitsRows(t *testing.T) {
	// Strict --quiet rule: rows are data; --quiet must not suppress.
	workdir := historyFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"history", "given/doc.md", "--quiet"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	lines := nonEmptyLines(r.Stdout)
	if len(lines) != 2 {
		t.Errorf("--quiet should NOT suppress rows; got %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "v2") {
		t.Errorf("first line should be v2; got %q", lines[0])
	}
}
