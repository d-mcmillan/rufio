package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestListen_CatchUp_EmitsAutoPromoteEvent pins v1.0.3's auto-promote
// stream-event contract end-to-end. A v1.0.3 @auto-promote record
// landed in live/promoted/ MUST appear on `rufio listen --catch-up`
// as a fully-enriched JSON event with the locked payload schema.
func TestListen_CatchUp_EmitsAutoPromoteEvent(t *testing.T) {
	root := initProject(t)

	// Seed a fully-enriched @auto-promote record (the v1.0.3 schema).
	// Same shape ExecutePromote would write — we seed it directly to
	// keep the test fast (no dev daemon, no confirm-fanout latency).
	promotedDir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(promotedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := "@auto-promote|version:1|thought:1727000000-aaaaaa|" +
		"observation:1727000001-bbbbbb|origin:agent-a|subject:customer:5821|" +
		"scope:fleet|confirmers:agent-b,agent-c,agent-d|" +
		"confirm-count:3|refute-count:0|" +
		"confidence:1|by:auto-promote|ts:2026-05-21T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(promotedDir, "1727000000-aaaaaa.gdl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	bin, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cmd := exec.Command(bin, "listen", "--catch-up")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-z")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	cmd.Stderr = devnull
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	}()

	var captured string
	matched := pollSubprocessStdout(t, cmd, stdout, 5*time.Second, func(line string) bool {
		if !strings.Contains(line, `"_type":"auto-promote"`) {
			return false
		}
		captured = line
		return true
	})
	if !matched {
		t.Fatalf("listen --catch-up did not emit auto-promote event within 5s")
	}

	// Validate the payload shape on the captured line.
	var got map[string]any
	if err := json.Unmarshal([]byte(captured), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nline=%q", err, captured)
	}
	if got["_version"].(float64) != 1 {
		t.Errorf("_version=%v want 1", got["_version"])
	}
	if got["source_thought_id"] != "1727000000-aaaaaa" {
		t.Errorf("source_thought_id=%v", got["source_thought_id"])
	}
	if got["promoted_id"] != "1727000001-bbbbbb" {
		t.Errorf("promoted_id=%v", got["promoted_id"])
	}
	if got["subject"] != "customer:5821" {
		t.Errorf("subject=%v", got["subject"])
	}
	if got["confirm_count"].(float64) != 3 {
		t.Errorf("confirm_count=%v want 3", got["confirm_count"])
	}
	confirmers, _ := got["confirmers"].([]any)
	if len(confirmers) != 3 {
		t.Errorf("confirmers len=%d want 3", len(confirmers))
	}
}

// TestListen_CatchUp_AutoPromote_PrivacyScopeAgent pins privacy floor
// (#147) end-to-end: an agent-scoped auto-promote is hidden from a
// non-author listener and surfaced to the original author.
func TestListen_CatchUp_AutoPromote_PrivacyScopeAgent(t *testing.T) {
	root := initProject(t)
	promotedDir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(promotedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := "@auto-promote|version:1|thought:1727000000-aaaaaa|" +
		"observation:1727000001-bbbbbb|origin:agent-a|subject:customer:5821|" +
		"scope:agent|confirmers:agent-b,agent-c,agent-d|" +
		"confirm-count:3|refute-count:0|" +
		"confidence:1|by:auto-promote|ts:2026-05-21T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(promotedDir, "1727000000-aaaaaa.gdl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	bin, _ := testutil.BuildBinary()

	// Two listeners: the original author (agent-a) must see the
	// scope=agent auto-promote event; a non-author (agent-z) must NOT.
	// `listen --catch-up` is long-running (it engages the live tail
	// after the catch-up flush), so we drive it as a subprocess and
	// poll stdout instead of going through testutil.RunCLI.
	for _, tc := range []struct {
		agent string
		want  bool
	}{
		{"agent-a", true},  // origin — must see
		{"agent-z", false}, // non-author — must not see
	} {
		tc := tc
		t.Run(tc.agent, func(t *testing.T) {
			cmd := exec.Command(bin, "listen", "--catch-up", "--types=auto-promote")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID="+tc.agent)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			devnull, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer devnull.Close()
			cmd.Stderr = devnull
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Signal(syscall.SIGKILL)
					_, _ = cmd.Process.Wait()
				}
			}()
			matched := pollSubprocessStdout(t, cmd, stdout, 3*time.Second, func(line string) bool {
				return strings.Contains(line, `"_type":"auto-promote"`)
			})
			if matched != tc.want {
				t.Errorf("agent=%s saw=%v want=%v", tc.agent, matched, tc.want)
			}
		})
	}
}
