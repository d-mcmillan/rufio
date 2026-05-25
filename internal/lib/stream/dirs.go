package stream

import "path/filepath"

// ListenDirs returns the canonical set of project-root-relative POSIX
// directory paths that constitute "the agent's listen surface" — the
// subset of the substrate that `rufio listen` and the MCP `listen`
// tool walk. Single source of truth: BOTH the CLI command and the
// MCP tool MUST call this function, never inline the list. The
// v1.0.3 MCP-listen-symmetry regression (PR #188 gate failure) was
// caused by the CLI's inline list gaining `live/promoted/` while the
// MCP tool's inline list did not — moving the list here makes a
// future addition automatically apply to both surfaces.
//
// The agent param is what scopes the per-agent inbox; project-wide
// subtrees (channels, summons, confirms, retracted, reasoning,
// promoted) are agent-independent and walked verbatim. Privacy
// filtering is applied downstream by Match — this function only
// answers "what dirs to walk", not "what records to emit".
//
// Missing dirs (fresh project; live/promoted/ before any promotion)
// are skipped cleanly by the walkers (EmitCatchUp, WatchAndEmit,
// Poll, WatchAndEmitFrom) — see the os.ErrNotExist guards in each.
func ListenDirs(agent string) []string {
	return []string{
		filepath.Join("live", "inbox", agent),
		"live/outbox",
		"live/channels/active",
		"live/summons/pending",
		"live/confirms",
		"live/retracted",
		"live/reasoning",
		// v1.0.3: auto-promote events. @auto-promote records carry
		// the enriched quorum-dynamics payload so listeners see the
		// full propagation without a separate read.
		"live/promoted",
	}
}
