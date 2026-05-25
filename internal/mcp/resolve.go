// (Package doc lives in doc.go.)
package mcp

import (
	"os"

	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
)

// Resolved is the once-at-startup substrate context shared by every tool.
type Resolved struct {
	Root  string // absolute substrate root (contains rufio.gdl)
	Agent string // validated agent id
}

// resolve mirrors the CLI's precedence: root = FindProjectRoot(rootFlag or cwd);
// agent = agentFlag (validated) else identity.Resolve(root) (env RUFIO_AGENT_ID
// then .rufio/identity.local.gdl). Errors are returned, never os.Exit'd.
func resolve(rootFlag, agentFlag string) (Resolved, error) {
	start := rootFlag
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Resolved{}, err
		}
		start = cwd
	}
	root, err := paths.FindProjectRoot(start)
	if err != nil {
		return Resolved{}, err
	}
	if agentFlag != "" {
		if err := identity.Validate(agentFlag); err != nil {
			return Resolved{}, err
		}
		return Resolved{Root: root, Agent: agentFlag}, nil
	}
	id, _, err := identity.Resolve(root)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Root: root, Agent: id}, nil
}
