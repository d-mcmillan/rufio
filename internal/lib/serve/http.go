// Package serve hosts the HTTPS transport for Rufio's MCP surface. The
// daemon exposes the same MCP tools normally reached via stdio, but across
// the network: agents on remote machines coordinate through this server,
// identity resolved per-request from a Bearer token. The local substrate
// disk on the server is the canonical store; clients reach it via
// MCP-over-HTTPS only (no direct file access).
//
// Security floor (locked in the v1.0.4 plan):
//   - TLS is mandatory unless --insecure --bind=127.0.0.1 is set.
//   - Bearer-token auth on /mcp and /listen (added in Task 4).
//   - Privacy enforcement on every read path (Task 5).
//   - Tokens are never logged in plaintext; identities are.
//
// The trust model is "trusted-collaborator" — bearer tokens are sufficient
// for one-team-runs-the-server topologies. PKI / federation is v1.2.
package serve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"

	rufiomcp "github.com/d-mcmillan/rufio/internal/mcp"
)

// Config bundles the bind/TLS/identity inputs for a single Run.
type Config struct {
	// Root is the absolute substrate root (must contain rufio.gdl).
	Root string
	// Bind is the host part of the listen address ("0.0.0.0", "127.0.0.1").
	Bind string
	// Port is the TCP port to listen on.
	Port int
	// TLSCertFile / TLSKeyFile are the PEM paths. Both empty = no TLS,
	// only legal when Insecure is true AND Bind == "127.0.0.1".
	TLSCertFile string
	TLSKeyFile  string
	// Insecure must be explicitly set to start without TLS.
	Insecure bool
	// Version is reported in the MCP server initialize response.
	Version string
	// Logf receives one-line server log events. Never receives plaintext
	// token values; only token IDs and resolved agent identities.
	// Nil falls back to a no-op.
	Logf func(format string, args ...interface{})
}

// mcpMaxBodyBytes caps the /mcp request body. 8 MB is generous for
// any realistic MCP tool invocation (the substrate doesn't accept
// arbitrary-size attachments; even the largest serialised request
// is well under a megabyte). The cap defends against memory-
// exhaustion DoS where a hostile client streams gigabytes of body
// data on a long-running POST.
//
// Security audit M2 (v1.0.5): pre-fix, the handler accepted
// unbounded bodies, which let a 401-eventual response still
// consume memory for the duration of the upload. The MaxBytesReader
// short-circuits at the cap and surfaces HTTP 413 to the client.
const mcpMaxBodyBytes int64 = 8 << 20 // 8 MB

// ErrTLSRequired is returned when Config has no TLS material and is not
// explicitly bound to localhost with the --insecure flag.
var ErrTLSRequired = errors.New("TLS is required: provide --tls-cert and --tls-key, or use --insecure --bind=127.0.0.1 for localhost dev")

// ErrInsecureNeedsLocalhost is returned when --insecure is set but the
// listener is not bound to 127.0.0.1.
var ErrInsecureNeedsLocalhost = errors.New("--insecure requires --bind=127.0.0.1 (no plaintext serving on non-localhost addresses)")

// Validate enforces the TLS-or-insecure-localhost gate. The CLI calls this
// before constructing the listener so a misconfigured serve invocation
// fails fast with an exit-2 usage error.
func (c Config) Validate() error {
	hasTLS := c.TLSCertFile != "" && c.TLSKeyFile != ""
	if hasTLS {
		return nil
	}
	if !c.Insecure {
		return ErrTLSRequired
	}
	if c.Bind != "127.0.0.1" && c.Bind != "::1" && c.Bind != "localhost" {
		return ErrInsecureNeedsLocalhost
	}
	return nil
}

// Handler builds the HTTP mux that routes /health, /mcp/, and /listen.
// Exposed so tests can drive the handler via httptest without spinning a
// real TLS listener. The /mcp and /listen routes are wrapped by
// authMiddleware (Task 4), which resolves the Bearer token to an agent
// identity and stashes it in the request context. The MCP per-request
// server constructor reads that identity via AgentFromContext so every
// tool call runs with the caller's authenticated identity.
func Handler(c Config) (http.Handler, error) {
	if c.Root == "" {
		return nil, errors.New("serve: Root must be set")
	}
	logf := c.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler())

	// MCP-over-HTTP: one server per request, with identity sourced from
	// whatever the auth middleware injected. NewServerFor lives in
	// internal/mcp/server.go alongside the existing stdio Serve helper —
	// both register the SAME tool roster against a Resolved struct, so
	// HTTPS callers see the identical surface as stdio.
	mcpHandler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		agent := AgentFromContext(req.Context())
		return rufiomcp.NewServerFor(c.Root, agent, c.Version)
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// Disable the SDK's localhost protection: it 403s when a request
		// arrives on a localhost listener with a non-localhost Host
		// header. We deliberately serve on 0.0.0.0 in deployment and
		// expect clients to use --server=<dns-name>; the protection is
		// overzealous for our trust model. CORS is N/A — this is a
		// machine-to-machine API, not a browser one.
		DisableLocalhostProtection: true,
	})

	// Auth middleware (Task 4) wraps every authenticated route. /health
	// stays open (callers probe without a token).
	//
	// Security audit M2 (v1.0.5): wrap /mcp request bodies in
	// http.MaxBytesReader with an 8 MB cap. Pre-fix, a malicious
	// client could POST a multi-GB body and exhaust the server's
	// memory before the per-request handler had a chance to reject
	// it. 8 MB is generous for legitimate MCP traffic (the largest
	// realistic payload is a `recall` response, which fits in
	// kilobytes) and bounds the damage a hostile client can do.
	//
	// /listen is NOT body-capped: SSE responses stream FROM the
	// server, not to it; the connection-cap layer (M3) is the
	// resource defence there.
	mux.Handle("/mcp/", maxBytesMiddleware(authMiddleware(c.Root, mcpHandler, logf), mcpMaxBodyBytes))
	mux.Handle("/mcp", maxBytesMiddleware(authMiddleware(c.Root, mcpHandler, logf), mcpMaxBodyBytes))
	mux.Handle("/listen", authMiddleware(c.Root, http.HandlerFunc(listenHandler(c.Root, logf)), logf))

	return mux, nil
}

// healthHandler returns 200 OK + JSON status. No authentication required —
// callers (load balancers, monitoring) need a probe path that doesn't carry
// a token.
func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// Run starts the HTTPS (or HTTP-on-localhost-when-insecure) listener. Blocks
// until ctx is done or the server fails.
func Run(ctx context.Context, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	handler, err := Handler(c)
	if err != nil {
		return err
	}
	logf := c.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	addr := net.JoinHostPort(c.Bind, fmt.Sprintf("%d", c.Port))
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// Defensive defaults — long-lived stream endpoints (/listen) tune
		// timeouts inside their handler. The general POST traffic stays
		// bounded by these.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		TLSConfig:         modernTLS(),
		// F3 follow-up: force HTTP/1.1 on the listener. SSE is
		// HTTP/1.1-native and Go's HTTP/2 server holds END_STREAM
		// frames in the writer queue when no further bytes are
		// written by the handler after the close message, which is
		// exactly the /listen-on-revoke scenario. An empty
		// TLSNextProto map disables Go's autoconfig of HTTP/2; the
		// TLS config's NextProtos pinning also blocks ALPN h2
		// negotiation as defense in depth.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}

	// Graceful shutdown on ctx cancel.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if c.TLSCertFile != "" && c.TLSKeyFile != "" {
		// Security audit L5: refuse a world- or group-readable TLS key
		// file. An operator who runs `chmod 644 key.pem` (or extracts
		// from a tarball with default 0644 perms) is one local-user
		// account compromise away from a credential leak. Refuse at
		// startup with an error pointing at the fix.
		if err := checkTLSKeyPerms(c.TLSKeyFile); err != nil {
			return err
		}
		logf("rufio serve: listening on https://%s", addr)
		err := srv.ListenAndServeTLS(c.TLSCertFile, c.TLSKeyFile)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	logf("rufio serve: listening on http://%s (INSECURE — localhost dev only)", addr)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// modernTLS returns a crypto/tls.Config restricted to TLS 1.3.
// crypto/tls' stdlib cipher-suite defaults are mature; we only narrow
// the protocol floor.
//
// Security audit L1 (must-fix): v1.0.4 ships a fresh launch surface
// — there is no legacy-compat story that justifies accepting a TLS 1.2
// downgrade from a client. TLS 1.3 has been stable + universally
// supported since 2018; every modern client + the rufio Go SDK speak
// it. Setting MinVersion = 1.3 makes a downgrade attempt
// (cipher-suite weakening, BEAST/POODLE-class attacks, etc.) impossible.
//
// F3 follow-up (cross-machine gate, 2026-05-22): NextProtos pinned to
// "http/1.1" disables HTTP/2 negotiation on the listener. SSE is
// HTTP/1.1-native; Go's HTTP/2 server keeps DATA frames in its
// writer queue and the END_STREAM frame on handler return doesn't
// flush promptly when no further data is written. The real droplet
// gate reproduced this: revoke + handler return left the existing
// /listen connection alive for many minutes. HTTP/1.1 with chunked
// encoding has no such buffering — the FIN goes out as soon as the
// handler returns. The /mcp endpoint also uses HTTP/1.1 here; the
// MCP SDK's StreamableHTTPHandler is protocol-agnostic above the
// transport and works fine over HTTP/1.1.
func modernTLS() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"http/1.1"},
	}
}

// checkTLSKeyPerms refuses to start the server when the TLS private
// key on disk is readable by group or world. The standard practice
// is 0600 (owner read/write only) or 0400 (owner read-only); anything
// looser is a one-local-user-compromise away from a credential leak.
//
// No-op on Windows where Unix permission bits aren't meaningful — the
// equivalent ACL check would be a separate code path.
//
// Returns a UsageError so the dispatcher's exit code is 2
// (configuration mistake) rather than 1 (runtime failure).
func checkTLSKeyPerms(keyFile string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(keyFile)
	if err != nil {
		return fmt.Errorf("rufio serve: cannot stat TLS key file %q: %w", keyFile, err)
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return &rufioerr.UsageError{Message: fmt.Sprintf(
			"rufio serve: TLS key file permissions too open (want 0600 or 0400, got %#o on %s). Run `chmod 600 %s` then retry.",
			mode, keyFile, keyFile,
		)}
	}
	return nil
}
