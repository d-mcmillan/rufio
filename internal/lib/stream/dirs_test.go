package stream

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestListenDirs_IncludesPromoted pins that the canonical listen
// walk surface includes live/promoted/ — the regression source for
// the PR #188 MCP symmetry gate. A future drop of live/promoted/
// from this list would silently kill auto-promote event surfacing
// on both transports; this test catches it loudly.
func TestListenDirs_IncludesPromoted(t *testing.T) {
	dirs := ListenDirs("agent-a")
	found := false
	for _, d := range dirs {
		if d == "live/promoted" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListenDirs is missing live/promoted; got %v", dirs)
	}
}

// TestListenDirs_AgentInbox pins per-agent inbox scoping. The
// inbox path is the only agent-specific entry — every other dir is
// project-wide and the agent argument has no effect on it.
func TestListenDirs_AgentInbox(t *testing.T) {
	dirs := ListenDirs("agent-a")
	want := filepath.Join("live", "inbox", "agent-a")
	found := false
	for _, d := range dirs {
		if d == want {
			found = true
		}
	}
	if !found {
		t.Errorf("ListenDirs missing per-agent inbox %q; got %v", want, dirs)
	}
}

// TestListenDirs_NoEmptyEntries pins basic sanity: no empty strings
// or stray duplicates. WalkDir on an empty string would explode the
// downstream walker; a duplicate would deliver each event twice.
func TestListenDirs_NoEmptyEntries(t *testing.T) {
	dirs := ListenDirs("agent-a")
	seen := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			t.Errorf("ListenDirs has empty entry; got %v", dirs)
		}
		if seen[d] {
			t.Errorf("ListenDirs has duplicate %q; got %v", d, dirs)
		}
		seen[d] = true
	}
}

// TestListenDirs_CanonicalSurface pins the v1.0.3 listen-surface
// shape: per-agent inbox + the 7 project-wide subtrees. A test
// failure here is a deliberate change signal — update the listen
// docstring in cli/listen.go AND the MCP tool description in
// mcp/tools_listen.go in the same commit if the set evolves.
func TestListenDirs_CanonicalSurface(t *testing.T) {
	dirs := ListenDirs("agent-x")
	want := []string{
		filepath.Join("live", "inbox", "agent-x"),
		"live/outbox",
		"live/channels/active",
		"live/summons/pending",
		"live/confirms",
		"live/retracted",
		"live/reasoning",
		"live/promoted",
	}
	if len(dirs) != len(want) {
		t.Fatalf("ListenDirs len=%d want %d; got=%v want=%v", len(dirs), len(want), dirs, want)
	}
	for i, w := range want {
		if dirs[i] != w {
			t.Errorf("ListenDirs[%d]=%q want %q", i, dirs[i], w)
		}
	}
}
