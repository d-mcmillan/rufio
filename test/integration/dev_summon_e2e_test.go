package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestDev_SummonRoute_LiveSession spawns the real `rufio dev` daemon, has
// agent-a issue a real `rufio summon` targeting agent-b, polls for the
// daemon to route the summon to agent-b's inbox, and finally has agent-b
// accept via the real `rufio accept` CLI. Catches regressions in the
// RoutingHandler @summon dispatch wiring (PR #15 T7), the accept command
// pending→accepted move + channel-meta write (PR #15 T5), and the full
// end-to-end summon → route → accept flow.
func TestDev_SummonRoute_LiveSession(t *testing.T) {
	root := initProject(t)

	// Build the binary once.
	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. --quiet suppresses banner chatter so test output stays clean.
	cmd := exec.Command(binPath, "dev", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-a")
	// os.NewFile(0, os.DevNull) wraps fd 0 (stdin); closes leak into the
	// parent test-process fd table → spurious EBADF on subsequent
	// TempDir RemoveAll cleanups. Use os.Open(os.DevNull).
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	defer devnull.Close()
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	if err := cmd.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_, _ = cmd.Process.Wait()
	}()

	// Give the daemon time to register fsnotify watchers on
	// live/summons/pending/. The race we're guarding against: did fsnotify
	// register the watch before we issue the first `rufio summon`?
	time.Sleep(500 * time.Millisecond)

	// Issue a real summon from agent-a. Capture the summon-id via
	// glob-diff against live/summons/pending/ before/after — mirrors the
	// autopromote-test's outbox glob-diff pattern and avoids parsing
	// stdout (which is presentational and could change shape).
	pendingPattern := filepath.Join(root, "live", "summons", "pending", "*.gdl")
	before, _ := filepath.Glob(pendingPattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
		"--intent=churn discussion",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("summon: exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	after, _ := filepath.Glob(pendingPattern)
	var fresh []string
	for _, p := range after {
		if !beforeSet[p] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("summon did not produce exactly one new pending file: got %d (%v)", len(fresh), fresh)
	}
	summonID := strings.TrimSuffix(filepath.Base(fresh[0]), ".gdl")

	// Poll agent-b's inbox for the routed summon. 5s is the canonical
	// budget for daemon e2e tests — the only latency between the summon
	// landing in live/summons/pending/ and the routed copy appearing in
	// live/inbox/agent-b/ is the fsnotify debounce + one Render/Write
	// round-trip.
	inboxPath := filepath.Join(root, "live", "inbox", "agent-b", summonID+".gdl")
	deadline := time.Now().Add(5 * time.Second)
	var routedOK bool
	for time.Now().Before(deadline) {
		if bs, err := os.ReadFile(inboxPath); err == nil {
			s := string(bs)
			if strings.Contains(s, "@summon|") &&
				strings.Contains(s, "@route|to:agent-b|from:agent-a") {
				routedOK = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !routedOK {
		// Surface enough state to debug a flake from CI logs alone.
		if bs, err := os.ReadFile(inboxPath); err == nil {
			t.Errorf("inbox file present but malformed: %s = %q", inboxPath, bs)
		} else {
			t.Errorf("inbox file missing at %s: %v", inboxPath, err)
		}
		if entries, err := os.ReadDir(filepath.Join(root, "live", "inbox", "agent-b")); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/inbox/agent-b/ contents: %v", names)
		} else {
			t.Errorf("live/inbox/agent-b/ unreadable: %v", err)
		}
		if entries, err := os.ReadDir(filepath.Join(root, "live", "summons", "pending")); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/summons/pending/ contents: %v", names)
		}
		t.FailNow()
	}

	// Have agent-b accept the summon via the real CLI. Accept is
	// synchronous — by the time RunCLI returns, the pending file is
	// gone, the accepted file is written, and the channel meta is on
	// disk. No polling required.
	ares := testutil.RunCLI(t, []string{"accept", summonID}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if ares.Code != 0 {
		t.Fatalf("accept: exit=%d stderr=%q stdout=%q", ares.Code, ares.Stderr, ares.Stdout)
	}

	// 1. Pending file is gone.
	pendingPath := filepath.Join(root, "live", "summons", "pending", summonID+".gdl")
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("pending file still exists after accept: %s err=%v", pendingPath, err)
		if entries, derr := os.ReadDir(filepath.Join(root, "live", "summons", "pending")); derr == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/summons/pending/ contents: %v", names)
		}
	}

	// 2. Accepted file present with both @summon and @accept records.
	acceptedPath := filepath.Join(root, "live", "summons", "accepted", summonID+".gdl")
	acceptedBytes, err := os.ReadFile(acceptedPath)
	if err != nil {
		t.Fatalf("accepted file missing: %s err=%v", acceptedPath, err)
	}
	// The @accept record carries the summon-id between the header and
	// by:/channel: keys, so we assert on the surrounding tokens rather
	// than a single contiguous substring.
	for _, want := range []string{
		"@summon|",
		"@accept|",
		"by:agent-b",
		"channel:ch-",
	} {
		if !strings.Contains(string(acceptedBytes), want) {
			t.Errorf("accepted file missing %q.\n%s", want, acceptedBytes)
		}
	}

	// 3. Exactly one channel meta exists with the expected fields. We
	// glob rather than parse the accept stdout for channel-id because
	// the on-disk meta is the source of truth — if the file is there
	// the channel was minted.
	chMatches, err := filepath.Glob(filepath.Join(root, "live", "channels", "active", "ch-*", "meta.gdl"))
	if err != nil {
		t.Fatalf("glob channels: %v", err)
	}
	if len(chMatches) != 1 {
		t.Errorf("expected exactly 1 channel meta.gdl, got %d (%v)", len(chMatches), chMatches)
		if entries, derr := os.ReadDir(filepath.Join(root, "live", "channels", "active")); derr == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/channels/active/ contents: %v", names)
		}
		t.FailNow()
	}
	metaBytes, err := os.ReadFile(chMatches[0])
	if err != nil {
		t.Fatalf("read meta.gdl: %v", err)
	}
	// Note: GDL render escapes colons in values, so the on-disk form
	// is `topic:customer\:5821`, not `topic:customer:5821`.
	for _, want := range []string{
		"@channel|",
		"opener:agent-a",
		"target:agent-b",
		`topic:customer\:5821`,
	} {
		if !strings.Contains(string(metaBytes), want) {
			t.Errorf("meta.gdl missing %q.\n%s", want, metaBytes)
		}
	}
}
