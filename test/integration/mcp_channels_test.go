package integration_test

import (
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// The channel lifecycle is stateful: summon → accept (opens channel) →
// say/leave/close. Each test drives the SAME sequence through the CLI
// (cliRoot) and the MCP server (mcpRoot), then asserts structuredContent
// == CLI --json (modulo volatile + per-root id keys) and the written
// record(s) are byte-identical under normaliseAll (summon/channel/message
// ids necessarily differ per root; their FORMAT must not).

func envA() map[string]string { return map[string]string{"RUFIO_AGENT_ID": "agent-a"} }
func envB() map[string]string { return map[string]string{"RUFIO_AGENT_ID": "agent-b"} }

// cliSummon opens a summon agent-a→agent-b via the CLI and returns its id.
func cliSummon(t *testing.T, root string) string {
	t.Helper()
	r := testutil.RunCLI(t, []string{
		"summon", "agent-b", "--topic", "incident:42",
		"--intent", "pair on the outage", "--json",
	}, root, envA())
	if r.Code != 0 {
		t.Fatalf("CLI summon exit=%d stderr=%q", r.Code, r.Stderr)
	}
	return idFromGlobPath(globOne(t, root, "live/summons/pending/*.gdl"))
}

func TestMCP_Summon_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	r := testutil.RunCLI(t, []string{
		"summon", "agent-b", "--topic", "incident:42",
		"--intent", "pair on the outage", "--json",
	}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI summon exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "summon", map[string]any{
		"to":     "agent-b",
		"topic":  "incident:42",
		"intent": "pair on the outage",
	})
	// `id` is per-root-volatile (dropped by dropVolatile already).
	assertStructuredFidelity(t, mcpStructured, r.Stdout)

	cliFile := globOne(t, cliRoot, "live/summons/pending/*.gdl")
	mcpFile := globOne(t, mcpRoot, "live/summons/pending/*.gdl")
	if normaliseAll(readSingleFromAbs(t, cliFile)) != normaliseAll(readSingleFromAbs(t, mcpFile)) {
		t.Fatalf("summon record not byte-identical:\n cli=%q\n mcp=%q",
			readSingleFromAbs(t, cliFile), readSingleFromAbs(t, mcpFile))
	}
}

func TestMCP_Accept_FidelityVsCLI(t *testing.T) {
	// accept is multi-record: it writes the channel meta AND moves the
	// summon to accepted/ (with an appended @accept). Assert BOTH.
	cliRoot := initProject(t)
	cliSID := cliSummon(t, cliRoot)
	r := testutil.RunCLI(t, []string{"accept", cliSID, "--json"}, cliRoot, envB())
	if r.Code != 0 {
		t.Fatalf("CLI accept exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpSID := cliSummon(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-b")
	mcpStructured := c.callTool(t, "accept", map[string]any{"summon_id": mcpSID})
	// summon-id + channel are per-root-volatile.
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "summon-id", "channel")

	// (a) channel meta file.
	cliMeta := globOne(t, cliRoot, "live/channels/active/*/meta.gdl")
	mcpMeta := globOne(t, mcpRoot, "live/channels/active/*/meta.gdl")
	if normaliseAll(readSingleFromAbs(t, cliMeta)) != normaliseAll(readSingleFromAbs(t, mcpMeta)) {
		t.Fatalf("channel meta not byte-identical:\n cli=%q\n mcp=%q",
			readSingleFromAbs(t, cliMeta), readSingleFromAbs(t, mcpMeta))
	}
	// (b) the accepted summon file (summon record + appended @accept).
	cliAcc := readAllLinesAbs(t, globOne(t, cliRoot, "live/summons/accepted/*.gdl"))
	mcpAcc := readAllLinesAbs(t, globOne(t, mcpRoot, "live/summons/accepted/*.gdl"))
	if len(cliAcc) != len(mcpAcc) {
		t.Fatalf("accepted summon line count differs: cli=%d mcp=%d", len(cliAcc), len(mcpAcc))
	}
	for i := range cliAcc {
		if normaliseAll(cliAcc[i]) != normaliseAll(mcpAcc[i]) {
			t.Fatalf("accepted summon record[%d] not byte-identical:\n cli=%q\n mcp=%q",
				i, cliAcc[i], mcpAcc[i])
		}
	}
}

func TestMCP_Decline_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliSID := cliSummon(t, cliRoot)
	r := testutil.RunCLI(t, []string{
		"decline", cliSID, "--reason", "out of bandwidth", "--json",
	}, cliRoot, envB())
	if r.Code != 0 {
		t.Fatalf("CLI decline exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpSID := cliSummon(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-b")
	mcpStructured := c.callTool(t, "decline", map[string]any{
		"summon_id": mcpSID,
		"reason":    "out of bandwidth",
	})
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "summon-id")

	cliDec := readAllLinesAbs(t, globOne(t, cliRoot, "live/summons/declined/*.gdl"))
	mcpDec := readAllLinesAbs(t, globOne(t, mcpRoot, "live/summons/declined/*.gdl"))
	if len(cliDec) != len(mcpDec) {
		t.Fatalf("declined summon line count differs: cli=%d mcp=%d", len(cliDec), len(mcpDec))
	}
	for i := range cliDec {
		if normaliseAll(cliDec[i]) != normaliseAll(mcpDec[i]) {
			t.Fatalf("declined summon record[%d] not byte-identical:\n cli=%q\n mcp=%q",
				i, cliDec[i], mcpDec[i])
		}
	}
}

// openChannel runs summon+accept via the CLI and returns the channel id.
func openChannel(t *testing.T, root string) string {
	t.Helper()
	sid := cliSummon(t, root)
	if rr := testutil.RunCLI(t, []string{"accept", sid}, root, envB()); rr.Code != 0 {
		t.Fatalf("accept exit=%d stderr=%q", rr.Code, rr.Stderr)
	}
	meta := globOne(t, root, "live/channels/active/*/meta.gdl")
	// .../active/<ch-id>/meta.gdl → <ch-id>
	parts := strings.Split(meta, "/")
	return parts[len(parts)-2]
}

func TestMCP_Say_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliCh := openChannel(t, cliRoot)
	r := testutil.RunCLI(t, []string{
		"say", "--channel", cliCh, "--content", "ack, looking now", "--json",
	}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI say exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpCh := openChannel(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "say", map[string]any{
		"channel": mcpCh,
		"content": "ack, looking now",
	})
	// id (message id) + channel are per-root-volatile.
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "channel")

	cliMsg := globOne(t, cliRoot, "live/channels/active/*/messages/*.gdl")
	mcpMsg := globOne(t, mcpRoot, "live/channels/active/*/messages/*.gdl")
	if normaliseAll(readSingleFromAbs(t, cliMsg)) != normaliseAll(readSingleFromAbs(t, mcpMsg)) {
		t.Fatalf("say record not byte-identical:\n cli=%q\n mcp=%q",
			readSingleFromAbs(t, cliMsg), readSingleFromAbs(t, mcpMsg))
	}
}

func TestMCP_Leave_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliCh := openChannel(t, cliRoot)
	r := testutil.RunCLI(t, []string{"leave", cliCh, "--json"}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI leave exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpCh := openChannel(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "leave", map[string]any{"channel": mcpCh})
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "channel")

	// leave appends a @channel-leave to the channel meta file.
	cliMeta := readAllLinesAbs(t, globOne(t, cliRoot, "live/channels/active/*/meta.gdl"))
	mcpMeta := readAllLinesAbs(t, globOne(t, mcpRoot, "live/channels/active/*/meta.gdl"))
	if len(cliMeta) != len(mcpMeta) {
		t.Fatalf("meta line count differs after leave: cli=%d mcp=%d", len(cliMeta), len(mcpMeta))
	}
	for i := range cliMeta {
		if normaliseAll(cliMeta[i]) != normaliseAll(mcpMeta[i]) {
			t.Fatalf("meta record[%d] not byte-identical after leave:\n cli=%q\n mcp=%q",
				i, cliMeta[i], mcpMeta[i])
		}
	}
}

func TestMCP_Close_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliCh := openChannel(t, cliRoot)
	// agent-a is the opener (summon's `from`); only the opener may close.
	r := testutil.RunCLI(t, []string{"close", cliCh, "--json"}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI close exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpCh := openChannel(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "close", map[string]any{"channel": mcpCh})
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "channel")

	// close appends @channel-close to meta AND archives active/→closed/.
	cliMeta := readAllLinesAbs(t, globOne(t, cliRoot, "live/channels/closed/*/meta.gdl"))
	mcpMeta := readAllLinesAbs(t, globOne(t, mcpRoot, "live/channels/closed/*/meta.gdl"))
	if len(cliMeta) != len(mcpMeta) {
		t.Fatalf("closed meta line count differs: cli=%d mcp=%d", len(cliMeta), len(mcpMeta))
	}
	for i := range cliMeta {
		if normaliseAll(cliMeta[i]) != normaliseAll(mcpMeta[i]) {
			t.Fatalf("closed meta record[%d] not byte-identical:\n cli=%q\n mcp=%q",
				i, cliMeta[i], mcpMeta[i])
		}
	}
}
