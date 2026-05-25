// Integration tests for `rufio channels list` and `rufio channel show`,
// the cold-late-joiner read API added in #142. Tests exercise the real
// CLI through testutil.RunCLI so the cobra wiring + auth + visibility
// logic is covered end-to-end.
package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestChannelsList_ShowsActiveChannels: default behaviour — `rufio
// channels list` (no flags) enumerates active channels the current
// agent is a member of, columnar by default.
func TestChannelsList_ShowsActiveChannels(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "churn-strategy", "chat")

	res := testutil.RunCLI(t, []string{"channels", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, chID) {
		t.Errorf("stdout missing chID=%s\n%s", chID, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "active") {
		t.Errorf("stdout missing state column 'active'\n%s", res.Stdout)
	}
}

// TestChannelsList_FilterClosed: --closed restricts to closed channels;
// active channels MUST NOT appear.
func TestChannelsList_FilterClosed(t *testing.T) {
	root := initProject(t)
	activeID := mustOpenChannel(t, root, "agent-a", "agent-b", "topic-active", "i")
	closedID := mustOpenChannel(t, root, "agent-a", "agent-b", "topic-closed", "i")
	cl := testutil.RunCLI(t, []string{"close", closedID}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if cl.Code != 0 {
		t.Fatalf("close failed: exit=%d stderr=%q", cl.Code, cl.Stderr)
	}

	res := testutil.RunCLI(t, []string{"channels", "list", "--closed"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, closedID) {
		t.Errorf("--closed output missing closedID=%s\n%s", closedID, res.Stdout)
	}
	if strings.Contains(res.Stdout, activeID) {
		t.Errorf("--closed output includes activeID=%s (must be filtered out)\n%s", activeID, res.Stdout)
	}
}

// TestChannelsList_MemberOfFilter: --member-of=<agent> lists channels
// where the named agent is opener or target (subject to caller's own
// privacy floor — covered separately).
func TestChannelsList_MemberOfFilter(t *testing.T) {
	root := initProject(t)
	abID := mustOpenChannel(t, root, "agent-a", "agent-b", "topic-ab", "i")
	acID := mustOpenChannel(t, root, "agent-a", "agent-c", "topic-ac", "i")

	// agent-a sees both (a is a member of both).
	res := testutil.RunCLI(t, []string{"channels", "list", "--member-of=agent-b"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, abID) {
		t.Errorf("--member-of=agent-b missing abID=%s\n%s", abID, res.Stdout)
	}
	if strings.Contains(res.Stdout, acID) {
		t.Errorf("--member-of=agent-b includes acID=%s (b is not a member)\n%s", acID, res.Stdout)
	}
}

// TestChannelsList_PrivacyHidesNonMemberChannels: privacy floor — a
// third party (agent-d) asking `--member-of=agent-a` MUST NOT see
// channels they themselves are not a member of. The privacy model is
// "members see the channel"; a non-member querying for someone else's
// channels does not get a backdoor enumeration.
func TestChannelsList_PrivacyHidesNonMemberChannels(t *testing.T) {
	root := initProject(t)
	abID := mustOpenChannel(t, root, "agent-a", "agent-b", "topic-ab", "i")

	res := testutil.RunCLI(t, []string{"channels", "list", "--member-of=agent-a"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-d"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, abID) {
		t.Errorf("agent-d should not see channel %s (not a member): \n%s", abID, res.Stdout)
	}
}

// TestChannelShow_RendersMessagesChronologically: `rufio channel show
// <ch-id>` prints header then messages in time-ascending order.
func TestChannelShow_RendersMessagesChronologically(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	// Three messages in order.
	for i, content := range []string{"first", "second", "third"} {
		// Space the writes so message ids (unix-millis prefix) sort
		// deterministically.
		if i > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		who := "agent-a"
		if i%2 == 1 {
			who = "agent-b"
		}
		res := testutil.RunCLI(t, []string{
			"say", "--channel=" + chID, "--content=" + content,
		}, root, map[string]string{"RUFIO_AGENT_ID": who})
		if res.Code != 0 {
			t.Fatalf("say %q failed: exit=%d stderr=%q", content, res.Code, res.Stderr)
		}
	}

	res := testutil.RunCLI(t, []string{"channel", "show", chID}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	// Header line must mention the channel-id and opener.
	if !strings.Contains(res.Stdout, chID) {
		t.Errorf("stdout missing chID header\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "opener:agent-a") {
		t.Errorf("stdout missing opener field\n%s", res.Stdout)
	}
	// Message ordering: indexOf("first") < indexOf("second") < indexOf("third").
	iFirst := strings.Index(res.Stdout, "first")
	iSecond := strings.Index(res.Stdout, "second")
	iThird := strings.Index(res.Stdout, "third")
	if iFirst < 0 || iSecond < 0 || iThird < 0 {
		t.Fatalf("missing one of the three contents:\n%s", res.Stdout)
	}
	if !(iFirst < iSecond && iSecond < iThird) {
		t.Errorf("messages not in chronological order: %d %d %d\n%s",
			iFirst, iSecond, iThird, res.Stdout)
	}
}

// TestChannelShow_NonMember_AuthzError: ONLY current/past members may
// `channel show`. A third party gets a clear "not authorized" error.
func TestChannelShow_NonMember_AuthzError(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{"channel", "show", chID}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-c"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stderr, "not authorized") {
		t.Errorf("stderr=%q (expected 'not authorized' phrasing)", res.Stderr)
	}
	if !strings.Contains(res.Stderr, chID) {
		t.Errorf("stderr=%q (expected ch-id in error)", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio channel:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestChannelShow_SinceFilter: --since=<duration> trims to recent
// messages. Older messages must be elided; newer ones must remain.
func TestChannelShow_SinceFilter(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	// One "old" message; sleep so the second message is unambiguously
	// newer in wall-clock terms.
	res1 := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=old-message",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res1.Code != 0 {
		t.Fatalf("say old failed: exit=%d stderr=%q", res1.Code, res1.Stderr)
	}
	time.Sleep(1100 * time.Millisecond)
	res2 := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=new-message",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res2.Code != 0 {
		t.Fatalf("say new failed: exit=%d stderr=%q", res2.Code, res2.Stderr)
	}

	// --since=1s should exclude the old message (written >1s ago).
	res := testutil.RunCLI(t, []string{
		"channel", "show", chID, "--since=1s",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if strings.Contains(res.Stdout, "old-message") {
		t.Errorf("--since=1s leaked old-message:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "new-message") {
		t.Errorf("--since=1s missing new-message:\n%s", res.Stdout)
	}
}

// TestChannelShow_JSON: L3 default — --json emits messages-only (no
// header). --with-header opts into the legacy header-first shape.
func TestChannelShow_JSON(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")
	r := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=hello",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("say failed: exit=%d stderr=%q", r.Code, r.Stderr)
	}

	// L3 default: --json without --with-header → one line, message only.
	res := testutil.RunCLI(t, []string{"channel", "show", chID, "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("L3 default --json: want exactly 1 JSONL line (message-only), got %d:\n%s",
			len(lines), res.Stdout)
	}
	if strings.Contains(res.Stdout, `"_type":"channel"`) && !strings.Contains(res.Stdout, `"_type":"channel-message"`) {
		t.Errorf("default --json must NOT include header line, got:\n%s", res.Stdout)
	}
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &msg); err != nil {
		t.Fatalf("message JSON: %v\n%q", err, lines[0])
	}
	if msg["_type"] != "channel-message" {
		t.Errorf("message _type=%v, want channel-message", msg["_type"])
	}
	if msg["content"] != "hello" {
		t.Errorf("message content=%v want hello", msg["content"])
	}

	// --with-header opt-in: header line 1 + message line 2.
	resH := testutil.RunCLI(t, []string{"channel", "show", chID, "--json", "--with-header"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if resH.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", resH.Code, resH.Stderr)
	}
	linesH := strings.Split(strings.TrimRight(resH.Stdout, "\n"), "\n")
	if len(linesH) < 2 {
		t.Fatalf("--with-header: want at least 2 JSONL lines (header + message), got %d:\n%s",
			len(linesH), resH.Stdout)
	}
	var header map[string]interface{}
	if err := json.Unmarshal([]byte(linesH[0]), &header); err != nil {
		t.Fatalf("header JSON: %v\n%q", err, linesH[0])
	}
	if header["_type"] != "channel" {
		t.Errorf("header _type=%v, want channel", header["_type"])
	}
	if header["id"] != chID {
		t.Errorf("header id=%v want %s", header["id"], chID)
	}
	var msgH map[string]interface{}
	if err := json.Unmarshal([]byte(linesH[1]), &msgH); err != nil {
		t.Fatalf("message JSON: %v\n%q", err, linesH[1])
	}
	if msgH["_type"] != "channel-message" {
		t.Errorf("--with-header message _type=%v, want channel-message", msgH["_type"])
	}
}

// TestChannelsList_JSON: --json emits one channel per line with the
// promised fields.
func TestChannelsList_JSON(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "topic-json", "intent-json")

	res := testutil.RunCLI(t, []string{"channels", "list", "--json"}, root,
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
		"_type":    "channel",
		"_version": "1",
		"id":       chID,
		"opener":   "agent-a",
		"target":   "agent-b",
		"topic":    "topic-json",
		"state":    "active",
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s=%v want %v", k, got[k], want)
		}
	}
	members, ok := got["members"].([]interface{})
	if !ok || len(members) != 2 {
		t.Errorf("members=%v (want 2-element slice)", got["members"])
	}
}
