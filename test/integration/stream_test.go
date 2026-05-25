package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestStream_EmitsOutboxEvent spawns `rufio stream` and verifies that
// writing to live/outbox/<agent>/ produces a JSONL line on stdout.
//
// Uses pollSubprocessStdout + lineCapture from listen_test.go (same
// package).
func TestStream_EmitsOutboxEvent(t *testing.T) {
	root := initProject(t)

	// Pre-create the dir so the watcher latches on (avoid the
	// dir-create-then-file-create race; that path is exercised by the
	// stream package's unit tests).
	outboxDir := filepath.Join(root, "live", "outbox", "agent-a")
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		t.Fatal(err)
	}

	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(binPath, "stream")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &lineCapture{w: &stderrBuf}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(os.Kill)
			_, _ = cmd.Process.Wait()
		}
		if stderrBuf.Len() > 0 {
			t.Logf("daemon stderr:\n%s", stderrBuf.String())
		}
	}()

	// Retry-write pattern: after watcher-registration window, rewrite
	// the file every 500ms so fsnotify gets multiple shots.
	outboxFile := filepath.Join(outboxDir, "1-x.gdl")
	contents := "@thought|id:1-x|author:agent-a|type:hypothesis|subject:x:1|content:c|scope:fleet|ts:2026-05-12T12:00:00Z|ttl:0\n"
	stopWrite := make(chan struct{})
	go func() {
		time.Sleep(1 * time.Second)
		for {
			_ = os.WriteFile(outboxFile, []byte(contents), 0o644)
			select {
			case <-stopWrite:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()

	matched := pollSubprocessStdout(t, cmd, stdout, 10*time.Second, func(line string) bool {
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			return false
		}
		return got["_type"] == "thought" && got["subject"] == "x:1"
	})
	close(stopWrite)
	if !matched {
		t.Errorf("stream did not emit outbox thought within 10s")
	}
}

func TestStream_InvalidType_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"stream", "--types=bogus"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --types") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestStream_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{"stream"}, root, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}
