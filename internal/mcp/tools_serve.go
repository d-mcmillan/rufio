package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// serveStatusIn is currently empty — the tool is parameter-less. Reserved
// for future additions (e.g. include detailed connection list).
type serveStatusIn struct{}

// serveStatusOut is the typed wire shape. Fields:
//
//   - root          absolute substrate path
//   - has_tokens    true iff at least one minted token exists
//   - token_count   number of active (non-revoked) tokens
//   - tls_hint      a soft hint about TLS setup ("self-signed" / "ca-signed" / "unknown")
//
// We deliberately do NOT expose runtime fields (port, listen address,
// uptime) — those leak operational details to remote agents. Admin
// agents that need them should ask the operator.
type serveStatusOut struct {
	Type       string `json:"_type"`
	Version    int    `json:"_version"`
	Root       string `json:"root"`
	HasTokens  bool   `json:"has_tokens"`
	TokenCount int    `json:"token_count"`
	TLSHint    string `json:"tls_hint"`
}

// registerServeStatus wires the `serve_status` MCP tool. Read-only,
// no side effects, returns a small JSON object describing the hosted
// server's health. Token mint/revoke are intentionally NOT exposed via
// MCP — they're privileged operations on the server's filesystem; only
// a local operator should invoke them via the CLI.
func registerServeStatus(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "serve_status",
		Description: "Inspect the rufio hosted server's health (read-only). Returns root, token count, and TLS hint.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ serveStatusIn) (*mcp.CallToolResult, serveStatusOut, error) {
		out := serveStatusOut{
			Type: "serve-status", Version: 1,
			Root: r.Root,
		}
		toks, err := admin.ListTokens(r.Root)
		if err != nil {
			return nil, out, toolErr(err)
		}
		out.TokenCount = 0
		for _, t := range toks {
			if !t.Revoked {
				out.TokenCount++
			}
		}
		out.HasTokens = out.TokenCount > 0
		out.TLSHint = inferTLSHint(r.Root)
		return nil, out, nil
	})
}

// inferTLSHint reports the TLS posture. v1.0.4 has no canonical
// location for the cert (operators pass it as a CLI flag), so this
// always returns "unknown". Reserved for future enrichment when the
// hosted-server config gains a config file.
func inferTLSHint(root string) string {
	_ = root
	return "unknown"
}
