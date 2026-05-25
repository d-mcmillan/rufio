package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// mustSeedSummon shells out to the real `rufio summon` command as `from`
// to drop a pending summon at live/summons/pending/<id>.gdl, returning the
// freshly generated summon-id. Mirrors mustWriteThought's diff-the-pending
// directory trick so repeated calls in one test don't have to track ids.
//
// T5 (accept) will need this same shape; the implementer for T5 can either
// copy it or refactor to a shared helper. Kept local here so we don't
// pre-empt that decision.
func mustSeedSummon(t *testing.T, root, from, to, topic, intent string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "summons", "pending", "*.gdl")
	before, _ := filepath.Glob(pattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"summon", to, "--topic=" + topic, "--intent=" + intent,
	}, root, map[string]string{"RUFIO_AGENT_ID": from})
	if res.Code != 0 {
		t.Fatalf("seed summon failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	var fresh []string
	for _, p := range after {
		if !beforeSet[p] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("seed summon did not produce exactly one new file: got %d", len(fresh))
	}
	return strings.TrimSuffix(filepath.Base(fresh[0]), ".gdl")
}

func TestDecline_HappyPath_MovesPendingToDeclined(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "churn-strategy", "let's chat")

	res := testutil.RunCLI(t, []string{
		"decline", id, "--reason=not interested",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	declinedPath := filepath.Join(root, "live", "summons", "declined", id+".gdl")
	bs, err := os.ReadFile(declinedPath)
	if err != nil {
		t.Fatalf("declined file missing: %v", err)
	}
	for _, want := range []string{"@summon|", "@decline|", "reason:not interested", "by:agent-b"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("declined file missing %q.\n%s", want, bs)
		}
	}

	pendingPath := filepath.Join(root, "live", "summons", "pending", id+".gdl")
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("pending file still exists after decline: err=%v", err)
	}
}

func TestDecline_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-x", "reason-y")
	res := testutil.RunCLI(t, []string{
		"decline", id, "--reason=busy", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "decline" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["summon-id"] != id {
		t.Errorf("summon-id=%v want %s", got["summon-id"], id)
	}
	if got["by"] != "agent-b" {
		t.Errorf("by=%v", got["by"])
	}
	if got["reason"] != "busy" {
		t.Errorf("reason=%v", got["reason"])
	}
}

func TestDecline_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	res := testutil.RunCLI(t, []string{
		"decline", id, "--reason=r",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// H3d (#125): echo prefix normalized "declined:" → "decline: ".
	if !strings.Contains(res.Stdout, "decline: summon-id="+id) {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio decline:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
}

func TestDecline_MissingReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	res := testutil.RunCLI(t, []string{"decline", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestDecline_EmptyReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	res := testutil.RunCLI(t, []string{
		"decline", id, "--reason=   ",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestDecline_WrongAgent_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	// agent-c (not the target) tries to decline.
	res := testutil.RunCLI(t, []string{
		"decline", id, "--reason=nope",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-c"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, `only "agent-b" may respond`) {
		t.Errorf("stderr=%q (expected mention of agent-b as the only valid responder)", res.Stderr)
	}
	// Pending file must still exist; no side effect on unauthorised attempt.
	pendingPath := filepath.Join(root, "live", "summons", "pending", id+".gdl")
	if _, err := os.Stat(pendingPath); err != nil {
		t.Errorf("pending file removed after unauthorised decline: %v", err)
	}
}

func TestDecline_NoSuchSummon_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"decline", "1727000000-fake12", "--reason=r",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such summon") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestDecline_AlreadyAccepted_Exit1 covers D15.10: accept/decline are only
// valid against pending summons. We pre-move the file to accepted/ to
// simulate "already accepted" without depending on T5's accept command.
func TestDecline_AlreadyAccepted_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")

	// Move pending → accepted manually. We don't need to append an @accept
	// record here — D15.10 only cares that the summon is no longer pending.
	pendingPath := filepath.Join(root, "live", "summons", "pending", id+".gdl")
	acceptedDir := filepath.Join(root, "live", "summons", "accepted")
	if err := os.MkdirAll(acceptedDir, 0o755); err != nil {
		t.Fatalf("mkdir accepted: %v", err)
	}
	if err := os.Rename(pendingPath, filepath.Join(acceptedDir, id+".gdl")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	res := testutil.RunCLI(t, []string{
		"decline", id, "--reason=too late",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such summon") {
		t.Errorf("stderr=%q (expected NoSuchSummonError per D15.10)", res.Stderr)
	}
}

func TestDecline_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	// Seed under agent-a so we have a real summon-id; the decline-side
	// identity lookup fails before we touch the file anyway, but the
	// summon-id keeps the test honest if the order ever changes.
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	res := testutil.RunCLI(t, []string{
		"decline", id, "--reason=r",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio decline:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}
