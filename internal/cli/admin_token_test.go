package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func setupAdminProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{"init"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("init failed: %s", res.Stderr)
	}
	return root
}

func TestAdminTokenMint_PrintsPlaintextOnce(t *testing.T) {
	root := setupAdminProject(t)
	res := testutil.RunCLI(t, []string{"admin", "token", "mint", "--agent=alice"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("mint failed: code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "rufio_") {
		t.Errorf("plaintext token missing from stdout: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "tok-") {
		t.Errorf("token id missing from stdout: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "token_id=") || !strings.Contains(res.Stdout, "token=") {
		t.Errorf("output should be key=value format; got %s", res.Stdout)
	}
	// stderr should carry the warning about the one-time print, but
	// stdout must remain machine-parseable.
	if !strings.Contains(res.Stderr, "ONCE") {
		t.Errorf("stderr should warn about one-time plaintext; got %s", res.Stderr)
	}
}

func TestAdminTokenMint_RequiresAgent(t *testing.T) {
	root := setupAdminProject(t)
	res := testutil.RunCLI(t, []string{"admin", "token", "mint"}, root, nil)
	if res.Code != 2 {
		t.Errorf("expected exit 2 without --agent, got %d (stderr=%s)", res.Code, res.Stderr)
	}
}

func TestAdminTokenMint_RejectsInvalidAgent(t *testing.T) {
	root := setupAdminProject(t)
	res := testutil.RunCLI(t, []string{"admin", "token", "mint", "--agent=BadName!"}, root, nil)
	if res.Code == 0 {
		t.Errorf("expected non-zero exit for invalid agent id")
	}
}

func TestAdminTokenMint_JSON(t *testing.T) {
	root := setupAdminProject(t)
	res := testutil.RunCLI(t, []string{"admin", "token", "mint", "--agent=bob", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("mint --json failed: %s", res.Stderr)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &obj); err != nil {
		t.Fatalf("decode JSON: %v (stdout=%s)", err, res.Stdout)
	}
	if obj["agent"] != "bob" {
		t.Errorf("expected agent=bob, got %q", obj["agent"])
	}
	if !strings.HasPrefix(obj["token"], "rufio_") {
		t.Errorf("token should be rufio_-prefixed, got %q", obj["token"])
	}
	if !strings.HasPrefix(obj["token_id"], "tok-") {
		t.Errorf("token_id should be tok--prefixed, got %q", obj["token_id"])
	}
}

func TestAdminTokenList_EmptyInitially(t *testing.T) {
	root := setupAdminProject(t)
	res := testutil.RunCLI(t, []string{"admin", "token", "list"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("list failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, "no tokens") {
		t.Errorf("expected 'no tokens' message; got %q", res.Stdout)
	}
}

func TestAdminTokenList_NeverShowsHashes(t *testing.T) {
	root := setupAdminProject(t)
	_ = testutil.RunCLI(t, []string{"admin", "token", "mint", "--agent=alice"}, root, nil)
	res := testutil.RunCLI(t, []string{"admin", "token", "list"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("list failed: %s", res.Stderr)
	}
	if strings.Contains(res.Stdout, "hash:") {
		t.Errorf("list output should NEVER contain hash: ; got %s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "hash=") {
		t.Errorf("list output should NEVER contain hash= ; got %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "alice") {
		t.Errorf("list should mention agent alice; got %s", res.Stdout)
	}
}

func TestAdminTokenList_JSON_NeverShowsHashes(t *testing.T) {
	root := setupAdminProject(t)
	_ = testutil.RunCLI(t, []string{"admin", "token", "mint", "--agent=alice"}, root, nil)
	res := testutil.RunCLI(t, []string{"admin", "token", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("list --json failed: %s", res.Stderr)
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]string
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("decode JSON line %q: %v", line, err)
		}
		if _, ok := obj["hash"]; ok {
			t.Errorf("JSON list must not include hash field; got %s", line)
		}
	}
}

func TestAdminTokenRevoke_HidesFromActive(t *testing.T) {
	root := setupAdminProject(t)
	mintRes := testutil.RunCLI(t, []string{"admin", "token", "mint", "--agent=alice", "--json"}, root, nil)
	if mintRes.Code != 0 {
		t.Fatalf("mint failed: %s", mintRes.Stderr)
	}
	var minted map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(mintRes.Stdout)), &minted); err != nil {
		t.Fatalf("decode mint JSON: %v", err)
	}
	tokenID := minted["token_id"]
	if tokenID == "" {
		t.Fatal("mint did not return token_id")
	}

	revokeRes := testutil.RunCLI(t, []string{"admin", "token", "revoke", tokenID}, root, nil)
	if revokeRes.Code != 0 {
		t.Fatalf("revoke failed: %s", revokeRes.Stderr)
	}

	listRes := testutil.RunCLI(t, []string{"admin", "token", "list"}, root, nil)
	if !strings.Contains(listRes.Stdout, "revoked") {
		t.Errorf("list should show revoked state; got %s", listRes.Stdout)
	}
}

func TestAdminTokenRevoke_UnknownID(t *testing.T) {
	root := setupAdminProject(t)
	res := testutil.RunCLI(t, []string{"admin", "token", "revoke", "tok-nonexistent"}, root, nil)
	if res.Code == 0 {
		t.Errorf("expected non-zero exit for unknown token id")
	}
}
