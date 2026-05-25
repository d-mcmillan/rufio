package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initProject scaffolds a minimal rufio.gdl + .rufio dir so the server's
// resolve path is happy. We don't need the full init machinery — only the
// project marker plus an empty .admin dir so the auth middleware doesn't
// fail open while reading a missing tokens file.
func initProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte("@config|name:test|version:1\n"), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rufio", ".admin"), 0o700); err != nil {
		t.Fatalf("mkdir .admin: %v", err)
	}
	return root
}

func TestConfig_Validate_RequiresTLS(t *testing.T) {
	c := Config{Root: "/tmp", Bind: "0.0.0.0", Port: 8443}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error without TLS")
	}
	if err != ErrTLSRequired {
		t.Errorf("expected ErrTLSRequired, got %v", err)
	}
}

func TestConfig_Validate_InsecureRequiresLocalhost(t *testing.T) {
	c := Config{Root: "/tmp", Bind: "0.0.0.0", Port: 8443, Insecure: true}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error when --insecure on non-localhost bind")
	}
	if err != ErrInsecureNeedsLocalhost {
		t.Errorf("expected ErrInsecureNeedsLocalhost, got %v", err)
	}
}

func TestConfig_Validate_InsecureLocalhost(t *testing.T) {
	c := Config{Root: "/tmp", Bind: "127.0.0.1", Port: 8443, Insecure: true}
	if err := c.Validate(); err != nil {
		t.Errorf("--insecure --bind=127.0.0.1 should be allowed: %v", err)
	}
}

func TestConfig_Validate_TLSAccepted(t *testing.T) {
	c := Config{Root: "/tmp", Bind: "0.0.0.0", Port: 8443, TLSCertFile: "/x", TLSKeyFile: "/y"}
	if err := c.Validate(); err != nil {
		t.Errorf("TLS material should validate: %v", err)
	}
}

func TestHealthEndpoint_BypassesAuth(t *testing.T) {
	root := initProject(t)
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

func TestMCPEndpoint_RequiresAuth(t *testing.T) {
	root := initProject(t)
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without Authorization header, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestAgentFromContext_EmptyWhenUnset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := AgentFromContext(req.Context()); got != "" {
		t.Errorf("expected empty agent in untouched context, got %q", got)
	}
}
