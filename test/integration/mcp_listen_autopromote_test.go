package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestMCP_Listen_SurfacesAutoPromoteEvents pins the v1.0.3 symmetry
// contract: the MCP `listen` tool MUST surface auto-promote events
// from live/promoted/ — same as `rufio listen --catch-up`. The MCP
// interactive validation gate on PR #188 caught the divergence:
// MCP listen walked only live/inbox/<agent>/ while CLI listen had
// just been extended to walk live/promoted/ for the new event type.
//
// Structural fix: both surfaces now share stream.ListenDirs(agent)
// — this test pins the contract so the next dir addition can't
// regress one transport without the other.
func TestMCP_Listen_SurfacesAutoPromoteEvents(t *testing.T) {
	root := initProject(t)

	// Seed a v1.0.3-shaped @auto-promote record directly. Same shape
	// ExecutePromote writes; bypassing the dev daemon keeps the test
	// fast and deterministic.
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

	// Drive the MCP listen tool with no filters.
	c := startMCP(t, root, "agent-z")
	result := c.callTool(t, "listen", map[string]any{})

	evs, _ := result["events"].([]any)
	var got map[string]any
	for _, e := range evs {
		m, _ := e.(map[string]any)
		if m["_type"] == "auto-promote" {
			got = m
			break
		}
	}
	if got == nil {
		t.Fatalf("MCP listen did not surface the auto-promote event; got %d events: %v", len(evs), evs)
	}
	// Sanity-check the enriched payload — same lock as the CLI side.
	if got["_version"].(float64) != 1 {
		t.Errorf("_version=%v want 1", got["_version"])
	}
	if got["source_thought_id"] != "1727000000-aaaaaa" {
		t.Errorf("source_thought_id=%v", got["source_thought_id"])
	}
	if got["promoted_id"] != "1727000001-bbbbbb" {
		t.Errorf("promoted_id=%v", got["promoted_id"])
	}
	if got["confirm_count"].(float64) != 3 {
		t.Errorf("confirm_count=%v want 3", got["confirm_count"])
	}
	confirmers, _ := got["confirmers"].([]any)
	if len(confirmers) != 3 {
		t.Errorf("confirmers len=%d want 3", len(confirmers))
	}
}

// TestMCP_Listen_EquivalentToCLI_ForAutoPromote pins byte-structural
// equivalence between the MCP listen tool and `rufio listen --catch-up`
// for the auto-promote event class. Drift between the two transports
// silently splits the symmetry contract; this assertion is the gate.
//
// The CLI emits Event + sideband {"_type":"cursor",...} lines on the
// same stdout; we filter the cursor sideband out before comparing.
func TestMCP_Listen_EquivalentToCLI_ForAutoPromote(t *testing.T) {
	root := initProject(t)
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

	// CLI side: spawn `rufio listen --catch-up --types=auto-promote`,
	// drain stdout, kill once the auto-promote line lands.
	bin, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cmd := exec.Command(bin, "listen", "--catch-up", "--types=auto-promote")
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

	var cliLine string
	matched := pollSubprocessStdout(t, cmd, stdout, 5*time.Second, func(line string) bool {
		if strings.Contains(line, `"_type":"auto-promote"`) {
			cliLine = line
			return true
		}
		return false
	})
	if !matched {
		t.Fatalf("CLI listen did not emit auto-promote event within 5s")
	}
	var cli map[string]any
	if err := json.Unmarshal([]byte(cliLine), &cli); err != nil {
		t.Fatalf("CLI line invalid JSON: %v", err)
	}

	// MCP side: drive the tool and find the auto-promote event.
	c := startMCP(t, root, "agent-z")
	result := c.callTool(t, "listen", map[string]any{"types": "auto-promote"})
	evs, _ := result["events"].([]any)
	var mcp map[string]any
	for _, e := range evs {
		m, _ := e.(map[string]any)
		if m["_type"] == "auto-promote" {
			mcp = m
			break
		}
	}
	if mcp == nil {
		t.Fatalf("MCP listen did not surface auto-promote event; events=%v", evs)
	}

	// Path differs by absolute prefix only — both report
	// "live/promoted/<id>.gdl". Confidence is float on both sides.
	// The structural keyset + values must match.
	if !reflect.DeepEqual(cli, mcp) {
		t.Fatalf("MCP listen auto-promote event != CLI listen auto-promote event:\n MCP: %#v\n CLI: %#v", mcp, cli)
	}
}
