package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestRetractPropagation_AppendsToSeededInbox exercises the
// PropagateRetract engine end-to-end through a synthetic routing setup.
// The full retract → daemon-watches → propagate flow will be tested in
// PR #11 when RoutingHandler exists; for now we manually seed the inbox.
func TestRetractPropagation_AppendsToSeededInbox(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "to retract")

	// Synthetically seed an inbox copy. In production, RoutingHandler
	// (PR #11) would create this when the thought first lands in
	// live/outbox/.
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")
	bs, _ := os.ReadFile(src)
	if err := os.WriteFile(filepath.Join(inboxDir, id+".gdl"), bs, 0o644); err != nil {
		t.Fatal(err)
	}

	// Run retract via the real CLI — writes live/retracted/<id>.gdl.
	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=outdated",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("retract: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	// Exercise the propagator directly. In production, the dev daemon
	// would fire this on the live/retracted/<id>.gdl create event.
	if err := retract.PropagateRetract(root, id); err != nil {
		t.Fatalf("PropagateRetract: %v", err)
	}

	bs, _ = os.ReadFile(filepath.Join(inboxDir, id+".gdl"))
	content := string(bs)
	if !strings.Contains(content, "@thought|") {
		t.Errorf("inbox lost @thought:\n%s", content)
	}
	if !strings.Contains(content, "@retract|target:"+id) {
		t.Errorf("inbox missing @retract:\n%s", content)
	}
}
