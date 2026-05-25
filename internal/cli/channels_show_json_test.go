// Package cli — RED tests for the L3 minor-cleanup: `rufio channel show
// --json` mixed-type stream split. R26 finding: today the JSON path
// emits a header object (_type:"channel") followed by message records
// (_type:"channel-message") in the same stream, forcing every consumer
// to filter via `jq 'select(._type == "channel-message")'`.
//
// The fix flips the default: `--json` emits ONLY message records
// (consumer-friendly default); `--with-header` opts in to the header
// object as the first JSONL line.
//
// BACKWARD-COMPAT: this changes the default JSONL shape of
// `channel show --json`. Existing consumers parsing the header must add
// `--with-header`. The behaviour is documented loudly in the L3 commit
// message.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// seedShowProject creates a tmp project with a channel + a few messages
// for the show tests. Returns root + channel id.
func seedShowProject(t *testing.T) (root, chID string) {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	// FindProjectRoot requires a rufio.gdl marker at the project root.
	if err := os.WriteFile(filepath.Join(real, "rufio.gdl"), []byte("@config|name:test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	root = real
	chID = "ch-test-aaaaaa"

	// Seed an active channel meta.gdl with alice as opener, bob as target.
	meta := channels.BuildMetaRecord(chID, "alice", "bob", "topic-line", "intent-line", "2026-05-12T12:00:00Z")
	if err := channels.WriteMeta(root, chID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Seed three messages.
	for _, m := range []struct {
		id, ts, by, content string
	}{
		{"m1", "2026-05-12T12:01:00Z", "alice", "first"},
		{"m2", "2026-05-12T12:02:00Z", "bob", "second"},
		{"m3", "2026-05-12T12:03:00Z", "alice", "third"},
	} {
		rec := channels.BuildSayRecord(m.id, chID, m.by, m.content, m.ts)
		if err := channels.WriteMessage(root, chID, m.id, rec); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
	}
	t.Setenv("RUFIO_AGENT_ID", "alice")
	return root, chID
}

// TestChannelShow_JSON_DefaultsToMessagesOnly — L3 RED. By default,
// `--json` emits only channel-message records. The first line MUST be a
// message; no "_type":"channel" header line is present.
func TestChannelShow_JSON_DefaultsToMessagesOnly(t *testing.T) {
	root, chID := seedShowProject(t)

	out := captureStdout(t, func() {
		opts := output.RenderOpts{JSON: true}
		// withHeader=false (the new default — header is opt-in).
		if err := runChannelShow(root, chID, "" /*since*/, false /*withHeader*/, opts); err != nil {
			t.Fatalf("runChannelShow: %v", err)
		}
	})

	lines := splitJSONLines(out)
	if len(lines) == 0 {
		t.Fatalf("expected at least one JSON line, got %q", out)
	}
	// No line may carry the channel header _type.
	if strings.Contains(out, `"_type":"channel"`) {
		t.Errorf("default --json must NOT include the channel header (_type:\"channel\"); got:\n%s", out)
	}
	// First line must be a channel-message.
	if !strings.Contains(lines[0], `"_type":"channel-message"`) {
		t.Errorf("first JSON line must be a channel-message; got:\n%s", lines[0])
	}
	// All 3 messages must appear.
	for _, msgID := range []string{`"id":"m1"`, `"id":"m2"`, `"id":"m3"`} {
		if !strings.Contains(out, msgID) {
			t.Errorf("expected %s in output, got:\n%s", msgID, out)
		}
	}
}

// TestChannelShow_JSON_WithHeaderFlag_IncludesChannelObject — L3 RED.
// With --with-header, the first JSONL line MUST be the channel header
// object (_type:"channel"); subsequent lines are messages. This is the
// legacy behaviour, preserved as opt-in for callers that want the
// metadata inline.
func TestChannelShow_JSON_WithHeaderFlag_IncludesChannelObject(t *testing.T) {
	root, chID := seedShowProject(t)

	out := captureStdout(t, func() {
		opts := output.RenderOpts{JSON: true}
		// withHeader=true — opts into legacy header-first shape.
		if err := runChannelShow(root, chID, "" /*since*/, true /*withHeader*/, opts); err != nil {
			t.Fatalf("runChannelShow: %v", err)
		}
	})

	lines := splitJSONLines(out)
	if len(lines) == 0 {
		t.Fatalf("expected at least one JSON line, got %q", out)
	}
	// First line MUST be the channel header.
	if !strings.Contains(lines[0], `"_type":"channel"`) {
		t.Errorf("--with-header: first JSON line must be the channel header (_type:\"channel\"); got first line:\n%s\nfull:\n%s",
			lines[0], out)
	}
	// Messages must still follow.
	if !strings.Contains(out, `"_type":"channel-message"`) {
		t.Errorf("--with-header must still emit channel-message lines after the header; got:\n%s", out)
	}
}

// TestChannelShowCmd_FlagWithHeaderExists — `rufio channel show` MUST
// register a --with-header flag (cobra surface regression guard).
func TestChannelShowCmd_FlagWithHeaderExists(t *testing.T) {
	cmd := NewChannelCmd()
	showCmd, _, err := cmd.Find([]string{"show"})
	if err != nil {
		t.Fatalf("find show: %v", err)
	}
	f := showCmd.Flags().Lookup("with-header")
	if f == nil {
		t.Fatal("rufio channel show is missing --with-header flag (L3)")
	}
}

// splitJSONLines splits the captured stdout into non-empty JSONL lines.
func splitJSONLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, l)
	}
	return lines
}
