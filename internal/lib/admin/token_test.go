package admin

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// initProject scaffolds the minimum substrate shape Token operations need.
// We don't depend on the full init machinery — only the marker file +
// .admin subdir so the lock path can be created safely.
func initProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte("@config|name:test|version:1\n"), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	return root
}

func readTokensRaw(t *testing.T, root string) string {
	t.Helper()
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", ".admin", "tokens.gdl"))
	if err != nil {
		t.Fatalf("read tokens.gdl: %v", err)
	}
	return string(bs)
}

func TestMintToken_ReturnsPlaintextOnce(t *testing.T) {
	root := initProject(t)
	plaintext, tok, err := MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if !strings.HasPrefix(plaintext, "rufio_") {
		t.Errorf("plaintext should start with rufio_; got %q", plaintext)
	}
	if len(plaintext) < 40 {
		t.Errorf("plaintext too short (%d chars): %q", len(plaintext), plaintext)
	}
	if !strings.HasPrefix(tok.ID, "tok-") {
		t.Errorf("token id should start with tok-; got %q", tok.ID)
	}
	if tok.Agent != "alice" {
		t.Errorf("agent mismatch: got %q want alice", tok.Agent)
	}
	if tok.Revoked {
		t.Error("fresh token should not be revoked")
	}

	onDisk := readTokensRaw(t, root)
	if strings.Contains(onDisk, plaintext) {
		t.Error("plaintext leaked to tokens.gdl on disk")
	}
	if !strings.Contains(onDisk, "hash:") {
		t.Error("tokens.gdl should contain hash: field")
	}
	if !strings.Contains(onDisk, "agent:alice") {
		t.Error("tokens.gdl should contain agent:alice")
	}
}

func TestMintToken_TokenIDIsDeterministicFromPlaintext(t *testing.T) {
	root := initProject(t)
	plaintext, tok, err := MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if got := TokenIDFromPlaintext(plaintext); got != tok.ID {
		t.Errorf("TokenIDFromPlaintext mismatch: got %q want %q", got, tok.ID)
	}
}

func TestMintToken_FilePermissionsRestrictive(t *testing.T) {
	root := initProject(t)
	_, _, err := MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	st, err := os.Stat(filepath.Join(root, ".rufio", ".admin", "tokens.gdl"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Errorf("tokens.gdl should be 0600; got %o", st.Mode().Perm())
	}
}

func TestResolveToken_VerifiesHash(t *testing.T) {
	root := initProject(t)
	plaintext, _, err := MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	agent, err := ResolveToken(root, plaintext)
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if agent != "alice" {
		t.Errorf("expected alice, got %q", agent)
	}
}

func TestResolveToken_RejectsUnknown(t *testing.T) {
	root := initProject(t)
	_, _, err := MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	_, err = ResolveToken(root, "rufio_unknown_token_value_does_not_exist")
	if err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestResolveToken_RejectsMalformedPrefix(t *testing.T) {
	root := initProject(t)
	_, err := ResolveToken(root, "not_a_rufio_token")
	if err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid for missing rufio_ prefix, got %v", err)
	}
}

func TestResolveToken_RejectsRevoked(t *testing.T) {
	root := initProject(t)
	plaintext, tok, err := MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := RevokeToken(root, tok.ID); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	_, err = ResolveToken(root, plaintext)
	if err != ErrTokenInvalid {
		t.Errorf("revoked token should fail with ErrTokenInvalid; got %v", err)
	}
}

func TestResolveToken_NoTokensFile_ReturnsInvalid(t *testing.T) {
	root := initProject(t)
	// No mint — tokens file does not exist.
	_, err := ResolveToken(root, "rufio_anything")
	if err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid on missing tokens file, got %v", err)
	}
}

func TestRevokeToken_Idempotent(t *testing.T) {
	root := initProject(t)
	_, tok, err := MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := RevokeToken(root, tok.ID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := RevokeToken(root, tok.ID); err != nil {
		t.Fatalf("second revoke should be idempotent: %v", err)
	}
}

func TestRevokeToken_UnknownID(t *testing.T) {
	root := initProject(t)
	err := RevokeToken(root, "tok-bogus")
	if err != ErrTokenInvalid {
		t.Errorf("revoke unknown id should fail with ErrTokenInvalid, got %v", err)
	}
}

func TestListTokens_OmitsHash(t *testing.T) {
	root := initProject(t)
	_, _, _ = MintToken(root, "alice")
	_, _, _ = MintToken(root, "bob")
	toks, err := ListTokens(root)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(toks) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(toks))
	}
	// Token struct has no Hash field — compile-time guarantee that
	// hashes never leak via List. The on-disk file does contain hashes,
	// which is correct (storage); the public projection must not.
}

func TestListTokens_ShowsRevokedStatus(t *testing.T) {
	root := initProject(t)
	_, tok, _ := MintToken(root, "alice")
	_ = RevokeToken(root, tok.ID)
	toks, err := ListTokens(root)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(toks) != 1 {
		t.Fatalf("expected 1 token, got %d", len(toks))
	}
	if !toks[0].Revoked {
		t.Error("revoked token should report Revoked=true in ListTokens")
	}
}

func TestMintToken_RejectsInvalidAgent(t *testing.T) {
	root := initProject(t)
	_, _, err := MintToken(root, "BadAgent!")
	if err == nil {
		t.Error("expected mint with invalid agent id to fail")
	}
}

func TestMintToken_MultipleAccumulate(t *testing.T) {
	root := initProject(t)
	for _, a := range []string{"alice", "bob", "carol"} {
		if _, _, err := MintToken(root, a); err != nil {
			t.Fatalf("MintToken(%s): %v", a, err)
		}
	}
	toks, err := ListTokens(root)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(toks) != 3 {
		t.Errorf("expected 3 tokens, got %d", len(toks))
	}
	seen := map[string]bool{}
	for _, t := range toks {
		seen[t.Agent] = true
	}
	for _, a := range []string{"alice", "bob", "carol"} {
		if !seen[a] {
			t.Errorf("expected agent %s in list", a)
		}
	}
}

// TestRevokeToken_RaceWithMint (security audit F2). Pre-fix,
// RevokeToken did read→modify→write WITHOUT holding the .admin/
// tokens.lock that appendTokenRecord uses. A Revoke racing a Mint
// could read the pre-mint state, then write back after Mint had
// added the new record, SILENTLY DROPPING THE JUST-MINTED TOKEN.
// Operator believes the new agent has a valid token (Mint returned
// it); next ResolveToken sees no record. This is data loss, not just
// a race.
//
// The test fires N parallel goroutines doing Mint+Revoke and asserts
// the final tokens file contains EXACTLY one record per
// successfully-minted token, regardless of revoked state.
func TestRevokeToken_RaceWithMint(t *testing.T) {
	root := initProject(t)
	const workers = 16
	type minted struct {
		plaintext string
		tok       Token
	}
	mints := make(chan minted, workers)
	var wg sync.WaitGroup

	// Half the workers mint then revoke immediately (the race
	// pattern). The other half mint and leave the token active.
	// The total expected count = workers.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		i := i
		agent := "agent-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
		go func() {
			defer wg.Done()
			plaintext, tok, err := MintToken(root, agent)
			if err != nil {
				t.Errorf("MintToken(%s): %v", agent, err)
				return
			}
			mints <- minted{plaintext: plaintext, tok: tok}
			// Half of them also revoke immediately to maximise the
			// race surface.
			if i%2 == 0 {
				if err := RevokeToken(root, tok.ID); err != nil {
					t.Errorf("RevokeToken(%s): %v", tok.ID, err)
				}
			}
		}()
	}
	wg.Wait()
	close(mints)

	// Build the expected set from what Mint returned.
	expected := map[string]bool{}
	for m := range mints {
		expected[m.tok.ID] = true
	}
	if len(expected) != workers {
		t.Fatalf("expected %d successful mints, got %d", workers, len(expected))
	}

	// Read the on-disk store and assert every minted token survived.
	got, err := ListTokens(root)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, tok := range got {
		gotIDs[tok.ID] = true
	}
	for id := range expected {
		if !gotIDs[id] {
			t.Errorf("token %q was minted successfully but is MISSING from on-disk store — race-induced data loss", id)
		}
	}
}
