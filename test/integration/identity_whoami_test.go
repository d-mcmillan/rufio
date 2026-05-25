package integration_test

import (
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// #112 papercut: `rufio identity` with NO args used to error out
// `missing required flag --as=<agent-id>`, which is hostile to the agent
// expecting "show me the current identity". The fix: no-args path
// degrades to whoami's behaviour (resolve env > file; print id or
// surface NoIdentityError with the "run `rufio identity --as=<id>`" hint).
// The --as <id> write path is UNCHANGED — both behaviours coexist on the
// same verb.

func TestIdentity_NoArgs_BehavesLikeWhoami_FromEnv(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "claude-code",
	})
	if r.Code != 0 {
		t.Fatalf("identity (no args, env set): exit %d, stderr: %s", r.Code, r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != "claude-code" {
		t.Errorf("identity stdout: got %q, want %q (whoami-style)", got, "claude-code")
	}
}

func TestIdentity_NoArgs_BehavesLikeWhoami_FromFile(t *testing.T) {
	workdir := initProject(t)
	// First persist an identity via the --as write path (must still work).
	if r := testutil.RunCLI(t, []string{"identity", "--as=cursor"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	}); r.Code != 0 {
		t.Fatalf("identity --as=cursor: %s", r.Stderr)
	}
	// Then read it back with the no-args path — env cleared so we resolve
	// from the file we just wrote.
	r := testutil.RunCLI(t, []string{"identity"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 0 {
		t.Fatalf("identity (no args, file set): exit %d, stderr: %s", r.Code, r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != "cursor" {
		t.Errorf("identity stdout: got %q, want %q (whoami-style, from file)", got, "cursor")
	}
}

// When neither env nor file set, the no-args path must SURFACE the
// helpful NoIdentityError (exit 1, "run `rufio identity --as=<id>`" hint)
// — same as whoami. The OLD behaviour was a `--as` usage error (exit 2)
// which buried the actual next step. Helping is the fix.
func TestIdentity_NoArgs_NoIdentitySet_Hints(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 1 {
		t.Errorf("identity (no args, nothing set): exit %d, want 1 (NoIdentityError)\nstderr: %s", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "no identity set") {
		t.Errorf("identity no-args stderr should hint about no identity; got: %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "rufio identity --as=") {
		t.Errorf("identity no-args stderr should hint at the --as remedy; got: %q", r.Stderr)
	}
	// Critical: must NOT regress to the old "missing required flag --as"
	// usage error — that is the papercut #112 fixes.
	if strings.Contains(r.Stderr, "missing required flag --as") {
		t.Errorf("identity no-args regressed to the #112 papercut (missing required flag); stderr: %q", r.Stderr)
	}
}

// The --as <id> WRITE path STAYS UNCHANGED (still validates, still
// persists). Re-pinned here so the no-args change cannot silently break
// the write contract.
func TestIdentity_AsFlag_StillWrites(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity", "--as=writer"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 0 {
		t.Fatalf("identity --as=writer: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `identity set: writer`)
	// Confirm the file is on disk.
	r2 := testutil.RunCLI(t, []string{"identity"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r2.Code != 0 {
		t.Fatalf("identity (read-back): exit %d, stderr: %s", r2.Code, r2.Stderr)
	}
	if got := strings.TrimSpace(r2.Stdout); got != "writer" {
		t.Errorf("identity (read-back) stdout: got %q, want %q", got, "writer")
	}
}

// --json on the no-args path must emit the SAME shape `whoami --json`
// emits (so agents consuming structured output get a uniform contract
// regardless of which verb they reached for).
func TestIdentity_NoArgs_JSON(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity", "--json"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "claude-code",
	})
	if r.Code != 0 {
		t.Fatalf("identity --json (no args, env set): exit %d, stderr: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `"_type":"whoami"`)
	mustMatch(t, r.Stdout, `"_version":"1"`)
	mustMatch(t, r.Stdout, `"agent":"claude-code"`)
	mustMatch(t, r.Stdout, `"source":"env"`)
}
