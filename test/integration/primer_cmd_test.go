package integration_test

import (
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// `rufio primer` is the cold-start anchor (#114 / #112): print the same
// primer `rufio init` writes to RUFIO.md, on demand, from ANYWHERE — no
// `rufio init` required, no substrate required. Because the primer is the
// single source of truth that teaches an agent how to use the substrate,
// the verb itself must work in a fresh shell with zero project state.

// TestPrimer_PrintsSubstantialMarkdown asserts the verb exits 0 and prints
// a substantial markdown document to stdout. The 500-char threshold rules
// out a stub / empty placeholder while staying loose enough that future
// trimming of the primer never trips this guard.
func TestPrimer_PrintsSubstantialMarkdown(t *testing.T) {
	// Bare tempdir — NO `rufio init`. The primer is teaching material, not
	// project-dependent state; it must work outside any project.
	dir := mkProject(t)
	r := testutil.RunCLI(t, []string{"primer"}, dir, nil)
	if r.Code != 0 {
		t.Fatalf("primer: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	if len(r.Stdout) < 500 {
		t.Fatalf("primer stdout too short (%d bytes); want a substantial markdown document:\n%s", len(r.Stdout), r.Stdout)
	}
	// Nothing should go to stderr on the happy path — the primer is data.
	if strings.TrimSpace(r.Stderr) != "" {
		t.Errorf("primer wrote to stderr unexpectedly: %q", r.Stderr)
	}
}

// TestPrimer_ContainsCanonicalPhrases asserts the load-bearing fragments
// an agent will key on — same shape as init_primer_test.go's
// loadBearingFragments contract. If primer.go's buildPrimer() drops any of
// these, both `rufio init` and `rufio primer` regress together — which is
// the whole point of single-source-of-truth.
func TestPrimer_ContainsCanonicalPhrases(t *testing.T) {
	dir := mkProject(t)
	r := testutil.RunCLI(t, []string{"primer"}, dir, nil)
	if r.Code != 0 {
		t.Fatalf("primer: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	for _, frag := range []string{
		// shared substrate, filesystem-native.
		"shared",
		"filesystem",
		// env override agents discovered via `strings` (#112).
		"RUFIO_AGENT_ID",
		// the verb table.
		"attend",
		"think",
		"observe",
		"recall",
		"summon",
		// the namespace:local subject convention.
		"namespace:local",
		"customer:5821",
		// quorum constants — proves the same buildPrimer() is feeding both.
		"3 distinct",
		"0.85",
	} {
		if !strings.Contains(r.Stdout, frag) {
			t.Errorf("primer stdout missing canonical phrase %q", frag)
		}
	}
}

// TestPrimer_NoInitRequired asserts the verb works in a fresh tempdir with
// no .rufio/, no rufio.gdl, no anything. This is what makes it the
// cold-start anchor: an agent on a fresh checkout / new shell can run
// `rufio primer` BEFORE any project exists and learn the substrate.
func TestPrimer_NoInitRequired(t *testing.T) {
	dir := t.TempDir()
	r := testutil.RunCLI(t, []string{"primer"}, dir, nil)
	if r.Code != 0 {
		t.Fatalf("primer in bare dir: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "RUFIO_AGENT_ID") {
		t.Errorf("primer in bare dir did not print primer body:\n%s", r.Stdout)
	}
}

// TestPrimer_MatchesRufioMD asserts the single-source-of-truth contract:
// `rufio primer` stdout is byte-identical to the RUFIO.md `rufio init`
// writes. The two MUST emit the same primer or agents reading one and
// not the other get different teaching material.
func TestPrimer_MatchesRufioMD(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"primer"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("primer: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	rufiomd := readPrimer(t, workdir)
	if r.Stdout != rufiomd {
		t.Errorf("primer stdout diverges from RUFIO.md (single source of truth violated)\n--- stdout (%d) ---\n%s\n--- RUFIO.md (%d) ---\n%s",
			len(r.Stdout), r.Stdout, len(rufiomd), rufiomd)
	}
}
