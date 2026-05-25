// testhelpers_test.go — shared test fixtures relocated out of the
// legacy panes_test.go ahead of the G4 legacy-TUI atomic delete.
//
// writeChannelMeta and writeGoalActive were declared in panes_test.go
// (delete-set) but are also consumed by the surviving keep-set test
// file live_tabs_test.go. They are moved here so the legacy delete-set
// can be removed atomically with zero compile breakage.
//
// The bodies are behavior-identical to the originals: the only change
// is the trivial one-line wrapper gdlRender(rec) (== gdl.RenderLine(rec)
// by its own definition, and which dies with panes_test.go) is replaced
// by the direct gdl.RenderLine(rec) call — the same idiom the surviving
// keep-set tests already use directly (live_tabs_test.go, watch_test.go,
// project_test.go, live_mesh_test.go).
package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
)

// writeChannelMeta writes a minimal active channel meta.gdl + parent
// dirs. Used by reveal-raw and initial-walk tests.
func writeChannelMeta(t *testing.T, root, chID, opener, target, topic, intent string) {
	t.Helper()
	dir := filepath.Join(root, "live", "channels", "active", chID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := channels.BuildMetaRecord(chID, opener, target, topic, intent, "2026-05-14T00:00:00Z")
	contents := gdl.RenderLine(rec) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.gdl"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeGoalActive writes a minimal active goal file. Used by
// initial-walk tests.
func writeGoalActive(t *testing.T, root, id, author, statement string) {
	t.Helper()
	dir := filepath.Join(root, "live", "goals", string(goal.StateActive))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := goal.BuildGoalRecord(id, author, statement, "", "", "agent", "2026-05-14T00:00:00Z")
	contents := gdl.RenderLine(rec) + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
