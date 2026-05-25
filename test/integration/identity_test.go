package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestIdentity_SetsFile(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity", "--as=claude-code"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 0 {
		t.Fatalf("exit: got %d (stderr: %s)", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `identity set: claude-code`)

	// File contents should be a single @identity record.
	bs, err := os.ReadFile(filepath.Join(workdir, ".rufio", "identity.local.gdl"))
	if err != nil {
		t.Fatalf("identity file missing: %v", err)
	}
	mustMatch(t, string(bs), `^@identity\|`)
	mustMatch(t, string(bs), `agent:claude-code`)
	mustMatch(t, string(bs), `set-at:\d{4}-\d{2}-\d{2}T`)
}

func TestIdentity_OverwritesPriorValue(t *testing.T) {
	workdir := initProject(t)
	if r := testutil.RunCLI(t, []string{"identity", "--as=first"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	}); r.Code != 0 {
		t.Fatal(r.Stderr)
	}
	if r := testutil.RunCLI(t, []string{"identity", "--as=second"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	}); r.Code != 0 {
		t.Fatal(r.Stderr)
	}
	bs, _ := os.ReadFile(filepath.Join(workdir, ".rufio", "identity.local.gdl"))
	if !strings.Contains(string(bs), "agent:second") {
		t.Errorf("expected second; got %q", bs)
	}
	if strings.Contains(string(bs), "agent:first") {
		t.Errorf("did not expect first; got %q", bs)
	}
}

func TestIdentity_RejectsInvalid(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity", "--as=BAD ID"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 2 {
		t.Errorf("exit: got %d, want 2", r.Code)
	}
	mustMatch(t, r.Stderr, `invalid agent id`)
}

// SUPERSEDED BY #112: previously `rufio identity` with no args errored
// `missing required flag --as=<agent-id>` (exit 2). That was the papercut
// — agents expected "show me the identity" and got a usage wall, while
// `rufio whoami` (which already did the right thing) was undiscoverable.
// The new contract: no-args → behave as `rufio whoami` (resolve env >
// file; surface NoIdentityError with the helpful "run `rufio identity
// --as=<id>`" hint when neither is set). The new contract is asserted
// positively in identity_whoami_test.go; this test now pins that the
// OLD usage-error behaviour is GONE so a future regression to it fails
// loudly.
func TestIdentity_NoArgs_NotAUsageError(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	// No env, no file, no project state → NoIdentityError (exit 1) is the
	// helpful path, NOT a UsageError (exit 2) about --as. The exact
	// stderr+stdout shape is pinned by identity_whoami_test.go; here we
	// just rule out a regression to the old exit-2 / "missing required
	// flag --as" behaviour.
	if r.Code == 2 {
		t.Errorf("identity no-args regressed to UsageError (exit 2); the #112 fix routes through whoami:\nstderr: %s", r.Stderr)
	}
	if strings.Contains(r.Stderr, "missing required flag --as") {
		t.Errorf("identity no-args regressed to the #112 papercut; stderr: %q", r.Stderr)
	}
}

func TestIdentity_WarnsOnEnvOverride(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity", "--as=stored-value"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "env-value",
	})
	if r.Code != 0 {
		t.Fatalf("exit: got %d (stderr: %s)", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stderr, `warning: RUFIO_AGENT_ID`)
	mustMatch(t, r.Stderr, `env-value`)
	mustMatch(t, r.Stderr, `overrides`)
}

func TestIdentity_NoWarnWhenEnvMatchesFile(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity", "--as=match"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "match",
	})
	if r.Code != 0 {
		t.Fatal(r.Stderr)
	}
	mustNotMatch(t, r.Stderr, `warning`)
}

func TestIdentity_JSON(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"identity", "--as=cursor", "--json"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 0 {
		t.Fatal(r.Stderr)
	}
	mustMatch(t, r.Stdout, `"_type":"identity-set"`)
	mustMatch(t, r.Stdout, `"_version":"1"`)
	mustMatch(t, r.Stdout, `"agent":"cursor"`)
}

func TestIdentity_NotInProject(t *testing.T) {
	dir := t.TempDir()
	r := testutil.RunCLI(t, []string{"identity", "--as=foo"}, dir, nil)
	if r.Code != 1 {
		t.Errorf("exit: got %d, want 1", r.Code)
	}
	mustMatch(t, r.Stderr, `not inside a Rufio project`)
}
