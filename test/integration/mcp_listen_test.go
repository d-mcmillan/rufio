package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// seedAgentInbox drops n crafted @thought records (distinct, ascending ts)
// into live/inbox/agent-a. Direct file-drop is the same seeding the
// stream/listen tests use; Poll is a synchronous walk so there is no
// fsnotify race to defend against here.
func seedAgentInbox(t *testing.T, root, agent string, n int) {
	t.Helper()
	inbox := filepath.Join(root, "live", "inbox", agent)
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		body := fmt.Sprintf(
			"@thought|id:%d-seed|author:agent-b|type:hypothesis|subject:order:%d|content:msg-%d|scope:fleet|ts:2026-05-12T12:00:0%dZ|ttl:0\n",
			i, i, i, i,
		)
		p := filepath.Join(inbox, fmt.Sprintf("%d-seed.gdl", i))
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func eventsOf(t *testing.T, structured map[string]any) []any {
	t.Helper()
	evs, ok := structured["events"].([]any)
	if !ok {
		t.Fatalf("listen result has no events array: %#v", structured)
	}
	return evs
}

func nextCursorOf(t *testing.T, structured map[string]any) string {
	t.Helper()
	nc, ok := structured["next_cursor"].(string)
	if !ok {
		t.Fatalf("listen result has no next_cursor string: %#v", structured)
	}
	return nc
}

// TestMCP_Listen_PaginatesAndIsIdempotent drives the listen tool over the
// stdio server: a full poll returns all seeded events ordered with a
// non-empty cursor; re-polling from that cursor returns zero events and the
// SAME cursor (idempotent); a max:1 poll returns exactly one event and
// advances the cursor.
func TestMCP_Listen_PaginatesAndIsIdempotent(t *testing.T) {
	root := initProject(t)
	seedAgentInbox(t, root, "agent-a", 3)

	c := startMCP(t, root, "agent-a")

	// Full poll: all 3, ordered by (ts,path), non-empty cursor.
	full := c.callTool(t, "listen", map[string]any{})
	evs := eventsOf(t, full)
	if len(evs) != 3 {
		t.Fatalf("full poll returned %d events, want 3", len(evs))
	}
	for i, e := range evs {
		m := e.(map[string]any)
		wantSubj := fmt.Sprintf("order:%d", i+1)
		if m["subject"] != wantSubj {
			t.Fatalf("event[%d].subject = %v, want %s (not (ts,path)-ordered)", i, m["subject"], wantSubj)
		}
		if m["_type"] != "thought" {
			t.Fatalf("event[%d]._type = %v, want thought (Event schema reused verbatim)", i, m["_type"])
		}
	}
	cur := nextCursorOf(t, full)
	if cur == "" {
		t.Fatal("next_cursor after a non-empty poll must be non-empty")
	}

	// Idempotent re-poll: zero events, SAME cursor.
	repoll := c.callTool(t, "listen", map[string]any{"cursor": cur})
	if got := eventsOf(t, repoll); len(got) != 0 {
		t.Fatalf("idempotent re-poll returned %d events, want 0", len(got))
	}
	if rc := nextCursorOf(t, repoll); rc != cur {
		t.Fatalf("idempotent re-poll changed cursor: %q -> %q", cur, rc)
	}

	// Paged poll: max=1 from the start → exactly one, advancing cursor.
	page1 := c.callTool(t, "listen", map[string]any{"max": 1})
	if got := eventsOf(t, page1); len(got) != 1 {
		t.Fatalf("max:1 poll returned %d events, want 1", len(got))
	}
	p1cur := nextCursorOf(t, page1)
	if p1cur == "" || p1cur == cur {
		t.Fatalf("max:1 cursor must be non-empty and earlier than the full cursor: p1=%q full=%q", p1cur, cur)
	}
	page2 := c.callTool(t, "listen", map[string]any{"cursor": p1cur, "max": 1})
	p2evs := eventsOf(t, page2)
	if len(p2evs) != 1 {
		t.Fatalf("max:1 page2 returned %d events, want 1", len(p2evs))
	}
	if m := p2evs[0].(map[string]any); m["subject"] != "order:2" {
		t.Fatalf("page2 first event subject = %v, want order:2 (no overlap with page1)", m["subject"])
	}
}

// TestMCP_Listen_InvalidTypeIsToolError confirms the listen tool replicates
// the CLI's --types validation (recall.ValidateTypes) and surfaces it as a
// mapped tool error rather than crashing the server.
func TestMCP_Listen_InvalidTypeIsToolError(t *testing.T) {
	root := initProject(t)
	c := startMCP(t, root, "agent-a")
	resp := c.rpc(t, "tools/call", map[string]any{
		"name":      "listen",
		"arguments": map[string]any{"types": "bogus"},
	})
	if _, bad := resp["error"]; bad {
		t.Fatalf("listen errored at protocol level (server should stay up): %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected an isError tool result for an invalid --types value, got: %#v", result)
	}
	// Server must still be alive: a follow-up call succeeds.
	ok := c.callTool(t, "listen", map[string]any{})
	_ = eventsOf(t, ok)
}
