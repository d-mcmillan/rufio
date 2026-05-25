package integration_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// This file holds the shared MCP integration-test harness, hoisted out of
// mcp_skeleton_test.go / mcp_attend_test.go (PR1 inline copies) so every MCP
// tool test (attend + the PR2 batch) drives the server through ONE client
// implementation. No behaviour change vs. PR1's hardened harness:
//   - rpc() still races a real 10s deadline against the blocking pipe read
//     and Kills the process on timeout (no reliance on Go's global timeout);
//   - normaliseVolatile() is still delimiter-anchored so it neutralises ts/id
//     whether they appear trailing OR mid-record;
//   - the structured-fidelity comparison still deep-equals the tool's
//     structuredContent against the CLI --json modulo the volatile keys.

// mcpConn is a minimal newline-delimited JSON-RPC client over the server's
// stdio. The MCP stdio server is long-lived, so it cannot be driven through
// testutil.RunCLI (which waits for the process to exit). We spawn the binary
// directly with os/exec, pipe stdin/stdout, speak newline-delimited
// JSON-RPC, and Kill() the process in cleanup.
type mcpConn struct {
	cmd *exec.Cmd
	in  *bufio.Writer
	out *bufio.Scanner
	id  int
}

func startMCP(t *testing.T, projectRoot, agent string) *mcpConn {
	t.Helper()
	bin, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("BuildBinary: %v", err)
	}
	cmd := exec.Command(bin, "mcp", "--root", projectRoot, "--agent", agent)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	c := &mcpConn{cmd: cmd, in: bufio.NewWriter(stdin), out: bufio.NewScanner(stdout)}
	c.out.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	c.initialize(t)
	return c
}

func (c *mcpConn) rpc(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	c.id++
	req := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := c.in.Flush(); err != nil {
		t.Fatal(err)
	}

	// bufio.Scanner.Scan() is an unbounded blocking pipe read: a
	// non-responding handler would otherwise hang until Go's global test
	// timeout. Read the response in a goroutine and race it against a real
	// 10s deadline; on timeout Kill the process so t.Cleanup's Wait() does
	// not also block, then fail fast.
	done := make(chan map[string]any, 1)
	go func() {
		for c.out.Scan() {
			var resp map[string]any
			if err := json.Unmarshal(c.out.Bytes(), &resp); err != nil {
				continue
			}
			if _, ok := resp["id"]; ok {
				done <- resp
				return
			}
		}
		close(done) // scanner ended (EOF/error) with no id-bearing response
	}()

	select {
	case resp, ok := <-done:
		if !ok {
			t.Fatalf("no JSON-RPC response for %s (server stdout closed)", method)
		}
		return resp
	case <-time.After(10 * time.Second):
		_ = c.cmd.Process.Kill()
		t.Fatalf("no JSON-RPC response within 10s for %s", method)
		return nil
	}
}

func (c *mcpConn) initialize(t *testing.T) {
	resp := c.rpc(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "rufio-test", "version": "0"},
	})
	if _, bad := resp["error"]; bad {
		t.Fatalf("initialize errored: %v", resp["error"])
	}
	// notifications/initialized (no id, no response expected)
	n, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	_, _ = c.in.Write(append(n, '\n'))
	_ = c.in.Flush()
}

// callTool issues a tools/call for `name` with `args`, fails on any
// protocol-level error or tool isError, and returns the tool's
// structuredContent object. This is the common shape every PR2 fidelity
// test needs.
func (c *mcpConn) callTool(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	resp := c.rpc(t, "tools/call", map[string]any{"name": name, "arguments": args})
	if _, bad := resp["error"]; bad {
		t.Fatalf("%s tool errored at protocol level: %v", name, resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("%s tool returned isError: %v", name, result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("%s result has no structuredContent object: %v", name, result)
	}
	return structured
}

// volatileTS matches the delimited `|ts:<value>` field anywhere in a
// rendered gdl record (NOT $-anchored — only @attention happens to put ts
// last; PR2 record types put it mid-line). gdl escapes colons in the
// RFC3339Nano timestamp (10\:29...) AND escapes the | delimiter inside
// values, so `[^|]*` safely consumes the whole value up to the next real
// field delimiter or end-of-line. The leading `|` is part of the match and
// is restored by the replacement so the record structure is preserved.
var volatileTS = regexp.MustCompile(`\|ts:[^|]*`)

// volatileID normalises a random id stem (`|id:<unix-millis>-<rand6>`) —
// the other inherently-random field. attention records have no id field,
// but PR2 record types (think/observe/...) do, mid-line; the match is
// delimiter-anchored on the leading `|` for the same reason as volatileTS.
var volatileID = regexp.MustCompile(`\|id:[0-9]+-[a-z0-9]{6}`)

// volatileCreatedAt matches the `|created-at:<value>` timestamp field the
// @channel meta record carries (the channel analogue of ts). Same
// delimiter-anchored, escaped-colon-safe reasoning as volatileTS.
var volatileCreatedAt = regexp.MustCompile(`\|created-at:[^|]*`)

// normaliseVolatile rewrites the inherently-random timestamp/id fields
// (ts, id, and the @channel record's created-at timestamp) to fixed
// sentinels so two records written from identical inputs by the CLI and
// the MCP server compare byte-identical. Works whether the volatile field
// is trailing or mid-record.
func normaliseVolatile(rec string) string {
	rec = volatileID.ReplaceAllString(rec, "|id:ID")
	rec = volatileTS.ReplaceAllString(rec, "|ts:TS")
	rec = volatileCreatedAt.ReplaceAllString(rec, "|created-at:TS")
	return rec
}

// dropVolatile removes the inherently-random keys (ts, and id if present)
// from a parsed structured payload so the CLI --json object and the MCP
// structuredContent object can be compared for 1:1 key/value fidelity.
func dropVolatile(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k == "ts" || k == "id" {
			continue
		}
		out[k] = v
	}
	return out
}

// assertStructuredFidelity deep-equals the MCP tool's structuredContent
// against the CLI verb's --json object, after dropping the volatile keys.
// A renamed/missing/extra Out key would otherwise ship green — this is the
// per-tool arbiter the plan mandates.
func assertStructuredFidelity(t *testing.T, mcpStructured map[string]any, cliJSON string) {
	t.Helper()
	var cli map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(cliJSON)), &cli); err != nil {
		t.Fatalf("CLI --json not parseable: %v\nstdout=%q", err, cliJSON)
	}
	if got, want := dropVolatile(mcpStructured), dropVolatile(cli); !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP structuredContent != CLI --json (volatile keys dropped):\n mcp=%#v\n cli=%#v", got, want)
	}
}

// readSingleRecord reads relPath under root and returns its single trimmed
// gdl record line, failing if the file is absent or holds != 1 line. Used
// by single-record fidelity tests (attend/observe/reason/...).
func readSingleRecord(t *testing.T, root, relPath string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one record line in %s, got %d: %q", relPath, len(lines), string(b))
	}
	return lines[0]
}

// readAllLines returns the trimmed non-empty lines of relPath under root
// (multi-record files: think bundles, channel meta with members, …).
func readAllLines(t *testing.T, root, relPath string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// globOne resolves a single file matching pattern under root (relative
// glob), failing unless exactly one match exists. Verbs that name files by
// a random id (think/observe/reason/summon/say/goal) need this to locate
// the written record without knowing the id.
func globOne(t *testing.T, root, pattern string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one file matching %s, got %d: %v", pattern, len(matches), matches)
	}
	return matches[0]
}

// globAll resolves every file matching pattern under root (relative
// glob), failing only on a malformed pattern. May return an empty slice.
func globAll(t *testing.T, root, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	return matches
}

// readSingleFromAbs reads an absolute file path and returns its single
// trimmed record line (companion to readSingleRecord for glob-resolved
// paths).
func readSingleFromAbs(t *testing.T, abs string) string {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one record line in %s, got %d: %q", abs, len(lines), string(b))
	}
	return lines[0]
}

// readAllLinesAbs returns the trimmed non-empty lines of an absolute file
// path (multi-record glob-resolved files: think bundles, …).
func readAllLinesAbs(t *testing.T, abs string) []string {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// idFromGlobPath returns the basename stem of a path (the canonical
// thought/record id — same derivation recall.idFromPath uses).
func idFromGlobPath(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// assertStructuredFidelityIgnoring is assertStructuredFidelity but also
// drops the named keys (in addition to the volatile ts/id) before the
// deep-equal — for verbs whose payload echoes a per-root-random id (e.g.
// retract's `target` is the seeded thought id, which differs per root).
func assertStructuredFidelityIgnoring(t *testing.T, mcpStructured map[string]any, cliJSON string, ignore ...string) {
	t.Helper()
	var cli map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(cliJSON)), &cli); err != nil {
		t.Fatalf("CLI --json not parseable: %v\nstdout=%q", err, cliJSON)
	}
	strip := func(m map[string]any) map[string]any {
		out := dropVolatile(m)
		for _, k := range ignore {
			delete(out, k)
		}
		return out
	}
	if got, want := strip(mcpStructured), strip(cli); !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP structuredContent != CLI --json (volatile+%v dropped):\n mcp=%#v\n cli=%#v", ignore, got, want)
	}
}

// volatileIDRefs normalises any field whose value is an id-shaped
// reference to a (per-root-random) thought/record id:
// `|<key>:<unix-millis>-<rand6>`. These (target/decision/parent) echo a
// generated id that necessarily differs between the independent CLI and
// MCP roots; the id FORMAT is byte-identical, only the random value
// differs, so neutralising it keeps the structural comparison honest. The
// strict `[0-9]+-[a-z0-9]{6}` shape is exactly thought.GenerateID's
// output, so no stable value can collide.
var volatileIDRefs = regexp.MustCompile(`\|(target|decision|parent):[0-9]+-[a-z0-9]{6}`)

func normaliseIDRefs(rec string) string {
	return volatileIDRefs.ReplaceAllString(rec, "|$1:IDREF")
}

// volatileChannelRefs neutralises a `|<key>:ch-<unix-millis>-<rand6>`
// channel-id reference (the `ch-` prefix means it does NOT match the
// id-only volatile regexes). Channels are minted per accept, so the id
// necessarily differs between the CLI and MCP roots while the FORMAT is
// byte-identical.
var volatileChannelRefs = regexp.MustCompile(`\|(id|channel):ch-[0-9]+-[a-z0-9]{6}`)

func normaliseChannelRefs(rec string) string {
	return volatileChannelRefs.ReplaceAllString(rec, "|$1:CHREF")
}

// normaliseAll applies every volatile/id-shaped normalisation. Use it for
// channel-chain records where summon ids, channel ids and message ids all
// appear and all differ per root, but the record STRUCTURE must be
// byte-identical between the CLI and the MCP server.
func normaliseAll(rec string) string {
	return normaliseChannelRefs(normaliseIDRefs(normaliseVolatile(rec)))
}
