package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestInit_FoldsCardIntoExistingClaudeMd pins the v1.0.3 cold-start
// scaffolding contract. When a project's CLAUDE.md already exists,
// `rufio init` folds the locked quickstart card into it inside the
// rufio:quickstart-card markers, preserving the user's existing content.
func TestInit_FoldsCardIntoExistingClaudeMd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# my existing claude config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := testutil.RunCLI(t, []string{"init"}, dir, nil)
	if res.Code != 0 {
		t.Fatalf("init: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	if !strings.Contains(content, "# my existing claude config") {
		t.Error("existing content was clobbered")
	}
	if !strings.Contains(content, "<!-- rufio:quickstart-card v1 -->") {
		t.Error("quickstart card open marker not folded in")
	}
	if !strings.Contains(content, "<!-- /rufio:quickstart-card -->") {
		t.Error("quickstart card close marker not folded in")
	}
	if !strings.Contains(content, "rufio attend") {
		t.Error("card body not folded in")
	}
}

// TestInit_Idempotent_DoesNotDuplicateCard pins the non-negotiable
// idempotency contract: re-running init must never duplicate the card
// in a harness file.
func TestInit_Idempotent_DoesNotDuplicateCard(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := testutil.RunCLI(t, []string{"init"}, dir, nil); r.Code != 0 {
		t.Fatalf("first init: exit=%d stderr=%q", r.Code, r.Stderr)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))

	if r := testutil.RunCLI(t, []string{"init"}, dir, nil); r.Code != 0 {
		t.Fatalf("second init: exit=%d stderr=%q", r.Code, r.Stderr)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))

	if string(first) != string(second) {
		t.Errorf("second init mutated file; not idempotent.\nfirst=%q\nsecond=%q", first, second)
	}
	// Defensive: assert the marker appears exactly once.
	if n := strings.Count(string(second), "<!-- rufio:quickstart-card v1 -->"); n != 1 {
		t.Errorf("quickstart card marker present %d times, want 1", n)
	}
}

// TestInit_DoesNotCreateMissingHarnessFiles pins the "fold only into
// existing files; never create" contract. Init is invoked on existing
// projects; creating an unexpected CLAUDE.md / .cursorrules / AGENTS.md
// would presume too much about the user's harness.
func TestInit_DoesNotCreateMissingHarnessFiles(t *testing.T) {
	dir := t.TempDir()
	res := testutil.RunCLI(t, []string{"init"}, dir, nil)
	if res.Code != 0 {
		t.Fatalf("init: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, f := range []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("init created %q that user didn't have", f)
		}
	}
}

// TestInit_FoldsIntoAllThreeHarnessFiles pins the multi-file
// scaffolding contract. CLAUDE.md, .cursorrules, AGENTS.md — if all
// three exist, all three get the card.
func TestInit_FoldsIntoAllThreeHarnessFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# existing\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if r := testutil.RunCLI(t, []string{"init"}, dir, nil); r.Code != 0 {
		t.Fatalf("init: exit=%d stderr=%q", r.Code, r.Stderr)
	}
	for _, f := range []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		c := string(b)
		if !strings.Contains(c, "<!-- rufio:quickstart-card v1 -->") {
			t.Errorf("%s missing card marker", f)
		}
		if !strings.Contains(c, "rufio attend") {
			t.Errorf("%s missing card body", f)
		}
	}
}

// TestInit_QuickstartCardCoexistsWithPrimer pins that the quickstart
// fold doesn't break (or get broken by) the existing primer fold.
// Both blocks must end up in the file, each in its own marker pair.
func TestInit_QuickstartCardCoexistsWithPrimer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := testutil.RunCLI(t, []string{"init"}, dir, nil); r.Code != 0 {
		t.Fatalf("init: exit=%d stderr=%q", r.Code, r.Stderr)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	c := string(b)
	if !strings.Contains(c, "<!-- rufio:begin -->") {
		t.Error("primer block missing")
	}
	if !strings.Contains(c, "<!-- rufio:quickstart-card v1 -->") {
		t.Error("quickstart card block missing")
	}
}
