package integration_test

import (
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestWhoami_NoIdentitySet(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"whoami"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 1 {
		t.Errorf("exit: got %d, want 1", r.Code)
	}
	mustMatch(t, r.Stderr, `rufio whoami:`)
	mustMatch(t, r.Stderr, `no identity set`)
	mustMatch(t, r.Stderr, `rufio identity --as=`)
}

func TestWhoami_FromEnv(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"whoami"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "claude-code",
	})
	if r.Code != 0 {
		t.Fatalf("exit: got %d (stderr: %s)", r.Code, r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != "claude-code" {
		t.Errorf("stdout: got %q, want claude-code", got)
	}
}

func TestWhoami_FromFile(t *testing.T) {
	workdir := initProject(t)
	// Persist identity via `identity --as=` then resolve via whoami; env
	// is explicitly cleared so the file path is exercised.
	r := testutil.RunCLI(t, []string{"identity", "--as=cursor"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 0 {
		t.Fatalf("identity --as: %s", r.Stderr)
	}
	r = testutil.RunCLI(t, []string{"whoami"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	})
	if r.Code != 0 {
		t.Fatalf("whoami: %s", r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != "cursor" {
		t.Errorf("stdout: got %q, want cursor", got)
	}
}

func TestWhoami_EnvWinsOverFile(t *testing.T) {
	workdir := initProject(t)
	// File says "from-file"; env says "from-env" — env must win.
	if r := testutil.RunCLI(t, []string{"identity", "--as=from-file"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "",
	}); r.Code != 0 {
		t.Fatal(r.Stderr)
	}
	r := testutil.RunCLI(t, []string{"whoami"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "from-env",
	})
	if r.Code != 0 {
		t.Fatal(r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != "from-env" {
		t.Errorf("stdout: got %q, want from-env (env should win)", got)
	}
}

func TestWhoami_JSON(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"whoami", "--json"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "claude-code",
	})
	if r.Code != 0 {
		t.Fatal(r.Stderr)
	}
	mustMatch(t, r.Stdout, `"_type":"whoami"`)
	mustMatch(t, r.Stdout, `"_version":"1"`)
	mustMatch(t, r.Stdout, `"agent":"claude-code"`)
	mustMatch(t, r.Stdout, `"source":"env"`)
}

func TestWhoami_InvalidEnvIdentity(t *testing.T) {
	workdir := initProject(t)
	r := testutil.RunCLI(t, []string{"whoami"}, workdir, map[string]string{
		"RUFIO_AGENT_ID": "BAD ID",
	})
	if r.Code != 2 {
		t.Errorf("exit: got %d, want 2", r.Code)
	}
	mustMatch(t, r.Stderr, `invalid agent id`)
}

func TestWhoami_NotInProject(t *testing.T) {
	// Bare tempdir — no `rufio init` — so FindProjectRoot returns
	// NotInProjectError before identity is consulted.
	dir := t.TempDir()
	r := testutil.RunCLI(t, []string{"whoami"}, dir, nil)
	if r.Code != 1 {
		t.Errorf("exit: got %d, want 1", r.Code)
	}
	mustMatch(t, r.Stderr, `not inside a Rufio project`)
}
