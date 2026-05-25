// Package mcp is the rufio MCP stdio adapter. It is a transport over the
// existing internal/lib substrate operations — it embeds no model/agent
// SDK and starts no daemon. Root and agent identity are resolved ONCE at
// startup (see resolve.go).
//
// # Tool pattern
//
// tools_attend.go is the reference pattern every tool follows: an In
// struct (verb flags, jsonschema tags), an Out struct, and a handler that
// REPLICATES the corresponding run* body with the pre-resolved
// root+agent injected (it never calls run*). The CLI run* and its --json
// payload are the source of truth for each Out — the fidelity tests are
// the arbiter.
//
// # Canonical Out-field nullable/omitempty rule
//
// Each Out field's Go type is chosen to make the marshalled JSON
// byte-identical to the CLI verb's --json payload:
//
//   - plain string (etc.): the CLI ALWAYS emits the key with a value.
//   - *string WITHOUT omitempty: the CLI always emits the key but as
//     JSON null when the value is absent (e.g. think/reason parent,
//     reason decision, goal by/parent). A nil pointer marshals to null,
//     a present key — matching the CLI's "always-present, null-when-
//     unset" contract.
//   - *string WITH `,omitempty`: the CLI OMITS the key entirely when the
//     value is absent (e.g. confirm/refute evidence). A nil pointer with
//     omitempty drops the key, matching the CLI's conditional emission.
//
// For read tools whose --json is JSONL (recall, goals_list) the Out
// wraps a non-nil []map[string]interface{} decoded from the SAME
// library renderer the CLI uses (recall.RenderJSON / goal.RenderJSON),
// so the per-record shape cannot drift from the CLI by construction.
//
// listen is the deliberate exception to that map-decode pattern: its Out
// returns the typed stream.Event slice directly because stream.Event IS
// the canonical wire schema (reused unchanged by a future push transport),
// not a CLI render target — a justified divergence, not drift.
package mcp
