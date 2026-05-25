package routing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/goal"
)

// Routing's two atomic-write sites (deliverToInbox ~:452 and
// deliverOverlapFile ~:417) both guard the write with a pre-write
// os.Stat(target) idempotency skip ("if target exists, return nil"). That
// makes the Rename-fail strand NOT deterministically injectable in-process:
// the only injection that keeps the parent dir writable (so the deferred
// Remove can demonstrably clean up) is pre-creating `target` as a non-empty
// directory — but os.Stat then succeeds and the helper returns nil before
// ever writing the tmp. The Rename-fail-path correctness of the identical
// one-line defer is proven by the pure-write helper tests (attention,
// thought, observation, identity, reason, retract.Write, autopromote,
// swarm, goal.WriteActive, channels.WriteMeta/WriteMessage,
// summon.WritePending). These two tests pin the SUCCESS path: the added
// defer must be a byte-identical no-op (record written, no *.tmp left).

func TestDeliverToInbox_SuccessNoTmp(t *testing.T) {
	root := t.TempDir()
	if err := deliverToInbox(root, "agent-a", "t-1", "agent-b", "@thought|id:t-1", "2026-05-12T00:00:00Z"); err != nil {
		t.Fatalf("deliverToInbox: unexpected error %v", err)
	}
	inboxDir := filepath.Join(root, "live", "inbox", "agent-a")
	if _, err := os.Stat(filepath.Join(inboxDir, "t-1.gdl")); err != nil {
		t.Errorf("delivered inbox file missing: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(inboxDir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp on success path: %v", matches)
	}
}

func TestDeliverOverlapFile_SuccessNoTmp(t *testing.T) {
	root := t.TempDir()
	pairs := []goal.OverlapPair{
		{Entity: "customer:5821", PeerGoal: goal.Goal{
			ID: "g-2", Author: "agent-b", Statement: "s", State: goal.StateActive,
		}},
	}
	if err := deliverOverlapFile(root, "agent-a", "agent-a", "g-2", "g-1", pairs, "2026-05-12T00:00:00Z"); err != nil {
		t.Fatalf("deliverOverlapFile: unexpected error %v", err)
	}
	inboxDir := filepath.Join(root, "live", "inbox", "agent-a")
	if _, err := os.Stat(filepath.Join(inboxDir, "g-1-overlap-g-2.gdl")); err != nil {
		t.Errorf("delivered overlap file missing: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(inboxDir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp on success path: %v", matches)
	}
}
