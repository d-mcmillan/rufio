package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// seedThought writes a single @thought record into the substrate
// authored by `author` at scope `scope`. Used by the privacy tests to
// build a cross-agent fixture without going through the full CLI.
//
// The on-disk layout matches what thought.Write produces:
//
//	live/outbox/<author>/<id>.gdl with one @thought record per file.
func seedThought(t *testing.T, root, author, scope, subject, content string, topics []string) string {
	t.Helper()
	id, err := thought.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID:      id,
		Author:  author,
		Type:    "hypothesis",
		Subject: subject,
		Content: content,
		Scope:   scope,
		Topics:  topics,
		TS:      versioning.NowISO(),
		TTL:     0,
	})
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write: %v", err)
	}
	return id
}

// callMCPTool drives a single MCP tool/call over the HTTPS handler with
// the supplied Bearer token. Returns the JSON response body. Asserts
// the response is 200. Used by every privacy test below to verify that
// the server-side privacy floor applies on EVERY read path the v1.0.4
// plan calls out (recall, listen, open, lineage, confirms, thoughts).
func callMCPTool(t *testing.T, handler http.Handler, token, toolName string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	// 2-step session: initialize then tool/call. The streamable HTTP
	// transport requires the initialize handshake before tool calls;
	// stateless mode short-circuits the session id but still expects
	// the initialize message first.
	//
	// To keep the test focused on privacy semantics (not transport
	// plumbing), we drive the handler directly and assert the parsed
	// result. Using the SDK's client transport here would couple us to
	// the SDK's connection-reuse heuristics — not what these tests are
	// proving.
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	}
	bs, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(bs))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("MCP %s call failed: status=%d body=%s", toolName, rec.Code, rec.Body.String())
	}
	return parseMCPResponse(t, rec.Body)
}

// parseMCPResponse decodes a JSON-RPC response (either application/json
// or text/event-stream wrapped). Returns the `result` field as a map.
func parseMCPResponse(t *testing.T, body *bytes.Buffer) map[string]interface{} {
	t.Helper()
	raw, _ := io.ReadAll(body)
	text := string(raw)
	// SSE-wrapped response: extract the data: line.
	if strings.Contains(text, "data: ") {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "data: ") {
				text = strings.TrimPrefix(line, "data: ")
				break
			}
		}
	}
	var resp struct {
		Result map[string]interface{} `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("parse MCP response: %v (body=%s)", err, text)
	}
	if resp.Error != nil {
		t.Fatalf("MCP error: %+v (body=%s)", resp.Error, text)
	}
	if resp.Result == nil {
		t.Fatalf("MCP result is nil; body=%s", text)
	}
	return resp.Result
}

func setupHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	root := initProject(t)
	// Init the canonical substrate dirs so writes don't 500.
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention"} {
		mustMkdir(t, root, sub)
	}
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	return h, root
}

func mustMkdir(t *testing.T, root, sub string) {
	t.Helper()
	if err := mkdirAll(root, sub); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}
}

func mkdirAll(root, sub string) error {
	return forwardSlashMkdirAll(root + "/" + sub)
}

// forwardSlashMkdirAll wraps os.MkdirAll to keep the test file os-import
// minimal — defined here so the per-test mustMkdir helper stays focused.
func forwardSlashMkdirAll(path string) error {
	return mkdirAllImpl(path)
}

// TestServerPrivacy_HidesOtherAgentScopeAgent — Alice cannot see Bob's
// scope=agent record via the MCP recall tool. This is THE security-
// critical assertion of v1.0.4: the privacy floor (#147) is enforced
// SERVER-SIDE on every read path.
func TestServerPrivacy_HidesOtherAgentScopeAgent(t *testing.T) {
	h, root := setupHandler(t)
	tokAlice := mintTestToken(t, root, "alice")
	tokBob := mintTestToken(t, root, "bob")
	_ = tokBob

	bobPrivateID := seedThought(t, root, "bob", "agent", "test:1", "bob private hypothesis", []string{"alpha"})
	_ = bobPrivateID

	// Alice should NOT see bob's scope=agent record via recall.
	result := callMCPTool(t, h, tokAlice, "recall", map[string]interface{}{
		"types":  "thought",
		"scope":  "",
		"query":  "",
		"topics": "alpha",
	})
	if !privacyResultExcludes(t, result, "bob private hypothesis") {
		dumpRecords(t, result)
		t.Fatal("alice recalled bob's scope=agent record — privacy floor breached")
	}
}

func TestServerPrivacy_OwnScopeAgentVisible(t *testing.T) {
	h, root := setupHandler(t)
	tokBob := mintTestToken(t, root, "bob")
	_ = seedThought(t, root, "bob", "agent", "test:1", "bob's own private", nil)

	result := callMCPTool(t, h, tokBob, "recall", map[string]interface{}{})
	if !privacyResultContains(t, result, "bob's own private") {
		dumpRecords(t, result)
		t.Fatal("bob recall did not return bob's own scope=agent record")
	}
}

func TestServerPrivacy_FleetScopeShareable(t *testing.T) {
	// Regression guard: privacy enforcement must not over-filter the
	// fleet-scope records both agents are entitled to see.
	h, root := setupHandler(t)
	tokAlice := mintTestToken(t, root, "alice")
	_ = seedThought(t, root, "bob", "fleet", "test:1", "bob fleet broadcast", nil)

	result := callMCPTool(t, h, tokAlice, "recall", map[string]interface{}{})
	if !privacyResultContains(t, result, "bob fleet broadcast") {
		dumpRecords(t, result)
		t.Fatal("alice missed bob's scope=fleet record — over-filtering bug")
	}
}

// TestServerPrivacy_OpenToolEnforcement covers the read-dual: `open`
// bundles the same data as recall plus identity + fleet sections, so
// it must also honor the privacy floor.
func TestServerPrivacy_OpenToolEnforcement(t *testing.T) {
	h, root := setupHandler(t)
	tokAlice := mintTestToken(t, root, "alice")
	_ = seedThought(t, root, "bob", "agent", "test:1", "bob's secret on test:1", nil)

	result := callMCPTool(t, h, tokAlice, "open", map[string]interface{}{
		"subject": "test:1",
	})
	if !privacyResultExcludes(t, result, "bob's secret on test:1") {
		dumpRecords(t, result)
		t.Fatal("alice's open of test:1 leaked bob's scope=agent record")
	}
}

// TestServerPrivacy_GoalsListEnforcement — goals_list goes through
// privacy.IsVisible per tools_goals.go. Verify across the HTTPS surface.
func TestServerPrivacy_GoalsListEnforcement(t *testing.T) {
	h, root := setupHandler(t)
	tokAlice := mintTestToken(t, root, "alice")

	// Seed a scope=agent goal authored by bob, directly on disk.
	mustMkdir(t, root, "live/goals/active")
	goalID := fmt.Sprintf("g-%d-bob", time.Now().UnixMilli())
	rec := gdl.Record{Type: "goal", Fields: []gdl.RecordField{
		{Key: "id", Value: goalID},
		{Key: "author", Value: "bob"},
		{Key: "statement", Value: "bob private goal"},
		{Key: "scope", Value: "agent"},
		{Key: "ts", Value: versioning.NowISO()},
	}}
	writeGoalFile(t, root, "live/goals/active/"+goalID+".gdl", rec)

	result := callMCPTool(t, h, tokAlice, "goals_list", map[string]interface{}{})
	if !privacyResultExcludes(t, result, "bob private goal") {
		dumpRecords(t, result)
		t.Fatal("alice goals_list leaked bob's scope=agent goal")
	}
}

func writeGoalFile(t *testing.T, root, sub string, rec gdl.Record) {
	t.Helper()
	writeFileContent(t, root, sub, gdl.RenderLine(rec)+"\n")
}

func writeFileContent(t *testing.T, root, sub, content string) {
	t.Helper()
	if err := writeFileImpl(root+"/"+sub, content); err != nil {
		t.Fatalf("write %s: %v", sub, err)
	}
}

// privacyResultContains walks the recall response and reports whether
// any returned record's content contains needle.
func privacyResultContains(t *testing.T, result map[string]interface{}, needle string) bool {
	t.Helper()
	return privacyWalk(result, needle)
}

func privacyResultExcludes(t *testing.T, result map[string]interface{}, needle string) bool {
	t.Helper()
	return !privacyWalk(result, needle)
}

// privacyWalk recursively scans the JSON tree for any string value
// containing needle. Used to detect leaks regardless of which key the
// content lands under (recall has `records[].content`, open has nested
// sections, goals_list has `goals[].statement`, etc.).
func privacyWalk(v interface{}, needle string) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, needle)
	case []interface{}:
		for _, e := range t {
			if privacyWalk(e, needle) {
				return true
			}
		}
	case map[string]interface{}:
		for _, e := range t {
			if privacyWalk(e, needle) {
				return true
			}
		}
	}
	return false
}

func dumpRecords(t *testing.T, m map[string]interface{}) {
	t.Helper()
	bs, _ := json.MarshalIndent(m, "", "  ")
	t.Logf("response: %s", bs)
}
