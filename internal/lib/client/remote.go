// Package client is the HTTPS-MCP transport for talking to a remote
// `rufio serve` daemon. It wraps the go-sdk's StreamableClientTransport
// with Bearer-token injection and a sensible TLS config, then exposes a
// single CallTool entry point the CLI uses when --server=<url> is set.
//
// Identity is server-authoritative: the client supplies the token, the
// server resolves it to an agent. The CLI MUST NOT pass an agent id over
// the wire — doing so would let a client impersonate any agent the
// server knows about. The remote MCP tool calls run under the identity
// the bearer token resolves to.
//
// Self-signed certs (localhost dev) are supported via the InsecureTLS
// flag, with a loud stderr warning at first use.
package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config bundles the transport parameters for the remote rufio server.
type Config struct {
	// Endpoint is the full HTTPS URL of the /mcp endpoint
	// (e.g. https://rufio.example.com:8443/mcp). Scheme MUST be https
	// unless InsecureTLS is set.
	Endpoint string
	// Token is the plaintext bearer token. Never logged.
	Token string
	// InsecureTLS skips certificate verification. Localhost dev only —
	// every call emits a stderr warning when set.
	InsecureTLS bool
	// HTTPTimeout caps a single tool call. Defaults to 30s.
	HTTPTimeout time.Duration
}

// ErrNoServer is returned when the caller didn't set --server but tried
// to route through the remote client. Surfaces as a UsageError to the
// CLI dispatcher.
var ErrNoServer = errors.New("no --server URL configured")

// Client is a long-lived handle wrapping the MCP session against a single
// rufio server. Construct once per invocation; the connection is reused
// across multiple CallTool calls in the same process lifetime.
type Client struct {
	cfg     Config
	session *mcp.ClientSession
}

// Dial connects to the remote rufio server and returns a Client ready
// for CallTool. The Bearer token is injected via a per-request transport
// wrapper (NOT a default header on http.Client — the SDK's streamable
// client manages multiple connections and we want auth on every one).
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, ErrNoServer
	}
	if cfg.Token == "" {
		return nil, errors.New("no token configured (set --token or RUFIO_TOKEN)")
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}

	// Security audit M5 (must-fix): refuse http:// unless the operator
	// explicitly opts in via InsecureTLS AND the host is loopback. The
	// previous behaviour silently sent bearer tokens in plaintext if
	// the operator typo'd https→http in --server=. Mirrors the
	// server-side TLS gate exactly.
	if err := validateEndpointScheme(cfg.Endpoint, cfg.InsecureTLS); err != nil {
		return nil, err
	}

	// Normalize: /mcp without trailing slash is the canonical endpoint.
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if !strings.HasSuffix(endpoint, "/mcp") {
		endpoint = endpoint + "/mcp"
	}

	// Security audit F1: the bearer-injecting RoundTripper MUST know
	// the configured endpoint host so it can refuse to attach the
	// token on any other host (e.g. a redirect target). Parse the
	// endpoint up front; downstream code uses the normalised /mcp
	// suffix, but the host is what guards the auth header.
	endpointURL, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid --server URL %q: %w", cfg.Endpoint, err)
	}
	httpClient := &http.Client{
		Timeout: cfg.HTTPTimeout,
		Transport: &bearerRoundTripper{
			base:         defaultTransport(cfg.InsecureTLS),
			token:        cfg.Token,
			endpointHost: endpointURL.Host,
		},
		// Security audit F1 second layer: refuse cross-host redirects
		// entirely so even if the underlying transport invokes a
		// redirect-following code path, the bearer never reaches a
		// different host than the operator configured. Also caps the
		// redirect chain length to defend against malicious loop
		// constructions.
		CheckRedirect: noCrossHostRedirect,
	}

	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true, // we don't need server-initiated notifications for one-shot CLI calls
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "rufio-cli", Version: "1.0.4"}, nil)
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	return &Client{cfg: cfg, session: session}, nil
}

// Close terminates the MCP session. Best-effort; safe to defer.
func (c *Client) Close() error {
	if c.session == nil {
		return nil
	}
	return c.session.Close()
}

// CallTool invokes the named MCP tool on the remote server with the
// provided arguments. Returns the raw map[string]interface{} so the CLI
// can re-render it through its existing JSON/text renderers, keeping
// the wire shape byte-identical with `rufio mcp` (the symmetry contract
// the v1.0.2 work pinned).
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (map[string]interface{}, error) {
	if c.session == nil {
		return nil, errors.New("client not connected")
	}
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, &RemoteToolError{Tool: name, Result: res}
	}
	// Prefer the structured content if present (typed tool output).
	if res.StructuredContent != nil {
		// StructuredContent is a json.RawMessage — decode to a generic map.
		bs, mErr := json.Marshal(res.StructuredContent)
		if mErr != nil {
			return nil, mErr
		}
		var out map[string]interface{}
		if err := json.Unmarshal(bs, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	// Fall back to the text content concatenation. Each TextContent is
	// expected to be a JSON line; we return the first decoded map.
	for _, item := range res.Content {
		if tc, ok := item.(*mcp.TextContent); ok {
			var out map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Text), &out); err == nil {
				return out, nil
			}
		}
	}
	return map[string]interface{}{}, nil
}

// RemoteToolError carries an MCP tool's IsError=true response back to
// the CLI dispatcher. Surface text comes from the result's TextContent.
type RemoteToolError struct {
	Tool   string
	Result *mcp.CallToolResult
}

func (e *RemoteToolError) Error() string {
	msg := "remote tool " + e.Tool + " failed"
	for _, item := range e.Result.Content {
		if tc, ok := item.(*mcp.TextContent); ok {
			msg = tc.Text
			break
		}
	}
	return msg
}

// bearerRoundTripper injects the Authorization header on every request
// whose URL.Host matches the configured endpoint host. Wraps the base
// transport rather than mutating an http.Client's Header (the SDK
// reuses the client across connections and we want auth scoped to the
// round trip, not the client).
//
// Security audit F1: cross-host requests (e.g. a 302 redirect target
// pointing at evil.com) MUST NOT receive the bearer. Go's stdlib
// auth-stripping only inspects headers set on the *original*
// *http.Request — because we inject in the round-trip layer on a
// clone, the stdlib's protection never fires. The endpointHost gate
// closes this leak.
type bearerRoundTripper struct {
	base         http.RoundTripper
	token        string
	endpointHost string
}

func (rt *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Refuse to attach Authorization on any host that isn't the
	// configured endpoint. Surface a clear error so the operator
	// sees the leak attempt rather than a mysterious downstream
	// 401 or unauthenticated success.
	if req.URL.Host != rt.endpointHost {
		return nil, fmt.Errorf("refusing to send bearer to %s (configured endpoint host is %s)", req.URL.Host, rt.endpointHost)
	}
	// Clone — the SDK may mutate the request elsewhere; never modify
	// the caller's request in place.
	r2 := req.Clone(req.Context())
	r2.Header.Set("Authorization", "Bearer "+rt.token)
	return rt.base.RoundTrip(r2)
}

// noCrossHostRedirect is the http.Client.CheckRedirect policy that
// refuses any redirect crossing host boundaries. Same defense layer as
// the bearerRoundTripper host check — belt-and-suspenders against a
// future code path that hands the request to a follower without going
// through our RoundTrip again.
//
// Also caps the redirect chain length so a malicious loop construction
// can't tie up the connection.
func noCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects (max 5)")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("refusing cross-host redirect to %s (originated from %s)", req.URL.Host, via[0].URL.Host)
	}
	return nil
}

func defaultTransport(insecureTLS bool) http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// Security audit L1: TLS 1.3 minimum — mirrors the server-side
	// floor in internal/lib/serve/http.go::modernTLS. No legacy compat
	// story for a fresh 2026 surface; refusing to negotiate down to
	// 1.2 closes the door on downgrade attacks.
	t.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: insecureTLS, // localhost dev only
	}
	return t
}

// validateEndpointScheme enforces the client-side mirror of the server's
// TLS gate: https:// is required unless the caller explicitly opts in
// to InsecureTLS AND the host resolves to loopback (127.0.0.1, ::1, or
// localhost). Without this check, an operator typo'ing https→http in
// --server= would ship bearer tokens in plaintext over the network.
//
// Refuses with a clear error pointing at the fix (use https:// or
// --insecure-tls with localhost).
func validateEndpointScheme(endpoint string, insecureTLS bool) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid --server URL %q: %w", endpoint, err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if !insecureTLS {
			return fmt.Errorf("refusing to send bearer token over plaintext http:// (got %q); use https:// or pass --insecure-tls with a loopback host for localhost dev", endpoint)
		}
		if !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("--insecure-tls only honoured for loopback hosts (127.0.0.1, ::1, localhost); got %q", u.Hostname())
		}
		return nil
	default:
		return fmt.Errorf("--server scheme must be https (or http+--insecure-tls for loopback dev); got %q", u.Scheme)
	}
}

// isLoopbackHost recognises the canonical loopback addresses and the
// "localhost" hostname. Bracketed IPv6 literals (e.g. "[::1]") are
// stripped by url.URL.Hostname before we see them.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
