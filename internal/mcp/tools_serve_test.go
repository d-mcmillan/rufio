package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// initRoot scaffolds an empty rufio project root for the serve_status
// tool to read.
func initRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte("@config|name:test|version:1\n"), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	return root
}

func TestServeStatus_NoTokens(t *testing.T) {
	root := initRoot(t)
	// Build a server with no tokens minted. The serve_status closure
	// reads ListTokens, which should return an empty slice + nil err.
	// We exercise via the underlying lib call (the MCP handler itself
	// is harder to unit-test in isolation; the integration coverage
	// comes from the help-text test + the live-smoke run).
	toks, err := admin.ListTokens(root)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(toks) != 0 {
		t.Errorf("expected 0 tokens initially, got %d", len(toks))
	}
}

func TestServeStatus_WithTokens(t *testing.T) {
	root := initRoot(t)
	if _, _, err := admin.MintToken(root, "alice"); err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if _, _, err := admin.MintToken(root, "bob"); err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	toks, err := admin.ListTokens(root)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	active := 0
	for _, t := range toks {
		if !t.Revoked {
			active++
		}
	}
	if active != 2 {
		t.Errorf("expected 2 active tokens, got %d", active)
	}
}

func TestServeStatus_Registered(t *testing.T) {
	// Compile-time guard: the registerServeStatus function exists and
	// can be called against a resolved struct. The runtime behavior
	// is covered by the help-text test (TestMcpHelp_ListsAllTools)
	// and by the cross-machine smoke in task 13.
	_ = registerServeStatus
}
