package cli

import (
	"testing"
)

// TestDefaultEventHandler_RoutesGoalActive_ToRouteGoalOverlap asserts
// that add events under live/goals/active/*.gdl reach
// routing.RouteGoalOverlap. The function reads the (missing) file and
// returns a *fs.PathError on ENOENT — non-nil error proves dispatch.
func TestDefaultEventHandler_RoutesGoalActive_ToRouteGoalOverlap(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "add", Path: "live/goals/active/1727000000-fake12.gdl"})
	if err == nil {
		t.Fatal("expected non-nil error from RouteGoalOverlap on missing file (proves dispatch reached the engine)")
	}
}

// TestDefaultEventHandler_IgnoresGoalsCompletedAbandoned — terminal-state
// goals don't trigger overlap detection. Defense against accidental
// wiring expansion.
func TestDefaultEventHandler_IgnoresGoalsCompletedAbandoned(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	for _, sub := range []string{"completed", "abandoned"} {
		err := h(FileEvent{Kind: "add", Path: "live/goals/" + sub + "/fake-id.gdl"})
		if err != nil {
			t.Errorf("%s: expected nil (no handler), got %v", sub, err)
		}
	}
}
