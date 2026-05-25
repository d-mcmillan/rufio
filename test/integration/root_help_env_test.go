package integration_test

import (
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// #112: RUFIO_AGENT_ID is the only per-invocation identity override for
// the cognition verbs (attend/observe/think/say/summon/goal/etc.). Cold
// agents found it via `strings` on the binary because it appears nowhere
// in --help. The fix: document it in `rufio --help` (footer
// "Environment variables:" section) AND inline in every verb's --help
// that consumes identity.

// TestRootHelp_DocumentsRUFIO_AGENT_ID pins the root --help mention. This
// is the "first-line discoverability" path: an agent typing `rufio
// --help` should learn about the override without reading source.
func TestRootHelp_DocumentsRUFIO_AGENT_ID(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"--help"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("rufio --help: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	help := r.Stdout + r.Stderr
	if !strings.Contains(help, "RUFIO_AGENT_ID") {
		t.Errorf("rufio --help missing RUFIO_AGENT_ID:\n%s", help)
	}
	// The footer section name itself — gives an agent a target to grep.
	if !strings.Contains(help, "Environment variables:") {
		t.Errorf("rufio --help missing \"Environment variables:\" section:\n%s", help)
	}
	// A short description of what it does (so the mention is not bare).
	if !strings.Contains(help, "agent identity") {
		t.Errorf("rufio --help RUFIO_AGENT_ID line should describe it (\"agent identity\"):\n%s", help)
	}
}

// TestVerbHelp_DocumentsRUFIO_AGENT_ID pins the per-verb mention. The
// brief enumerates attend/observe/summon/say/think/goal as the
// identity-consuming verbs whose --help must surface the env var. (The
// underlying identity-consumer set is wider, but these are the verbs an
// agent most commonly reaches for in the cold-start flow.)
func TestVerbHelp_DocumentsRUFIO_AGENT_ID(t *testing.T) {
	for _, verb := range []string{"attend", "observe", "summon", "say", "think", "goal", "whoami"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			workdir := mkProject(t)
			r := testutil.RunCLI(t, []string{verb, "--help"}, workdir, nil)
			if r.Code != 0 {
				t.Fatalf("%s --help: exit %d, stderr: %s", verb, r.Code, r.Stderr)
			}
			help := r.Stdout + r.Stderr
			if !strings.Contains(help, "RUFIO_AGENT_ID") {
				t.Errorf("%s --help missing RUFIO_AGENT_ID mention:\n%s", verb, help)
			}
		})
	}
}
