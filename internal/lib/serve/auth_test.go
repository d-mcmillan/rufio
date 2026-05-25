package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// captureLogf collects every log line for assertion. Useful to prove the
// auth middleware never logs the plaintext token.
type captureLogf struct {
	mu    sync.Mutex
	lines []string
}

func (c *captureLogf) Logf(format string, args ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func (c *captureLogf) All() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

func dummyNext() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent := AgentFromContext(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"agent": agent})
	})
}

func mintTestToken(t *testing.T, root, agent string) string {
	t.Helper()
	plaintext, _, err := admin.MintToken(root, agent)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	return plaintext
}

func TestAuthMiddleware_RejectsMissingHeader(t *testing.T) {
	root := initProject(t)
	cap := &captureLogf{}
	h := authMiddleware(root, dummyNext(), cap.Logf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "rufio_") {
		t.Errorf("response body must not leak token plaintext: %s", rec.Body.String())
	}
}

func TestAuthMiddleware_RejectsInvalidToken(t *testing.T) {
	root := initProject(t)
	h := authMiddleware(root, dummyNext(), nil)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer rufio_invalid_token_value")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_RejectsRevokedToken(t *testing.T) {
	root := initProject(t)
	plaintext, tok, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := admin.RevokeToken(root, tok.ID); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	h := authMiddleware(root, dummyNext(), nil)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token should produce 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AcceptsValidToken_InjectsIdentity(t *testing.T) {
	root := initProject(t)
	plaintext := mintTestToken(t, root, "alice")

	h := authMiddleware(root, dummyNext(), nil)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token should pass, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["agent"] != "alice" {
		t.Errorf("expected agent=alice in context, got %q", body["agent"])
	}
}

func TestAuthMiddleware_NeverLogsPlaintextToken(t *testing.T) {
	root := initProject(t)
	plaintext := mintTestToken(t, root, "alice")

	cap := &captureLogf{}
	h := authMiddleware(root, dummyNext(), cap.Logf)

	// Happy path: send valid token.
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Bad path: send invalid token (likely to be logged for diagnostics).
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req2.Header.Set("Authorization", "Bearer rufio_someoneswroteatypoinmytoken")
	h.ServeHTTP(httptest.NewRecorder(), req2)

	logs := cap.All()
	if strings.Contains(logs, plaintext) {
		t.Errorf("logs must not contain the plaintext token; logs=%s", logs)
	}
	if strings.Contains(logs, "rufio_someoneswroteatypoinmytoken") {
		t.Errorf("logs must not contain the invalid token plaintext; logs=%s", logs)
	}
}

func TestAuthMiddleware_RejectsMalformedScheme(t *testing.T) {
	root := initProject(t)
	// Note (security audit L4): the Bearer scheme name is now
	// case-insensitive per RFC 6750/7235; "bearer rufio_xyz" no
	// longer counts as malformed. Truly bad inputs (typos, missing
	// space, extra fields) still reject.
	cases := []string{
		"Basic rufio_xyz",        // wrong scheme
		"Bearer",                 // no space
		"Bearer ",                // no token
		"Barer rufio_xyz",        // typo (Barer ≠ Bearer)
		"Bearer rufio_xyz extra", // extra fields rejected
	}
	for _, hv := range cases {
		t.Run(hv, func(t *testing.T) {
			h := authMiddleware(root, dummyNext(), nil)
			req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
			req.Header.Set("Authorization", hv)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 for %q, got %d", hv, rec.Code)
			}
		})
	}
}

func TestAuthMiddleware_HealthEndpointBypasses(t *testing.T) {
	root := initProject(t)
	// /health is registered on the mux WITHOUT authMiddleware wrap,
	// so it should be reachable without a token. Verify by going
	// through the Handler() entry point (not the bare middleware).
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/health should bypass auth (200), got %d", rec.Code)
	}
}

func TestParseBearer(t *testing.T) {
	// Note (security audit L4): scheme name is case-insensitive per
	// RFC 6750/7235 — "bearer rufio_xyz" is valid; "Barer rufio_xyz"
	// is a typo and stays rejected. See TestParseBearer_CaseInsensitive
	// for the full case-fold matrix.
	cases := []struct {
		header  string
		want    string
		wantOK  bool
		comment string
	}{
		{"", "", false, "empty header rejected"},
		{"Bearer ", "", false, "bare prefix rejected"},
		{"Bearer  ", "", false, "whitespace-only payload rejected"},
		{"Bearer rufio_xyz", "rufio_xyz", true, "valid token"},
		{"Bearer rufio_a b", "", false, "internal whitespace rejected"},
		{"Bearer\trufio_xyz", "", false, "tab separator rejected"},
		{"Basic foo", "", false, "wrong scheme rejected"},
		{"Barer foo", "", false, "typo'd scheme name rejected"},
	}
	for _, c := range cases {
		got, ok := parseBearer(c.header)
		if ok != c.wantOK || got != c.want {
			t.Errorf("parseBearer(%q): got (%q,%v), want (%q,%v) — %s", c.header, got, ok, c.want, c.wantOK, c.comment)
		}
	}
}

// Fuzz the bearer parser with malformed inputs to make sure it never
// panics or returns true for anything that isn't a clean "Bearer <token>"
// shape.
func FuzzParseBearer(f *testing.F) {
	for _, seed := range []string{"", "Bearer", "Bearer ", "Bearer rufio_x", "Authorization Bearer X", "Bearer\x00rufio_x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, header string) {
		_, _ = parseBearer(header) // must never panic
	})
}

// TestAuthLog_QuotesURL (security audit F5). Pre-fix, the auth
// middleware logged r.URL.Path via %s. Go's HTTP server populates
// r.URL.Path from the DECODED path (percent-encoded %0A → newline).
// An attacker submitting a request with %0A in the URL could inject
// a forged log entry — multi-line viewers would see a fabricated
// auth-success line for an admin agent. Forensic-confusion + alert-
// fooling.
//
// Fix: format caller-controlled fields with %q so newlines/control
// bytes render as literal backslash-escapes rather than actual line
// breaks.
//
// We construct the request manually (httptest.NewRequest does its
// own URL parsing) and set r.URL directly with the injected payload.
func TestAuthLog_QuotesURL(t *testing.T) {
	root := initProject(t)
	cap := &captureLogf{}
	h := authMiddleware(root, dummyNext(), cap.Logf)

	// Forge a URL.Path that contains a literal newline + a forged log line.
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.URL.Path = "/mcp\n2026-05-22 auth: tok-fakefake -> admin (GET /mcp)"
	// No Authorization header → falls into the "missing/malformed"
	// branch which logs the URL.Path.
	h.ServeHTTP(httptest.NewRecorder(), req)

	logs := cap.All()
	// The log line MUST contain the escaped form (\n as literal
	// backslash-n) and MUST NOT contain a raw newline that splits
	// into a forged line.
	if !strings.Contains(logs, `\n`) {
		t.Errorf("log should contain escaped \\n; got: %q", logs)
	}
	// Defense: ensure the literal "auth:" string in the injected
	// payload (after a real newline) is NOT followed by a real
	// newline. We do this by checking that the captured single log
	// line still contains "/mcp\\n" as one substring — proof that
	// %q quoted the field.
	for _, line := range strings.Split(logs, "\n") {
		// Each line should NOT itself end with a forged tail. Look
		// for the marker we injected; if it landed in a line OTHER
		// than the one that contains the original /mcp prefix, the
		// injection succeeded.
		if strings.Contains(line, "tok-fakefake") && !strings.Contains(line, "/mcp") {
			t.Errorf("forged auth log line found in isolation — log injection succeeded:\n%s", logs)
		}
	}
}

// TestAuthLog_QuotesMethod pins the second caller-controlled field.
// HTTP method is even less constrained than the path; Go accepts
// arbitrary non-control bytes here.
func TestAuthLog_QuotesMethod(t *testing.T) {
	root := initProject(t)
	cap := &captureLogf{}
	h := authMiddleware(root, dummyNext(), cap.Logf)

	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Method = "GET\nFAKE"
	h.ServeHTTP(httptest.NewRecorder(), req)
	logs := cap.All()
	if !strings.Contains(logs, `GET\nFAKE`) {
		t.Errorf("method should be quoted with \\n escape; got: %q", logs)
	}
}

// TestParseBearer_CaseInsensitive (security audit L4). RFC 6750 §2.1
// + RFC 7235 §2.1 specify that the auth scheme name is case-
// insensitive. Pre-fix, parseBearer used strict strings.HasPrefix
// against "Bearer ", which rejected "bearer ", "BEARER ", "BeArEr "
// — all of which are valid per the spec.
func TestParseBearer_CaseInsensitive(t *testing.T) {
	cases := []string{
		"Bearer rufio_xyz",
		"bearer rufio_xyz",
		"BEARER rufio_xyz",
		"BeArEr rufio_xyz",
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			got, ok := parseBearer(h)
			if !ok {
				t.Errorf("parseBearer(%q) rejected case variant — scheme name must be case-insensitive", h)
			}
			if got != "rufio_xyz" {
				t.Errorf("parseBearer(%q) = %q, want %q", h, got, "rufio_xyz")
			}
		})
	}
}

// TestParseBearer_StillRejectsMalformed pins the bound: case
// insensitivity applies to the scheme name "Bearer" only. Typos
// ("Barer"), wrong schemes ("Basic"), tab separators, and missing
// space stay rejected. Extra leading whitespace AFTER the scheme +
// space prefix is tolerated (existing TrimSpace behavior) — that's
// not a malformation per RFC 7235, just a sloppy client.
func TestParseBearer_StillRejectsMalformed(t *testing.T) {
	cases := []string{
		"Barer rufio_xyz",   // typo'd scheme name
		"Bearer\trufio_xyz", // tab instead of space — RFC 7235 says SP
		"Bearerrufio_xyz",   // no separator at all
		"Basic rufio_xyz",   // wrong scheme entirely
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			if _, ok := parseBearer(h); ok {
				t.Errorf("parseBearer(%q) accepted a malformed header — must reject", h)
			}
		})
	}
}

// TestAuthMiddleware_SetsWWWAuthenticateOn401 (security audit L2).
// RFC 7235 §4.1 requires a 401 response to carry a WWW-Authenticate
// header announcing the supported challenge schemes. Standards-
// compliant HTTP clients rely on this to know what credential type
// to send next; pre-fix our 401s shipped the JSON body alone.
func TestAuthMiddleware_SetsWWWAuthenticateOn401(t *testing.T) {
	root := initProject(t)
	h := authMiddleware(root, dummyNext(), nil)
	cases := []struct {
		name string
		hv   string
	}{
		{"missing-header", ""},
		{"invalid-token", "Bearer rufio_definitely_not_a_real_token"},
		{"wrong-scheme", "Basic foo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
			if c.hv != "" {
				req.Header.Set("Authorization", c.hv)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
			got := rec.Header().Get("WWW-Authenticate")
			want := `Bearer realm="rufio"`
			if got != want {
				t.Errorf("WWW-Authenticate=%q, want %q", got, want)
			}
		})
	}
}
