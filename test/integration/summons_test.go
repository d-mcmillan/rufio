package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// movePendingToAccepted is a test-only helper that promotes a pending
// summon file to the accepted/ directory without going through the
// accept command (which T5 owns). We don't need the @accept audit
// record for these tests — `summons list` only cares which directory
// the file lives in.
func movePendingToAccepted(t *testing.T, root, id string) {
	t.Helper()
	pending := filepath.Join(root, "live", "summons", "pending", id+".gdl")
	acceptedDir := filepath.Join(root, "live", "summons", "accepted")
	if err := os.MkdirAll(acceptedDir, 0o755); err != nil {
		t.Fatalf("mkdir accepted: %v", err)
	}
	if err := os.Rename(pending, filepath.Join(acceptedDir, id+".gdl")); err != nil {
		t.Fatalf("rename %s → accepted: %v", id, err)
	}
}

// TestSummonsList_HappyPath_DefaultPendingForCurrentAgent: agent-a opens
// two summons to agent-b; `summons list` as agent-a returns both pending
// summons (default scope = current identity, default filter = pending).
func TestSummonsList_HappyPath_DefaultPendingForCurrentAgent(t *testing.T) {
	root := initProject(t)
	id1 := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-one", "intent-one")
	id2 := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-two", "intent-two")

	res := testutil.RunCLI(t, []string{"summons", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id1) {
		t.Errorf("stdout missing id1=%s\n%s", id1, res.Stdout)
	}
	if !strings.Contains(res.Stdout, id2) {
		t.Errorf("stdout missing id2=%s\n%s", id2, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "pending") {
		t.Errorf("stdout missing state column 'pending'\n%s", res.Stdout)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("want 2 lines, got %d:\n%s", len(lines), res.Stdout)
	}
}

// TestSummonsList_PendingOnly_ExcludesAccepted: seed one pending and one
// accepted; default list shows only the pending one.
func TestSummonsList_PendingOnly_ExcludesAccepted(t *testing.T) {
	root := initProject(t)
	pendingID := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-pending", "i")
	acceptedID := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-accepted", "i")
	movePendingToAccepted(t, root, acceptedID)

	res := testutil.RunCLI(t, []string{"summons", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, pendingID) {
		t.Errorf("stdout missing pendingID=%s\n%s", pendingID, res.Stdout)
	}
	if strings.Contains(res.Stdout, acceptedID) {
		t.Errorf("stdout includes acceptedID=%s but should not\n%s", acceptedID, res.Stdout)
	}
}

// TestSummonsList_AllFlag_IncludesAllStates: same seed; --all returns
// both pending and accepted.
func TestSummonsList_AllFlag_IncludesAllStates(t *testing.T) {
	root := initProject(t)
	pendingID := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-pending", "i")
	acceptedID := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-accepted", "i")
	movePendingToAccepted(t, root, acceptedID)

	res := testutil.RunCLI(t, []string{"summons", "list", "--all"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, pendingID) {
		t.Errorf("stdout missing pendingID=%s\n%s", pendingID, res.Stdout)
	}
	if !strings.Contains(res.Stdout, acceptedID) {
		t.Errorf("stdout missing acceptedID=%s\n%s", acceptedID, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "pending") {
		t.Errorf("stdout missing 'pending' state column\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "accepted") {
		t.Errorf("stdout missing 'accepted' state column\n%s", res.Stdout)
	}
}

// TestSummonsList_AsFilterScopesToTarget: agent-a → agent-b summon;
// `summons list --as=agent-b` returns it (b is the target), and
// `--as=agent-c` (uninvolved) returns nothing.
func TestSummonsList_AsFilterScopesToTarget(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-x", "intent-x")

	res := testutil.RunCLI(t, []string{"summons", "list", "--as=agent-b"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id) {
		t.Errorf("stdout missing id=%s for --as=agent-b (the target)\n%s", id, res.Stdout)
	}

	res2 := testutil.RunCLI(t, []string{"summons", "list", "--as=agent-c"}, root, nil)
	if res2.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res2.Code, res2.Stderr)
	}
	if strings.TrimSpace(res2.Stdout) != "" {
		t.Errorf("stdout non-empty for --as=agent-c (uninvolved):\n%s", res2.Stdout)
	}
}

// TestSummonsList_JSONOutput_HasExpectedShape: --json emits JSONL with
// every field promised in D15.8.
func TestSummonsList_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-json", "intent-json")

	res := testutil.RunCLI(t, []string{"summons", "list", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 JSONL line, got %d:\n%s", len(lines), res.Stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, lines[0])
	}
	checks := map[string]interface{}{
		"_type":    "summon",
		"_version": "1",
		"id":       id,
		"from":     "agent-a",
		"to":       "agent-b",
		"topic":    "topic-json",
		"intent":   "intent-json",
		"state":    "pending",
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s=%v want %v", k, got[k], want)
		}
	}
	if _, ok := got["ts"]; !ok {
		t.Errorf("missing 'ts' field: %v", got)
	}
	if _, ok := got["ttl"]; !ok {
		t.Errorf("missing 'ttl' field: %v", got)
	}
}

// TestSummonsList_NoIdentityNoFlag_Exit1: no env, no --as, no
// identity.local.gdl → NoIdentityError, exit 1, single-prefix invariant.
func TestSummonsList_NoIdentityNoFlag_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"summons", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q (want 'no identity set')", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio summons:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestSummonsList_EmptyResult_NoOutput: no summons in the project →
// exit 0, stdout empty (no header, not even chatter).
func TestSummonsList_EmptyResult_NoOutput(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"summons", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout should be empty for no-summons case:\n%q", res.Stdout)
	}
}

// TestSummonsList_AsFlagOverridesIdentity: env=agent-a, --as=agent-b
// scopes the result to agent-b's view. The summon (a→b) is present
// because b is the target.
func TestSummonsList_AsFlagOverridesIdentity(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-z", "intent-z")
	// Add a summon that agent-b is NOT party to — should be filtered out.
	excluded := mustSeedSummon(t, root, "agent-x", "agent-y", "topic-other", "i")

	res := testutil.RunCLI(t, []string{"summons", "list", "--as=agent-b"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id) {
		t.Errorf("stdout missing id=%s (agent-b is the target)\n%s", id, res.Stdout)
	}
	if strings.Contains(res.Stdout, excluded) {
		t.Errorf("stdout includes excluded=%s (agent-b is not party)\n%s", excluded, res.Stdout)
	}
}

// TestSummonsList_AcceptedSummon_ShowsChannelId_JSON covers #140: when a
// summon has been accepted, the resulting channel-id must surface on the
// JSON row. Cold agents (and B, who opened the summon) need a discoverable
// path from "I see an accepted summon" → "I know which channel to say into."
// The channel-id lives on the @accept record inside the on-disk file; we
// project it onto the row so callers don't have to crack the @accept
// audit themselves.
func TestSummonsList_AcceptedSummon_ShowsChannelId_JSON(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "churn-strategy", "chat")
	// Find the now-accepted summon's id.
	pattern := filepath.Join(root, "live", "summons", "accepted", "*.gdl")
	matches, _ := filepath.Glob(pattern)
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 accepted summon, got %d", len(matches))
	}
	summonID := strings.TrimSuffix(filepath.Base(matches[0]), ".gdl")

	res := testutil.RunCLI(t, []string{"summons", "list", "--all", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	var found map[string]interface{}
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		var row map[string]interface{}
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			t.Fatalf("invalid JSON: %v\n%q", err, ln)
		}
		if row["id"] == summonID {
			found = row
			break
		}
	}
	if found == nil {
		t.Fatalf("accepted summon %q not in JSON output:\n%s", summonID, res.Stdout)
	}
	if found["state"] != "accepted" {
		t.Errorf("state=%v, want accepted", found["state"])
	}
	if found["channel"] != chID {
		t.Errorf("channel=%v want %q", found["channel"], chID)
	}
	// decline_reason key is present (null) for shape stability.
	if v, ok := found["decline_reason"]; !ok || v != nil {
		t.Errorf("decline_reason=%v (present=%v); want present-and-null", v, ok)
	}
}

// TestSummonsList_AcceptedSummon_ShowsChannelId_Text mirrors the JSON
// assertion for human columnar output: accepted rows must surface
// `channel:<ch-id>` so a scroll-back-loss recovery path doesn't require
// shelling to live/.
func TestSummonsList_AcceptedSummon_ShowsChannelId_Text(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{"summons", "list", "--all"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "channel:"+chID) {
		t.Errorf("stdout missing channel:%s in accepted row:\n%s", chID, res.Stdout)
	}
}

// TestSummonsList_DeclinedSummon_ShowsReason_JSON covers the parallel
// metadata join for the decline path: declined rows must carry the
// @decline.reason on the JSON row, so a cold agent can see WHY the
// counterparty declined without grepping live/summons/declined/.
func TestSummonsList_DeclinedSummon_ShowsReason_JSON(t *testing.T) {
	root := initProject(t)
	summonID := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	dec := testutil.RunCLI(t, []string{
		"decline", summonID, "--reason=busy right now",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if dec.Code != 0 {
		t.Fatalf("decline failed: exit=%d stderr=%q", dec.Code, dec.Stderr)
	}

	res := testutil.RunCLI(t, []string{"summons", "list", "--all", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 JSONL line, got %d:\n%s", len(lines), res.Stdout)
	}
	var row map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, lines[0])
	}
	if row["state"] != "declined" {
		t.Errorf("state=%v, want declined", row["state"])
	}
	if row["decline_reason"] != "busy right now" {
		t.Errorf("decline_reason=%v, want %q", row["decline_reason"], "busy right now")
	}
	// channel key present (null) for shape stability across states.
	if v, ok := row["channel"]; !ok || v != nil {
		t.Errorf("channel=%v (present=%v); want present-and-null on declined row", v, ok)
	}
}
