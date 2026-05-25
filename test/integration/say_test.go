package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
	"github.com/d-mcmillan/rufio/internal/testutil"
)

// msgIDPattern matches a message id minted by channels.GenerateMessageID:
// <unix-millis>-<rand6>. Same shape as a thought id (D16.1) — context
// disambiguates.
var msgIDPattern = regexp.MustCompile(`^\d+-[a-z0-9]{6}$`)

// mustOpenChannel drives the real CLI to open a channel between opener and
// target, returning the resulting ch-id. Steps:
//
//  1. opener: `rufio summon <target> --topic=<topic> --intent=<intent>`
//  2. target: `rufio accept <summon-id> --json` → channel id parsed from
//     the JSON payload.
//
// Local to say_test.go for PR #16 T3; T4 (leave) and T5 (close) tests can
// reuse this directly since they share the same `integration_test` package.
func mustOpenChannel(t *testing.T, root, opener, target, topic, intent string) string {
	t.Helper()
	summonID := mustSeedSummon(t, root, opener, target, topic, intent)
	res := testutil.RunCLI(t, []string{
		"accept", summonID, "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": target})
	if res.Code != 0 {
		t.Fatalf("accept failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &payload); err != nil {
		t.Fatalf("accept json parse: %v stdout=%q", err, res.Stdout)
	}
	chID, ok := payload["channel"].(string)
	if !ok || !chIDPattern.MatchString(chID) {
		t.Fatalf("accept payload missing/invalid channel: %v", payload["channel"])
	}
	return chID
}

// findMessageFile returns the single .gdl file under
// live/channels/active/<ch>/messages/. Fails if zero or more than one
// exist. Used by the happy-path test to assert exact-match contents
// without having to parse the say output for the message id.
func findMessageFile(t *testing.T, root, chID string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "channels", "active", chID, "messages", "*.gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob messages: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 message file, got %d (%v)", len(matches), matches)
	}
	return matches[0]
}

func TestSay_HappyPath_WritesMessageFile(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "churn-strategy", "lets chat")

	res := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=hello",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	msgPath := findMessageFile(t, root, chID)
	msgID := strings.TrimSuffix(filepath.Base(msgPath), ".gdl")
	if !msgIDPattern.MatchString(msgID) {
		t.Errorf("message id %q does not match canonical shape", msgID)
	}
	bs, err := os.ReadFile(msgPath)
	if err != nil {
		t.Fatalf("read message file: %v", err)
	}
	// Issue #107: on-disk Type is "channel-message" (CLI verb still `say`).
	for _, want := range []string{
		"@channel-message|",
		"id:" + msgID,
		"channel:" + chID,
		"by:agent-a",
		"content:hello",
	} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("message file missing %q.\n%s", want, bs)
		}
	}
}

func TestSay_AnyCurrentMemberCanSay(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	// Both opener (agent-a) AND target (agent-b) are current members
	// per D16.3 — both must succeed and each must produce its own
	// message file.
	for _, agent := range []string{"agent-a", "agent-b"} {
		agent := agent
		t.Run(agent, func(t *testing.T) {
			before, _ := filepath.Glob(filepath.Join(root, "live", "channels", "active", chID, "messages", "*.gdl"))
			res := testutil.RunCLI(t, []string{
				"say", "--channel=" + chID, "--content=hi from " + agent,
			}, root, map[string]string{"RUFIO_AGENT_ID": agent})
			if res.Code != 0 {
				t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
			}
			after, _ := filepath.Glob(filepath.Join(root, "live", "channels", "active", chID, "messages", "*.gdl"))
			if len(after) != len(before)+1 {
				t.Fatalf("expected one new message file, got before=%d after=%d", len(before), len(after))
			}
			// Find the fresh file and assert its `by:` field.
			beforeSet := make(map[string]bool, len(before))
			for _, p := range before {
				beforeSet[p] = true
			}
			var fresh string
			for _, p := range after {
				if !beforeSet[p] {
					fresh = p
					break
				}
			}
			bs, err := os.ReadFile(fresh)
			if err != nil {
				t.Fatalf("read fresh: %v", err)
			}
			if !strings.Contains(string(bs), "by:"+agent) {
				t.Errorf("fresh message missing by:%s\n%s", agent, bs)
			}
		})
	}
}

func TestSay_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=hello world", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	// Issue #107: _type is "channel-message" (CLI verb still `say`).
	if got["_type"] != "channel-message" {
		t.Errorf("_type=%v, want channel-message", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	idRaw, ok := got["id"].(string)
	if !ok || !msgIDPattern.MatchString(idRaw) {
		t.Errorf("id=%v does not match canonical shape", got["id"])
	}
	if got["channel"] != chID {
		t.Errorf("channel=%v want %s", got["channel"], chID)
	}
	if got["by"] != "agent-a" {
		t.Errorf("by=%v", got["by"])
	}
	if got["content"] != "hello world" {
		t.Errorf("content=%v", got["content"])
	}
	if _, ok := got["ts"].(string); !ok {
		t.Errorf("ts missing or non-string: %v", got["ts"])
	}
}

func TestSay_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=hi",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// H3d (#125): echo prefix normalized "said:" → "say: " and keys
	// reordered to lead with `id=` (matches house style).
	if !strings.Contains(res.Stdout, "say: id=") || !strings.Contains(res.Stdout, "channel="+chID) {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
	// Single-prefix invariant: success path must NOT carry an error prefix.
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio say:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
}

func TestSay_MissingChannel_Exit2(t *testing.T) {
	root := initProject(t)
	// Open a channel just to make sure the project is set up; the say
	// call deliberately omits --channel.
	_ = mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"say", "--content=hello",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--channel must not be empty") {
		t.Errorf("stderr=%q (expected --channel must not be empty)", res.Stderr)
	}
}

func TestSay_MissingContent_Exit2(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--content must not be empty") {
		t.Errorf("stderr=%q (expected --content must not be empty)", res.Stderr)
	}
}

func TestSay_NoSuchChannel_Exit1(t *testing.T) {
	root := initProject(t)
	// Fake but well-formed ch-id — must not collide with anything real.
	res := testutil.RunCLI(t, []string{
		"say", "--channel=ch-1727000000-fake12", "--content=hi",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such channel") {
		t.Errorf("stderr=%q (expected no such channel)", res.Stderr)
	}
}

func TestSay_NonMember_Exit1(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	// agent-c is neither opener nor target — must be rejected before any
	// write happens.
	res := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=intrusion",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-c"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "is not a current member") {
		t.Errorf("stderr=%q (expected NotChannelMemberError)", res.Stderr)
	}
	// No message file should have been written.
	pattern := filepath.Join(root, "live", "channels", "active", chID, "messages", "*.gdl")
	if matches, _ := filepath.Glob(pattern); len(matches) != 0 {
		t.Errorf("unauthorised say produced message files: %v", matches)
	}
}

func TestSay_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	res := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=hi",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio say:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestSay_AfterLeaveCannotSay_Exit1 covers D16.3: a member who has left
// the channel is no longer a current member and must be rejected on
// further say attempts. `rufio leave` lands in PR #16 T4 — until then,
// this test exercises the same audit-trail outcome via the channels
// package directly (importable in the integration_test package).
func TestSay_AfterLeaveCannotSay_Exit1(t *testing.T) {
	root := initProject(t)
	chID := mustOpenChannel(t, root, "agent-a", "agent-b", "t", "i")

	if err := channels.AppendLeave(root, chID, "agent-a", versioning.NowISO()); err != nil {
		t.Fatalf("AppendLeave: %v", err)
	}

	res := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=stragglers",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "is not a current member") {
		t.Errorf("stderr=%q (expected NotChannelMemberError after leave)", res.Stderr)
	}
}
