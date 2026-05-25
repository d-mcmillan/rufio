package integration_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// The tripwire test for CLAUDE.md rule 1 ("no half-built features").
//
// Every command in the CLI MUST EITHER:
//   1. Be fully implemented (in implementedCommands below), OR
//   2. Exit 2 with the canonical "rufio <name> — not implemented yet —
//      planned for <target>" envelope (in expectedStubs below).
//
// Silent stubs that return success or produce mock output are forbidden.
//
// As each phase ships, command names move from expectedStubs to
// implementedCommands. At v1.0 ship, expectedStubs MUST be empty.
//
// The structural-drift test reads internal/cli/stub.go via go/parser and
// asserts that the set of names in allStubs() equals expectedStubs
// (bidirectional). Catches silent additions to stub.go in both directions.
//
// implementedCommands below is documentation-only — there is no test that
// asserts every name in it has a corresponding New<Cmd>; that linkage is
// guarded indirectly by `go build` (an unregistered command would not
// appear in `rufio --help` / `tools/list` and is caught at PR review).

var implementedCommands = []string{
	"init",
	"push",
	"pull",
	"history",
	"diff",
	"rollback",
	"dev",
	"whoami",
	"identity",
	"attend",
	"think",
	"observe",
	"reason",
	"retract",
	"confirm",
	"refute",
	"recall",
	"approve",
	"promote",
	"listen",
	"stream",
	"summon",
	"summons",
	"decline",
	"accept",
	"say",
	"leave",
	"close",
	"goal",
	"goals",
	"lineage",
	"fleet",
	"attention",
	"thoughts",
	"swarm",
	"demo",
	"mcp",
}

// expectedStubs is empty as of v1.1: every CLI command is fully
// implemented (the mcp adapter was the last stub and shipped this arc).
// Kept as a (now-empty) registry so the structural-drift tripwire still
// guards any future pre-implementation command against silent addition.
var expectedStubs = []string{}

// TestMCP_NotAStub asserts `rufio mcp` is a real server, not the canonical
// "not implemented yet" stub. With no identity resolvable it must fail on
// the genuine startup-validation path (NoIdentityError, exit 1, printed
// like any verb via HandleError) — NOT exit 2 with the stub envelope.
func TestMCP_NotAStub(t *testing.T) {
	root := initProject(t)
	r := testutil.RunCLI(t, []string{"mcp", "--root", root}, root, nil)
	mustNotMatch(t, r.Stderr, "not implemented yet")
	mustNotMatch(t, r.Stderr, "planned for")
	if r.Code == 2 {
		t.Fatalf("mcp still behaves like a stub: code=%d stderr=%q", r.Code, r.Stderr)
	}
	// Positively assert the real non-stub startup behaviour: no identity
	// resolvable → NoIdentityError, exit 1, canonical "rufio mcp: " prefix.
	if r.Code != 1 {
		t.Fatalf("mcp no-identity startup: exit=%d, want 1; stderr=%q", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "no identity set") {
		t.Fatalf("mcp no-identity stderr=%q, want substring 'no identity set'", r.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(r.Stderr), "rufio mcp:") {
		t.Fatalf("mcp startup error missing single-prefix invariant: %q", r.Stderr)
	}
}

func TestStub_VersionDoesNotPrintEnvelope(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"--version"}, workdir, nil)
	// Accept either a semver-like token (release build with ldflags
	// injection per #88) OR "dev" (default for plain `go build`). Both
	// forms prove --version is wired and not stubbed.
	mustMatch(t, r.Stdout, `(v?\d+\.\d+\.\d+|\bdev\b)`)
	mustNotMatch(t, r.Stderr, `not implemented yet`)
}

func TestStub_HelpDoesNotPrintEnvelope(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"--help"}, workdir, nil)
	if r.Code != 0 {
		t.Errorf("--help: exit %d", r.Code)
	}
	mustNotMatch(t, r.Stderr, `not implemented yet`)
}

// TestStub_StructuralDrift parses internal/cli/stub.go and asserts that
// the names in allStubs() match expectedStubs exactly. Catches the case
// where someone adds a command to the dispatcher but forgets to update
// the tripwire registry (or vice-versa).
func TestStub_StructuralDrift(t *testing.T) {
	stubFile := stubFilePath(t)
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, stubFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse stub.go: %v", err)
	}

	var found []string
	ast.Inspect(src, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "allStubs" {
			return true
		}
		// Walk the function body for composite-literal expressions of
		// the form `{"name", "target"}` and pick out the first string.
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			cl, ok := inner.(*ast.CompositeLit)
			if !ok || len(cl.Elts) < 2 {
				return true
			}
			lit, ok := cl.Elts[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			// Strip quotes: "foo" → foo
			name := lit.Value[1 : len(lit.Value)-1]
			found = append(found, name)
			return false
		})
		return false
	})

	// As of v1.1 allStubs() is legitimately empty (every command shipped),
	// so an empty `found` is the CORRECT state, not a parse failure. The
	// bidirectional set-equality below still detects any drift: a stub
	// re-added to stub.go but absent from expectedStubs (or vice-versa).
	wantSet := setOf(expectedStubs)
	gotSet := setOf(found)
	for name := range gotSet {
		if !wantSet[name] {
			t.Errorf("stub %q in stub.go but not in expectedStubs (tripwire drift)", name)
		}
	}
	for name := range wantSet {
		if !gotSet[name] {
			t.Errorf("expectedStubs has %q but stub.go does not (tripwire drift)", name)
		}
	}
}

func stubFilePath(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	stub := filepath.Join(repoRoot, "internal", "cli", "stub.go")
	if _, err := os.Stat(stub); err != nil {
		t.Fatalf("stub.go not found at %s: %v", stub, err)
	}
	return stub
}

func setOf(xs []string) map[string]bool {
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}
