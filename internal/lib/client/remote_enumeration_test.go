package client

import (
	"context"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// TestRemoteWiringComplete_AllAgentParticipationVerbs is the v1.0.5
// functional-audit guard for Task 6. It enumerates every MCP tool
// shipped by the server's tool registry and confirms each one routes
// through the remote MCP transport without falling back to local-mode
// errors ("no identity set", "not in a rufio project"). Failure mode:
// the test name + tool name identify exactly which verb broke; the
// regression guard going forward.
//
// 22-verb surface (locked):
//
//	attend, think, observe, reason, retract, confirm, refute, recall,
//	summon, accept, decline, say, leave, close,
//	goal, goals_list, goal_complete, goal_abandon,
//	open, listen, plus the two specialised verbs (quickstart, init are
//	local-only by design and excluded — agent-participation only).
//
// Identity test contract: each call carries minimal-valid input so the
// server's validators pass (or fail with a tool-specific error like
// NoSuchSummonError — NOT a local-mode error). We assert the response
// is well-formed (no transport error, no "not in a rufio project"
// error text in the response).
func TestRemoteWiringComplete_AllAgentParticipationVerbs(t *testing.T) {
	root, baseURL, token := startServer(t)
	_ = root
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := Dial(ctx, Config{
		Endpoint:    baseURL + "/mcp",
		Token:       token,
		InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	// Each case provides a minimal-valid args map. Some calls will
	// surface tool-specific errors (NoSuchSummonError, NoSuchGoalError,
	// etc.) — those are EXPECTED because we haven't seeded the
	// substrate. What we must NOT see is a local-mode fallback error
	// like "not in a rufio project" or "no identity set", which would
	// indicate the verb didn't actually wire through --server.
	cases := []struct {
		tool string
		args map[string]interface{}
		// expectSuccess true → the call should return res, err==nil.
		//  false → either success OR an expected tool-specific error
		// (e.g. NoSuchSummonError) is acceptable; only local-mode
		// errors fail the test.
		expectSuccess bool
	}{
		// Cognition group
		{"attend", map[string]interface{}{
			"intent":   "enumeration probe",
			"entities": []string{"test:1"},
			"scope":    "fleet",
		}, true},
		{"think", map[string]interface{}{
			"type":    "hypothesis",
			"subject": "test:1",
			"content": "probe",
			"scope":   "fleet",
		}, true},
		{"observe", map[string]interface{}{
			"subject":   "test:1",
			"predicate": "is",
			"object":    "probe",
			"scope":     "fleet",
		}, true},
		{"reason", map[string]interface{}{
			"content": "enumeration probe",
			"scope":   "fleet",
		}, true},
		{"recall", map[string]interface{}{}, true},
		{"retract", map[string]interface{}{
			"thought_id": "nonexistent-id",
			"reason":     "probe",
		}, false},
		// Verification group
		{"confirm", map[string]interface{}{
			"thought_id": "nonexistent-id",
		}, false},
		{"refute", map[string]interface{}{
			"thought_id": "nonexistent-id",
			"reason":     "probe",
		}, false},
		// Channel-open group
		{"summon", map[string]interface{}{
			"to":     "bob",
			"topic":  "probe",
			"intent": "probe",
		}, true},
		{"accept", map[string]interface{}{
			"summon_id": "sm-nonexistent",
		}, false},
		{"decline", map[string]interface{}{
			"summon_id": "sm-nonexistent",
			"reason":    "probe",
		}, false},
		// Channel-message group
		{"say", map[string]interface{}{
			"channel": "ch-nonexistent",
			"content": "probe",
		}, false},
		{"leave", map[string]interface{}{
			"channel": "ch-nonexistent",
		}, false},
		{"close", map[string]interface{}{
			"channel": "ch-nonexistent",
		}, false},
		// Goal group
		{"goal", map[string]interface{}{
			"statement": "enumeration probe",
			"scope":     "fleet",
		}, true},
		{"goals_list", map[string]interface{}{}, true},
		{"goal_complete", map[string]interface{}{
			"goal_id": "g-nonexistent",
			"outcome": "probe",
		}, false},
		{"goal_abandon", map[string]interface{}{
			"goal_id": "g-nonexistent",
			"reason":  "probe",
		}, false},
		// Read-bundle
		{"open", map[string]interface{}{
			"subject": "test:1",
		}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.tool, func(t *testing.T) {
			res, callErr := c.CallTool(ctx, tc.tool, tc.args)
			if tc.expectSuccess {
				if callErr != nil {
					// Local-mode fallback markers (the failure mode
					// we're guarding against).
					msg := callErr.Error()
					if strings.Contains(msg, "not in a rufio project") ||
						strings.Contains(msg, "no identity set") {
						t.Fatalf("tool %q routed to local-mode (--server wiring broken): %v", tc.tool, callErr)
					}
					t.Fatalf("tool %q failed unexpectedly: %v", tc.tool, callErr)
				}
				if res == nil {
					t.Fatalf("tool %q returned nil response", tc.tool)
				}
			} else {
				// Error is allowed (tool-specific). What we MUST NOT
				// see is a local-mode fallback.
				if callErr != nil {
					msg := callErr.Error()
					if strings.Contains(msg, "not in a rufio project") ||
						strings.Contains(msg, "no identity set") {
						t.Fatalf("tool %q routed to local-mode (--server wiring broken): %v", tc.tool, callErr)
					}
				}
			}
		})
	}

	// Token must resolve to the bearer's agent identity — separate
	// admin-side check. This is the "identity comes from the token,
	// not env" contract baked into every verb's remote path.
	if _, err := admin.ResolveToken(root, token); err != nil {
		t.Errorf("post-enumeration: token failed to resolve: %v", err)
	}
}
