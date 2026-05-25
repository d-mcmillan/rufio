package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
	"github.com/d-mcmillan/rufio/internal/lib/serve"
)

// initProject scaffolds the minimum substrate shape (rufio.gdl + dirs)
// the remote tests need. The serve handler reads the project root for
// real, so we need it to look like an initialised project.
func initProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte("@config|name:test|version:1\n"), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention", ".rufio/.admin"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	return root
}

func startServer(t *testing.T) (string, string, string) {
	t.Helper()
	root := initProject(t)
	plaintext, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	handler, err := serve.Handler(serve.Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return root, ts.URL, plaintext
}

func TestRemote_CallTool_Recall(t *testing.T) {
	_, baseURL, token := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := Dial(ctx, Config{
		Endpoint:    baseURL + "/mcp",
		Token:       token,
		InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	res, err := c.CallTool(ctx, "recall", map[string]interface{}{})
	if err != nil {
		t.Fatalf("CallTool recall: %v", err)
	}
	// Empty substrate → recall returns {records: []}.
	if _, ok := res["records"]; !ok {
		t.Errorf("expected records key in recall response; got %#v", res)
	}
}

func TestRemote_NoToken_RejectsConnect(t *testing.T) {
	_, baseURL, _ := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := Dial(ctx, Config{Endpoint: baseURL + "/mcp", Token: ""})
	if err == nil {
		t.Fatal("expected Dial to fail without token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error should mention token, got %v", err)
	}
}

func TestRemote_InvalidToken_FailsCallTool(t *testing.T) {
	_, baseURL, _ := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := Dial(ctx, Config{
		Endpoint:    baseURL + "/mcp",
		Token:       "rufio_definitely_not_a_real_token",
		InsecureTLS: true,
	})
	if err == nil {
		t.Fatal("expected Dial to fail with invalid token (initialize should 401)")
	}
}

func TestRemote_Identity_FromTokenNotEnv(t *testing.T) {
	// Mint a token for bob, then attempt to call attend via the remote.
	// The server logs identity = bob regardless of any client-side
	// RUFIO_AGENT_ID. We can't directly inspect logs here, but the
	// response payload of the attend tool echoes the agent the server
	// resolved (server-authoritative identity).
	root := initProject(t)
	bobToken, _, err := admin.MintToken(root, "bob")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	handler, err := serve.Handler(serve.Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Set RUFIO_AGENT_ID to "alice" — a malicious client trying to
	// impersonate. The server MUST ignore this and use bob (the token's
	// resolved identity).
	t.Setenv("RUFIO_AGENT_ID", "alice")

	ctx := context.Background()
	c, err := Dial(ctx, Config{
		Endpoint:    ts.URL + "/mcp",
		Token:       bobToken,
		InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	res, err := c.CallTool(ctx, "attend", map[string]interface{}{
		"intent":   "testing identity",
		"entities": []string{"test:1"},
		"scope":    "fleet",
	})
	if err != nil {
		t.Fatalf("CallTool attend: %v", err)
	}
	if agent, _ := res["agent"].(string); agent != "bob" {
		t.Errorf("server should resolve identity from token (bob), not env (alice); got %q", agent)
	}
}

// TestRemoteClient_RefusesHTTPScheme (security audit M5): the client
// MUST refuse to send a bearer token over plaintext http:// unless the
// operator explicitly opts in via InsecureTLS AND the host is loopback.
// Pre-fix, a typo of https→http in --server= would silently ship the
// token in the clear.
func TestRemoteClient_RefusesHTTPScheme(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cases := []struct {
		name     string
		endpoint string
		insecure bool
	}{
		{"plain-http-public", "http://example.com:8080/mcp", false},
		// InsecureTLS is honoured ONLY for loopback hosts; a non-
		// loopback http:// must still be refused even with the flag.
		{"plain-http-public-with-insecure", "http://example.com:8080/mcp", true},
		{"weird-scheme", "ftp://example.com/mcp", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Dial(ctx, Config{
				Endpoint:    c.endpoint,
				Token:       "rufio_dummy",
				InsecureTLS: c.insecure,
			})
			if err == nil {
				t.Errorf("Dial accepted %q insecure=%v — must refuse plaintext/foreign scheme", c.endpoint, c.insecure)
			}
		})
	}
}

// TestRemoteClient_AllowsHTTPLoopbackWithInsecure: the localhost dev
// path (http://127.0.0.1 + --insecure-tls) MUST keep working — this
// is the smoke loop and the localhost validation gate.
func TestRemoteClient_AllowsHTTPLoopbackWithInsecure(t *testing.T) {
	root := initProject(t)
	plaintext, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	handler, err := serve.Handler(serve.Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	for _, host := range []string{"127.0.0.1", "localhost"} {
		// Replace the host portion of the httptest URL so we
		// exercise both the canonical IPv4 loopback AND the
		// "localhost" hostname form.
		base := strings.Replace(ts.URL, "127.0.0.1", host, 1)
		ctx, cancel := context.WithCancel(context.Background())
		c, err := Dial(ctx, Config{
			Endpoint:    base + "/mcp",
			Token:       plaintext,
			InsecureTLS: true,
		})
		cancel()
		if err != nil {
			t.Errorf("Dial(%s/mcp) with InsecureTLS=true should succeed (loopback dev path): %v", base, err)
			continue
		}
		_ = c.Close()
	}
}

// TestRemoteClient_HTTPSAlwaysAllowed: the safe default must pass the
// scheme gate for any host. We point at an unroutable port so the TCP
// connect fails AFTER the scheme check succeeds — the error we see
// must NOT be a scheme-refusal.
func TestRemoteClient_HTTPSAlwaysAllowed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := Dial(ctx, Config{
		Endpoint: "https://127.0.0.1:1/mcp", // refused — but scheme gate must pass
		Token:    "rufio_dummy",
	})
	if err == nil {
		t.Skip("unexpected: 127.0.0.1:1 is serving MCP")
	}
	if strings.Contains(err.Error(), "refusing to send bearer token") || strings.Contains(err.Error(), "scheme must be https") {
		t.Errorf("https endpoint should pass the scheme gate; got %v", err)
	}
}

// TestBearerRoundTripper_RefusesNonEndpointHost (security audit F1).
// The bearer-injecting RoundTripper MUST compare req.URL.Host to the
// configured endpoint host BEFORE attaching Authorization. Pre-fix, a
// 302 redirect from the rufio server to evil.com would land the bearer
// token at the redirect target — Go's stdlib auth-stripping doesn't
// fire because the header is injected in the RoundTripper layer, not
// on the original *http.Request.
func TestBearerRoundTripper_RefusesNonEndpointHost(t *testing.T) {
	rt := &bearerRoundTripper{
		base:         http.DefaultTransport,
		token:        "rufio_secret",
		endpointHost: "rufio.example.com:8443",
	}
	// Construct a request to a DIFFERENT host. RoundTrip must refuse
	// before any network I/O.
	req, err := http.NewRequest(http.MethodPost, "http://evil.example.com:8443/mcp", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("RoundTrip accepted a different-host request — bearer would leak")
	}
	if !strings.Contains(err.Error(), "refusing to send bearer") {
		t.Errorf("error should mention 'refusing to send bearer'; got %v", err)
	}
}

// TestBearerRoundTripper_HostMatchAttachesHeader pins the happy path:
// when req.URL.Host matches the configured endpoint host, the bearer
// is injected as before.
func TestBearerRoundTripper_HostMatchAttachesHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	rt := &bearerRoundTripper{
		base:         http.DefaultTransport,
		token:        "rufio_secret",
		endpointHost: u.Host,
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if gotAuth != "Bearer rufio_secret" {
		t.Errorf("expected Authorization=%q, got %q", "Bearer rufio_secret", gotAuth)
	}
}

// TestClient_RefusesCrossHostRedirect (security audit F1 second layer).
// Even with the bearerRoundTripper host check in place, a defense-in-
// depth CheckRedirect policy on the http.Client refuses cross-host
// redirects entirely. This catches any redirect-following code path
// the SDK's underlying transport might invoke.
//
// We spin up two servers: A acts as the rufio endpoint and returns
// 302→B. The client MUST refuse to follow the redirect, AND server B
// MUST NEVER see the Authorization header.
func TestClient_RefusesCrossHostRedirect(t *testing.T) {
	// Server B = the evil target. Records any Authorization header it
	// sees. If we see ANY auth header here, the bearer leaked.
	bAuthHeaders := make([]string, 0, 4)
	var bMu sync.Mutex
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bMu.Lock()
		bAuthHeaders = append(bAuthHeaders, r.Header.Get("Authorization"))
		bMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer serverB.Close()

	// Server A redirects to B. Pretends to be the configured rufio endpoint.
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, serverB.URL+"/mcp", http.StatusFound)
	}))
	defer serverA.Close()

	uA, _ := url.Parse(serverA.URL)
	rt := &bearerRoundTripper{
		base:         http.DefaultTransport,
		token:        "rufio_secret_must_not_leak",
		endpointHost: uA.Host,
	}
	client := &http.Client{
		Transport:     rt,
		CheckRedirect: noCrossHostRedirect,
		Timeout:       2 * time.Second,
	}
	resp, err := client.Get(serverA.URL + "/mcp")
	if err == nil {
		_ = resp.Body.Close()
	}
	// Either CheckRedirect raised an error OR bearerRoundTripper
	// refused to attach. What we MUST NOT see is the bearer arriving
	// at server B.
	bMu.Lock()
	defer bMu.Unlock()
	for _, h := range bAuthHeaders {
		if strings.Contains(h, "rufio_secret_must_not_leak") {
			t.Errorf("bearer leaked to cross-host redirect target; auth header at B = %q", h)
		}
	}
}
