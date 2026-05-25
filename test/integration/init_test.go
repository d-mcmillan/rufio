package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func mkProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	return real
}

// initProject is mkProject(t) + `rufio init` so each test starts with a
// real on-disk Rufio project. mkProject only allocates a tempdir; without
// init there is no rufio.gdl and identity-dependent commands short-circuit
// on NotInProjectError before consulting identity. Tests that specifically
// want the no-project path (e.g., TestWhoami_NotInProject) use bare
// t.TempDir() instead.
func initProject(t *testing.T) string {
	t.Helper()
	workdir := mkProject(t)
	if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
		t.Fatalf("init: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	return workdir
}

func TestRufioInit_ScaffoldsAtCwd(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit code %d, stderr: %s", r.Code, r.Stderr)
	}

	for _, sub := range []string{
		"rufio.gdl", "given", "learned", "live", "live/outbox",
		"live/inbox", "live/attention", ".rufio/history", ".rufio/refs",
	} {
		if _, err := os.Stat(filepath.Join(workdir, sub)); err != nil {
			t.Errorf("missing %q: %v", sub, err)
		}
	}

	cfgBytes, err := os.ReadFile(filepath.Join(workdir, "rufio.gdl"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg := string(cfgBytes)
	mustMatch(t, cfg, `(?m)^@config\|`)
	mustMatch(t, cfg, `name:`)
	mustMatch(t, cfg, `version:1`)
	mustMatch(t, cfg, `created:\d{4}-\d{2}-\d{2}T`)
}

func TestRufioInit_ScaffoldsLiveRetracted(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "retracted")); err != nil {
		t.Errorf("init didn't scaffold live/retracted: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveConfirms(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "confirms")); err != nil {
		t.Errorf("init didn't scaffold live/confirms: %v", err)
	}
}

func TestRufioInit_ScaffoldsLivePromoted(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "promoted")); err != nil {
		t.Errorf("init didn't scaffold live/promoted: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveExpired(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "expired")); err != nil {
		t.Errorf("init didn't scaffold live/expired: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveSummonsPending(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "pending")); err != nil {
		t.Errorf("init didn't scaffold live/summons/pending: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveSummonsAccepted(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "accepted")); err != nil {
		t.Errorf("init didn't scaffold live/summons/accepted: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveSummonsDeclined(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "declined")); err != nil {
		t.Errorf("init didn't scaffold live/summons/declined: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveSummonsExpired(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "expired")); err != nil {
		t.Errorf("init didn't scaffold live/summons/expired: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveChannelsActive(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "channels", "active")); err != nil {
		t.Errorf("init didn't scaffold live/channels/active: %v", err)
	}
}

func TestRufioInit_ScaffoldsLiveChannelsClosed(t *testing.T) {
	root := initProject(t)
	if _, err := os.Stat(filepath.Join(root, "live", "channels", "closed")); err != nil {
		t.Errorf("init didn't scaffold live/channels/closed: %v", err)
	}
}

func TestRufioInit_AcceptsNameArg(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init", "demo-project"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit code %d, stderr: %s", r.Code, r.Stderr)
	}
	cfg, err := os.ReadFile(filepath.Join(workdir, "rufio.gdl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	mustMatch(t, string(cfg), `name:demo-project`)
}

// TestRufioInit_ReinitRefreshesAndPreservesConfig pins the #128 contract.
//
// SUPERSEDED CONTRACT (deliberately reversed by #128): this test
// previously asserted re-init exits NON-ZERO with `already initialised`
// on stderr — the hard-fail that #128 fixes (it blocked refreshing the
// primer / folding a harness file added after the first init). The
// "expected non-zero exit on re-init" + `mustMatch(stderr, "already
// initialised")` assertions encoded that superseded contract and are
// replaced with the new contract: re-init SUCCEEDS and is a primer
// refresh. The STILL-VALID invariant — re-init must NOT rewrite the
// project config (rufio.gdl is left byte-identical, including the
// original `--name`) — is preserved verbatim below.
func TestRufioInit_ReinitRefreshesAndPreservesConfig(t *testing.T) {
	workdir := mkProject(t)
	first := testutil.RunCLI(t, []string{"init", "first"}, workdir, nil)
	if first.Code != 0 {
		t.Fatalf("first init failed: %s", first.Stderr)
	}
	original, _ := os.ReadFile(filepath.Join(workdir, "rufio.gdl"))

	second := testutil.RunCLI(t, []string{"init", "second"}, workdir, nil)
	if second.Code != 0 {
		t.Fatalf("expected re-init to SUCCEED (primer refresh), got exit %d, stderr: %s", second.Code, second.Stderr)
	}

	// Still-valid invariant: re-scaffolding is skipped, so rufio.gdl
	// (and its original name:first) is NEVER rewritten by re-init.
	after, _ := os.ReadFile(filepath.Join(workdir, "rufio.gdl"))
	if string(after) != string(original) {
		t.Errorf("re-init must NOT rewrite rufio.gdl\n--- before ---\n%s\n--- after ---\n%s", original, after)
	}
	mustMatch(t, string(after), `name:first`)
}

// TestRufioInit_FreshVsRefresh_DistinctStdout pins the cold-reader signal:
// a fresh `rufio init` and a re-init MUST produce visibly different stdout
// so an operator running `rufio init` twice can tell the second was a
// refresh, not a silent re-scaffold. The init code already implements two
// paths (see internal/cli/init.go ~L126 vs ~L173); this test guards them
// from drifting back to a single message.
func TestRufioInit_FreshVsRefresh_DistinctStdout(t *testing.T) {
	workdir := mkProject(t)

	first := testutil.RunCLI(t, []string{"init"}, workdir, nil)
	if first.Code != 0 {
		t.Fatalf("first init: exit %d, stderr: %s", first.Code, first.Stderr)
	}
	mustMatch(t, first.Stdout, `initialised rufio project at`)
	mustNotMatch(t, first.Stdout, `refreshed primer at`)

	second := testutil.RunCLI(t, []string{"init"}, workdir, nil)
	if second.Code != 0 {
		t.Fatalf("re-init: exit %d, stderr: %s", second.Code, second.Stderr)
	}
	mustMatch(t, second.Stdout, `refreshed primer at`)
	mustNotMatch(t, second.Stdout, `initialised rufio project at`)
}

func TestRufioInit_JSON_EmitsSingleObject(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init", "demo", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit code %d, stderr: %s", r.Code, r.Stderr)
	}
	lines := nonEmptyLines(r.Stdout)
	if len(lines) < 1 {
		t.Fatalf("expected at least one stdout line, got 0; stdout: %q", r.Stdout)
	}
	var obj map[string]string
	last := lines[len(lines)-1]
	if err := json.Unmarshal([]byte(last), &obj); err != nil {
		t.Fatalf("json unmarshal: %v (line: %q)", err, last)
	}
	if obj["name"] != "demo" {
		t.Errorf("name: got %q, want %q", obj["name"], "demo")
	}
	if obj["root"] != workdir {
		t.Errorf("root: got %q, want %q", obj["root"], workdir)
	}
}

func TestRufioInit_QuietJSON_EmitsExactlyJSONLineNoStderr(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init", "demo", "--quiet", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit code %d, stderr: %s", r.Code, r.Stderr)
	}
	lines := nonEmptyLines(r.Stdout)
	if len(lines) != 1 {
		t.Errorf("got %d stdout lines, want exactly 1; stdout: %q", len(lines), r.Stdout)
	}
	if r.Stderr != "" {
		t.Errorf("stderr should be empty; got %q", r.Stderr)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if obj["name"] != "demo" {
		t.Errorf("name: got %q, want %q", obj["name"], "demo")
	}
}

func TestRufioInit_RejectsUnknownFlagsExit2(t *testing.T) {
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init", "--bogus-flag"}, workdir, nil)
	// Unknown-flag errors from pflag don't implement RufioError, so the
	// dispatcher falls through to the catch-all in cmd/rufio/main.go which
	// exits 2 (Unix convention for usage errors). The error message has
	// "rufio: " prefix (root-level) rather than "rufio init: " (command-
	// level) — the followup GO-P3-3 tracks tightening that to a typed
	// UsageError so unknown-flag-on-init produces the per-command prefix.
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2; stderr: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stderr, `(?i)unknown`)
	mustNotMatch(t, r.Stderr, `rufio init: rufio init:`)
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func mustMatch(t *testing.T, s string, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("expected match for %q in:\n%s", pattern, s)
	}
}

func mustNotMatch(t *testing.T, s string, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("expected NO match for %q in:\n%s", pattern, s)
	}
}
