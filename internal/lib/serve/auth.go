package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// agentContextKey is the request-context key under which the resolved
// agent identity is stored after the auth middleware runs. Type-distinct
// so callers can't accidentally collide.
type agentContextKey struct{}

// AgentFromContext returns the resolved agent identity from the request
// context. Returns "" if no identity was resolved (anonymous /health,
// rejected auth, etc.). Callers that need authoritative identity should
// only invoke handlers that authMiddleware has wrapped.
func AgentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(agentContextKey{}).(string)
	return v
}

// withAgent attaches the resolved agent identity to ctx. Internal — only
// the auth middleware should set it.
func withAgent(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, agentContextKey{}, agent)
}

// parseBearer extracts the token plaintext from an Authorization header.
// Returns ("", false) for malformed/missing headers. Strict: case-sensitive
// "Bearer " prefix, single token, no extra fields. Defends against
// header-folding tricks by rejecting internal whitespace in the token
// payload.
func parseBearer(authHeader string) (string, bool) {
	if authHeader == "" {
		return "", false
	}
	// Security audit L4: the auth-scheme name is case-insensitive
	// per RFC 6750 §2.1 and RFC 7235 §2.1. Pre-fix, strict-case
	// HasPrefix rejected "bearer" / "BEARER" / "BeArEr" — which are
	// all valid scheme names. Use EqualFold on the scheme TOKEN
	// (everything up to the first space) so the comparison is
	// case-insensitive but doesn't allow trailing characters
	// (e.g. "Barer" still rejects).
	const scheme = "Bearer"
	if len(authHeader) <= len(scheme) || authHeader[len(scheme)] != ' ' {
		return "", false
	}
	if !strings.EqualFold(authHeader[:len(scheme)], scheme) {
		return "", false
	}
	tok := strings.TrimSpace(authHeader[len(scheme)+1:])
	if tok == "" {
		return "", false
	}
	// Reject internal whitespace — a token should be a single opaque
	// string. Defense against header-folding tricks.
	if strings.ContainsAny(tok, " \t\r\n") {
		return "", false
	}
	return tok, true
}

// authMiddleware enforces Bearer-token auth on its wrapped handler. It:
//
//  1. Extracts the Authorization header
//  2. Parses the Bearer scheme strictly
//  3. Resolves the plaintext token to an agent identity via admin.ResolveToken
//  4. Injects the identity into the request context via withAgent
//  5. Logs the token id + agent (NEVER the plaintext) for audit
//
// Invalid / missing / revoked tokens fail with 401 + a JSON error body.
// The reason field intentionally NEVER echoes the token plaintext back
// to the caller — defence-in-depth against accidental token reflection
// in logs or error responses.
//
// The same code path is used for both /mcp and /listen. /health bypasses
// auth entirely (mounted on the mux without this wrapper).
func authMiddleware(root string, next http.Handler, logf func(string, ...interface{})) http.Handler {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security audit F5: format caller-controlled fields (Method,
		// URL.Path) with %q. Go's HTTP server populates r.URL.Path
		// from the percent-DECODED path (%0A → newline), so a request
		// with an encoded newline could inject a forged log line and
		// confuse multi-line log viewers / fool alert pipelines.
		// %q escapes newlines, control bytes, and quotes — the value
		// renders on a single line as a quoted Go-string literal.
		tok, ok := parseBearer(r.Header.Get("Authorization"))
		if !ok {
			logf("auth: missing or malformed Authorization header from %s (%q %q)", r.RemoteAddr, r.Method, r.URL.Path)
			writeUnauthorized(w, "missing or malformed Authorization header (expect 'Bearer <token>')")
			return
		}
		agent, err := admin.ResolveToken(root, tok)
		if err != nil {
			if errors.Is(err, admin.ErrTokenInvalid) {
				// Log the token id (derived from hash, never
				// the plaintext) for audit. We do NOT log the
				// plaintext or any prefix of it — even partial
				// plaintext is a forensic risk.
				logf("auth: token rejected (%s) from %s (%q %q)", admin.TokenIDFromPlaintext(tok), r.RemoteAddr, r.Method, r.URL.Path)
				writeUnauthorized(w, "token invalid or revoked")
				return
			}
			// Internal error — don't leak details to the caller.
			// Log a redacted token id for diagnosis.
			logf("auth: resolve error for token %s: %v", admin.TokenIDFromPlaintext(tok), err)
			writeUnauthorized(w, "token resolution failed")
			return
		}
		logf("auth: %s -> %s (%q %q)", admin.TokenIDFromPlaintext(tok), agent, r.Method, r.URL.Path)
		r = r.WithContext(withAgent(r.Context(), agent))
		next.ServeHTTP(w, r)
	})
}

// writeUnauthorized is the canonical 401 response — always JSON, never
// echoes any part of the offending header back to the caller.
//
// Security audit L2: emits WWW-Authenticate per RFC 7235 §4.1. The
// challenge announces the Bearer scheme + realm="rufio" so standards-
// compliant HTTP clients know what credential type to send next.
func writeUnauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="rufio"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":  "unauthorized",
		"reason": reason,
	})
}
