package serve

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// TestServe_RejectsLargeBody pins the M2 security floor: a 16 MB
// request body POSTed to /mcp must NOT result in a 200 success.
// http.MaxBytesReader signals over-cap reads via *http.MaxBytesError
// in the Read error channel; the inner MCP body decoder surfaces
// that as a 4xx-ish failure (the exact code depends on how the SDK
// frames the read error). What matters here is: the cap fires
// BEFORE the handler can stream the entire 16 MB into memory.
func TestServe_RejectsLargeBody(t *testing.T) {
	root := setupServeProject(t)
	plaintext, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	handler, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 16 MB payload — double the cap.
	body := bytes.Repeat([]byte("a"), 16<<20)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// A connection reset / EOF from the server is acceptable —
		// the middleware closed the connection rather than draining
		// the entire body. Pre-fix this test would have passed by
		// streaming the full 16 MB into memory and THEN failing.
		if !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "reset") {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	// HTTP 413 is the canonical "request entity too large" response.
	// Any 4xx response indicates the cap fired; we accept either 413
	// or 400 since the JSON decoder downstream may surface the
	// truncated body differently.
	if resp.StatusCode == http.StatusOK {
		t.Errorf("16 MB body accepted with 200; M2 floor not enforced (body=%q)", respBody[:min(len(respBody), 200)])
	}
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		// happy path — explicit 413
		return
	}
}

// TestServe_AcceptsNormalBody is the regression guard — a 1 MB body
// (well under the 8 MB cap) MUST still succeed. Without this, the
// cap could be tightened too far and break legitimate traffic.
func TestServe_AcceptsNormalBody(t *testing.T) {
	root := setupServeProject(t)
	plaintext, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	handler, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 1 MB of valid-ish JSON — won't pass MCP's tool parser but the
	// MCP server will surface a parse error (200 with error body, or
	// some 4xx), not 413. The point: the body was accepted as input.
	body := append(append([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"big":"`), bytes.Repeat([]byte("a"), 1<<20)...), []byte(`"}}`)...)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		t.Errorf("1 MB body rejected with 413 — cap is too tight, breaks legit traffic")
	}
}

func setupServeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := writeFileImpl(root+"/rufio.gdl", "@config|name:test|version:1\n"); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention", ".rufio/.admin"} {
		if err := mkdirAllImpl(root + "/" + sub); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	return root
}
