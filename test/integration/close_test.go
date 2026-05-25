package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// readClosedMeta reads live/channels/closed/<ch-id>/meta.gdl and returns
// its contents as a string. Fails the test on any read error. Companion
// to readActiveMeta from leave_test.go.
func readClosedMeta(t *testing.T, root, chID string) string {
	t.Helper()
	path := filepath.Join(root, "live", "channels", "closed", chID, "meta.gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read closed meta: %v", err)
	}
	return string(bs)
}

func TestClose_HappyPath_RenamesActiveToClosed(t *testing.T) {
	// D16.5: close appends @channel-close to active meta.gdl AND renames
	// active/<ch-id>/ → closed/<ch-id>/ atomically under the channel
	// lock. After a successful close, active/ must not contain the
	// directory and closed/ must hold the full audit trail.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "churn-strategy", "lets chat")

	res := testutil.RunCLI(t, []string{
		"close", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	// active/<ch-id>/ must be gone.
	activeDir := filepath.Join(root, "live", "channels", "active", chID)
	if _, err := os.Stat(activeDir); !os.IsNotExist(err) {
		t.Errorf("active/%s/ still exists after close (err=%v)", chID, err)
	}

	// closed/<ch-id>/meta.gdl must have both @channel and @channel-close.
	contents := readClosedMeta(t, root, chID)
	if !strings.Contains(contents, "@channel|") {
		t.Errorf("closed meta missing @channel| header:\n%s", contents)
	}
	if !strings.Contains(contents, "@channel-close|") {
		t.Errorf("closed meta missing @channel-close| record:\n%s", contents)
	}
	if !strings.Contains(contents, "by:agent-a") {
		t.Errorf("closed meta missing by:agent-a:\n%s", contents)
	}
}

func TestClose_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"close", chID, "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "channel-close" {
		t.Errorf("_type=%v want channel-close", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v want 1", got["_version"])
	}
	if got["channel"] != chID {
		t.Errorf("channel=%v want %s", got["channel"], chID)
	}
	if got["by"] != "agent-a" {
		t.Errorf("by=%v want agent-a", got["by"])
	}
	if _, ok := got["ts"].(string); !ok {
		t.Errorf("ts missing or non-string: %v", got["ts"])
	}
}

func TestClose_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"close", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	want := "closed: channel=" + chID
	if !strings.Contains(res.Stdout, want) {
		t.Errorf("stdout missing %q: %q", want, res.Stdout)
	}
	// Single-prefix invariant: success path must NOT carry an error prefix.
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio close:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
}

func TestClose_NoSuchChannel_Exit1(t *testing.T) {
	root := initProject(t)
	// Fake but well-formed ch-id — must not collide with anything real.
	res := testutil.RunCLI(t, []string{
		"close", "ch-1727000000-fake12",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such channel") {
		t.Errorf("stderr=%q (expected no such channel)", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio close:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestClose_AlreadyClosed_Exit1(t *testing.T) {
	// D16.6: once gone, gone. A second close on an already-closed channel
	// must surface as NoSuchChannel — same shape as a channel that never
	// existed. The closed audit trail must remain untouched.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	first := testutil.RunCLI(t, []string{
		"close", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if first.Code != 0 {
		t.Fatalf("first close failed: exit=%d stderr=%q", first.Code, first.Stderr)
	}

	second := testutil.RunCLI(t, []string{
		"close", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if second.Code != 1 {
		t.Fatalf("second close: exit=%d stderr=%q", second.Code, second.Stderr)
	}
	if !strings.Contains(second.Stderr, "no such channel") {
		t.Errorf("stderr=%q (expected no such channel for already-closed)", second.Stderr)
	}
}

func TestClose_NotOpener_Exit1(t *testing.T) {
	// D16.5: only the opener may close. The target (and any third party)
	// must be rejected with NotChannelOpenerError. The channel must remain
	// in active/<ch-id>/ — a rejected close has no side effect.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"close", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// Typed error text: "agent agent-b cannot close channel <id>: only opener agent-a may close"
	if !strings.Contains(res.Stderr, "only opener agent-a may close") {
		t.Errorf("stderr=%q (expected NotChannelOpenerError text)", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio close:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}

	// Channel must still be live — no side effect from rejected attempt.
	activePath := filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")
	if _, err := os.Stat(activePath); err != nil {
		t.Errorf("active/%s/meta.gdl missing after rejected close: %v", chID, err)
	}
	closedPath := filepath.Join(root, "live", "channels", "closed", chID)
	if _, err := os.Stat(closedPath); !os.IsNotExist(err) {
		t.Errorf("closed/%s/ was created by rejected close (err=%v)", chID, err)
	}
}

func TestClose_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"close", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio close:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestClose_AfterCloseSayFails_Exit1(t *testing.T) {
	// Full close → reject-future-writes flow. After a successful close,
	// a `rufio say` against the same ch-id must surface as NoSuchChannel
	// (D16.6): closed channels are gone for write purposes regardless of
	// the caller's former membership.
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	closeRes := testutil.RunCLI(t, []string{
		"close", chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if closeRes.Code != 0 {
		t.Fatalf("close failed: exit=%d stderr=%q", closeRes.Code, closeRes.Stderr)
	}

	sayRes := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=after close",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if sayRes.Code != 1 {
		t.Fatalf("say after close: exit=%d stderr=%q", sayRes.Code, sayRes.Stderr)
	}
	if !strings.Contains(sayRes.Stderr, "no such channel") {
		t.Errorf("stderr=%q (expected no such channel for closed)", sayRes.Stderr)
	}
}
