package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// setupServeProject scaffolds an initialised rufio project so the serve
// command can find rufio.gdl. Returns the root path.
func setupServeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{"init"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("init failed: %s", res.Stderr)
	}
	return root
}

func TestServe_RequiresTLS(t *testing.T) {
	root := setupServeProject(t)
	res := testutil.RunCLI(t, []string{"serve", "--port=18443"}, root, nil)
	if res.Code != 2 {
		t.Errorf("expected exit 2 without TLS, got %d (stderr=%s)", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "TLS") {
		t.Errorf("error should mention TLS; stderr=%s", res.Stderr)
	}
}

func TestServe_InsecureRequiresLocalhost(t *testing.T) {
	root := setupServeProject(t)
	res := testutil.RunCLI(t, []string{"serve", "--insecure", "--bind=0.0.0.0", "--port=18444"}, root, nil)
	if res.Code != 2 {
		t.Errorf("--insecure must require --bind=127.0.0.1, got exit %d (stderr=%s)", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "127.0.0.1") && !strings.Contains(res.Stderr, "localhost") {
		t.Errorf("error should mention localhost requirement; stderr=%s", res.Stderr)
	}
}

func TestServe_HelpListsRoutes(t *testing.T) {
	root := setupServeProject(t)
	res := testutil.RunCLI(t, []string{"serve", "--help"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("serve --help should succeed, got %d (stderr=%s)", res.Code, res.Stderr)
	}
	for _, want := range []string{"/health", "/mcp", "/listen", "Bearer"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("serve --help should mention %q; stdout=%s", want, res.Stdout)
		}
	}
}

// TestServe_RegisteredOnRoot pins the command into root's surface so a
// future refactor that accidentally drops it from NewRootCmd fails loud.
func TestServe_RegisteredOnRoot(t *testing.T) {
	root := setupServeProject(t)
	res := testutil.RunCLI(t, []string{"--help"}, root, nil)
	if !strings.Contains(res.Stdout, "serve") {
		t.Errorf("`rufio --help` should list serve; stdout=%s", res.Stdout)
	}
}

func TestServe_FixturePathsExist(t *testing.T) {
	// Sanity that t.TempDir + init produces the expected on-disk shape.
	// Keeps future test failures debuggable when this contract changes.
	root := setupServeProject(t)
	if _, err := os.Stat(filepath.Join(root, "rufio.gdl")); err != nil {
		t.Fatalf("rufio.gdl missing after init: %v", err)
	}
}
