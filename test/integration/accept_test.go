package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// chIDPattern matches the canonical channel-id shape minted by
// channels.GenerateID: ch-<unix-millis>-<rand6>.
var chIDPattern = regexp.MustCompile(`^ch-\d+-[a-z0-9]{6}$`)

// findActiveChannelDir returns the single channel-id directory under
// live/channels/active/. Fails the test if zero or more than one exist.
func findActiveChannelDir(t *testing.T, root string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "channels", "active", "ch-*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob channels: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 active channel dir, got %d (%v)", len(matches), matches)
	}
	return matches[0]
}

func TestAccept_HappyPath_MovesPendingAndCreatesChannel(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "churn-strategy", "lets chat")

	res := testutil.RunCLI(t, []string{"accept", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	// 1. Pending file gone.
	pendingPath := filepath.Join(root, "live", "summons", "pending", id+".gdl")
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("pending file still exists after accept: err=%v", err)
	}

	// 2. Accepted file present with @summon + @accept records.
	acceptedPath := filepath.Join(root, "live", "summons", "accepted", id+".gdl")
	bs, err := os.ReadFile(acceptedPath)
	if err != nil {
		t.Fatalf("accepted file missing: %v", err)
	}
	for _, want := range []string{"@summon|", "@accept|", "by:agent-b", "channel:ch-"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("accepted file missing %q.\n%s", want, bs)
		}
	}

	// 3. Channel meta exists with @channel record.
	chDir := findActiveChannelDir(t, root)
	chID := filepath.Base(chDir)
	if !chIDPattern.MatchString(chID) {
		t.Errorf("channel-id %q does not match canonical shape", chID)
	}
	metaBytes, err := os.ReadFile(filepath.Join(chDir, "meta.gdl"))
	if err != nil {
		t.Fatalf("read meta.gdl: %v", err)
	}
	for _, want := range []string{
		"@channel|",
		"opener:agent-a",
		"target:agent-b",
		"topic:churn-strategy",
		"intent:lets chat",
		"id:" + chID,
	} {
		if !strings.Contains(string(metaBytes), want) {
			t.Errorf("meta.gdl missing %q.\n%s", want, metaBytes)
		}
	}
}

func TestAccept_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "topic-x", "reason-y")

	res := testutil.RunCLI(t, []string{"accept", id, "--json"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "accept" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["summon-id"] != id {
		t.Errorf("summon-id=%v want %s", got["summon-id"], id)
	}
	chRaw, ok := got["channel"].(string)
	if !ok || !chIDPattern.MatchString(chRaw) {
		t.Errorf("channel=%v does not match canonical shape", got["channel"])
	}
	if got["by"] != "agent-b" {
		t.Errorf("by=%v", got["by"])
	}
	if _, ok := got["ts"].(string); !ok {
		t.Errorf("ts missing or non-string: %v", got["ts"])
	}
}

func TestAccept_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{"accept", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// H3d (#125): echo prefix normalized "accepted:" → "accept: ".
	if !strings.Contains(res.Stdout, "accept: summon-id="+id+" channel=ch-") {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio accept:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
}

func TestAccept_WrongAgent_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	// agent-c is not the target — auth check must precede any write.
	res := testutil.RunCLI(t, []string{"accept", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-c"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, `only "agent-b" may respond`) {
		t.Errorf("stderr=%q (expected mention of agent-b as the only valid responder)", res.Stderr)
	}
	// Pending file untouched.
	pendingPath := filepath.Join(root, "live", "summons", "pending", id+".gdl")
	if _, err := os.Stat(pendingPath); err != nil {
		t.Errorf("pending file removed after unauthorised accept: %v", err)
	}
	// No channel meta created — auth fails before WriteMeta.
	chDir := filepath.Join(root, "live", "channels", "active")
	if entries, _ := os.ReadDir(chDir); len(entries) != 0 {
		t.Errorf("orphan channel dirs created on unauthorised accept: %v", entries)
	}
}

func TestAccept_NoSuchSummon_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"accept", "1727000000-fake12"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such summon") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestAccept_AlreadyDeclined_Exit1 covers D15.10: a summon that is no
// longer pending surfaces as NoSuchSummon regardless of its terminal
// state.
func TestAccept_AlreadyDeclined_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")

	// Decline first via the real CLI.
	dec := testutil.RunCLI(t, []string{"decline", id, "--reason=nope"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if dec.Code != 0 {
		t.Fatalf("pre-decline failed: exit=%d stderr=%q", dec.Code, dec.Stderr)
	}

	res := testutil.RunCLI(t, []string{"accept", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such summon") {
		t.Errorf("stderr=%q (expected NoSuchSummonError per D15.10)", res.Stderr)
	}
}

// TestAccept_AlreadyAccepted_Idempotency_Exit1 covers D15.10: the second
// accept call surfaces NoSuchSummon. The first accept's channel meta
// must remain on disk untouched.
func TestAccept_AlreadyAccepted_Idempotency_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")

	first := testutil.RunCLI(t, []string{"accept", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if first.Code != 0 {
		t.Fatalf("first accept failed: exit=%d stderr=%q", first.Code, first.Stderr)
	}
	chDirBefore := findActiveChannelDir(t, root)
	metaBefore, err := os.ReadFile(filepath.Join(chDirBefore, "meta.gdl"))
	if err != nil {
		t.Fatalf("read meta before second accept: %v", err)
	}

	second := testutil.RunCLI(t, []string{"accept", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if second.Code != 1 {
		t.Fatalf("second accept: exit=%d stderr=%q", second.Code, second.Stderr)
	}
	if !strings.Contains(second.Stderr, "no such summon") {
		t.Errorf("stderr=%q (expected NoSuchSummonError per D15.10)", second.Stderr)
	}

	// First channel meta still present and unchanged.
	chDirAfter := findActiveChannelDir(t, root)
	if chDirAfter != chDirBefore {
		t.Errorf("channel dir changed after second accept: before=%s after=%s", chDirBefore, chDirAfter)
	}
	metaAfter, err := os.ReadFile(filepath.Join(chDirAfter, "meta.gdl"))
	if err != nil {
		t.Fatalf("read meta after second accept: %v", err)
	}
	if string(metaBefore) != string(metaAfter) {
		t.Errorf("meta.gdl mutated by failed second accept.\nbefore: %s\nafter: %s", metaBefore, metaAfter)
	}
}

func TestAccept_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	// Seed under agent-a so the summon-id is real. Identity lookup
	// fires before any state move, so the file stays untouched.
	id := mustSeedSummon(t, root, "agent-a", "agent-b", "t", "i")
	res := testutil.RunCLI(t, []string{"accept", id}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio accept:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestAccept_NotInProject_Exit1(t *testing.T) {
	// Bare tempdir — no rufio.gdl present.
	root := mkProject(t)
	res := testutil.RunCLI(t, []string{"accept", "1727000000-fake12"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}
