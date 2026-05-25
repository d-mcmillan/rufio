package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
	"github.com/d-mcmillan/rufio/internal/testutil"
)

// readMeta is a tiny helper for the leave tests: reads the active meta.gdl
// for a channel and returns its contents as a string. Fails the test on
// any read error.
func readActiveMeta(t *testing.T, root, chID string) string {
	t.Helper()
	path := filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read active meta: %v", err)
	}
	return string(bs)
}

// countOccurrences counts how many times needle appears in haystack.
// Used to verify the idempotency invariant: a repeated `rufio leave` must
// NOT append a second @channel-leave|by:<agent> line.
func countOccurrences(haystack, needle string) int {
	return strings.Count(haystack, needle)
}

func TestLeave_HappyPath_AppendsLeaveRecord(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "churn-strategy", "lets chat")

	res := testutil.RunCLI(t, []string{
		"leave", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	contents := readActiveMeta(t, root, chID)
	if !strings.Contains(contents, "@channel|") {
		t.Errorf("meta.gdl missing @channel| header:\n%s", contents)
	}
	if !strings.Contains(contents, "@channel-leave|") {
		t.Errorf("meta.gdl missing @channel-leave| record:\n%s", contents)
	}
	if !strings.Contains(contents, "by:agent-b") {
		t.Errorf("meta.gdl missing by:agent-b:\n%s", contents)
	}
}

func TestLeave_OpenerCanLeave(t *testing.T) {
	// D16.14: BOTH opener and target may leave. The original opener is
	// not privileged with respect to leave — only close is opener-only.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"leave", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("opener leave failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	contents := readActiveMeta(t, root, chID)
	if !strings.Contains(contents, "@channel-leave|") {
		t.Errorf("meta.gdl missing @channel-leave|:\n%s", contents)
	}
	if !strings.Contains(contents, "by:agent-a") {
		t.Errorf("meta.gdl missing by:agent-a:\n%s", contents)
	}
}

func TestLeave_Idempotent_SecondLeaveIsNoop(t *testing.T) {
	// D16.4: AppendLeave is idempotent — a second leave by the same
	// agent MUST NOT produce a second @channel-leave record. We assert
	// this at the meta.gdl level (line count) since the CLI surface
	// doesn't distinguish first-leave from second-leave.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	for i := 0; i < 2; i++ {
		res := testutil.RunCLI(t, []string{
			"leave", chID,
		}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
		if res.Code != 0 {
			t.Fatalf("leave #%d failed: exit=%d stderr=%q", i+1, res.Code, res.Stderr)
		}
	}

	contents := readActiveMeta(t, root, chID)
	// Exactly one @channel-leave|by:agent-b line.
	if got := countOccurrences(contents, "@channel-leave|"); got != 1 {
		t.Errorf("expected exactly 1 @channel-leave| record after idempotent retry, got %d:\n%s", got, contents)
	}
	if got := countOccurrences(contents, "by:agent-b"); got != 1 {
		t.Errorf("expected exactly 1 by:agent-b field, got %d:\n%s", got, contents)
	}
}

func TestLeave_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"leave", chID, "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "channel-leave" {
		t.Errorf("_type=%v want channel-leave", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v want 1", got["_version"])
	}
	if got["channel"] != chID {
		t.Errorf("channel=%v want %s", got["channel"], chID)
	}
	if got["by"] != "agent-b" {
		t.Errorf("by=%v want agent-b", got["by"])
	}
	if _, ok := got["ts"].(string); !ok {
		t.Errorf("ts missing or non-string: %v", got["ts"])
	}
}

func TestLeave_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"leave", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	want := "left: channel=" + chID
	if !strings.Contains(res.Stdout, want) {
		t.Errorf("stdout missing %q: %q", want, res.Stdout)
	}
	// Single-prefix invariant: success path must NOT carry an error prefix.
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio leave:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
}

func TestLeave_NoSuchChannel_Exit1(t *testing.T) {
	root := initProject(t)
	// Fake but well-formed ch-id — must not collide with anything real.
	res := testutil.RunCLI(t, []string{
		"leave", "ch-1727000000-fake12",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such channel") {
		t.Errorf("stderr=%q (expected no such channel)", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio leave:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestLeave_ClosedChannel_Exit1(t *testing.T) {
	// D16.6: closed channels are gone for write purposes. A subsequent
	// `rufio leave` against a channel that has already been closed must
	// surface as NoSuchChannel — same shape as a channel that never
	// existed, since both are equally unwritable.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	// Close the channel directly via the lib (the `rufio close` CLI
	// command lands in PR #16 T5; the lib call exercises the same
	// audit-trail outcome).
	if err := channels.AppendClose(root, chID, "agent-a", versioning.NowISO()); err != nil {
		t.Fatalf("AppendClose: %v", err)
	}

	res := testutil.RunCLI(t, []string{
		"leave", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such channel") {
		t.Errorf("stderr=%q (expected no such channel for closed channel)", res.Stderr)
	}
}

func TestLeave_NonMember_Exit1(t *testing.T) {
	// D16.14: a third party who is neither opener nor target cannot
	// "leave" a channel they were never part of. Surface as
	// NotChannelMemberError; no write side effects.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"leave", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-c"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "is not a current member") {
		t.Errorf("stderr=%q (expected NotChannelMemberError)", res.Stderr)
	}
	// Meta must NOT contain a leave record for agent-c.
	contents := readActiveMeta(t, root, chID)
	if strings.Contains(contents, "by:agent-c") {
		t.Errorf("unauthorised leave produced audit trail:\n%s", contents)
	}
}

func TestLeave_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"leave", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio leave:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}
