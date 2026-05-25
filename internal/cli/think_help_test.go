// Package cli — tests pinning `rufio think --help` documents the
// required --type flag and its enum alternatives (#143).
//
// #143 surfaced in cold-start round 7: agents ran `rufio think
// --scope=fleet --subject=test:r7 --content="hi"`, hit a runtime
// "invalid --type" error, and could not discover the missing flag
// from `--help`. This test pins the fix so the regression can't
// recur silently — the flag must appear in --help AND the rendered
// help text must teach the enum.
package cli

import (
	"strings"
	"testing"
)

// TestThinkHelp_DocumentsTypeFlag pins #143. The rendered --help text
// must contain "--type" and every accepted thought-type alternative
// so a cold agent can self-correct from the help alone.
func TestThinkHelp_DocumentsTypeFlag(t *testing.T) {
	help := renderHelp(t, NewThinkCmd())
	if !strings.Contains(help, "--type") {
		t.Errorf("think --help missing --type flag; help text:\n%s", help)
	}
	for _, kind := range []string{"hypothesis", "decision", "focus", "question", "observation"} {
		if !strings.Contains(help, kind) {
			t.Errorf("think --help missing type alternative %q", kind)
		}
	}
}
