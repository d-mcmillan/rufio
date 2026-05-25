package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func pullFixture(t *testing.T) string {
	t.Helper()
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init", "test"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("init failed: %s", r.Stderr)
	}
	if err := os.MkdirAll(filepath.Join(workdir, "given"), 0o755); err != nil {
		t.Fatal(err)
	}
	return workdir
}

func TestRufioPull_RoundTrip(t *testing.T) {
	workdir := pullFixture(t)
	content := "Refund policy v1\nthreshold: $500\n"
	_ = os.WriteFile(filepath.Join(workdir, "given", "policy.md"), []byte(content), 0o644)
	// pull defaults to stage=live; explicit --stage=live on push so the
	// round-trip resolves under the new push default (#123 changed bare
	// push to draft).
	_ = testutil.RunCLI(t, []string{"push", "given/policy.md", "--stage=live"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"pull", "given/policy.md"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if r.Stdout != content {
		t.Errorf("got %q, want %q", r.Stdout, content)
	}
}

func TestRufioPull_DefaultLatestLive(t *testing.T) {
	workdir := pullFixture(t)
	file := filepath.Join(workdir, "given", "x.md")
	for _, c := range []string{"v1\n", "v2\n", "v3\n"} {
		_ = os.WriteFile(file, []byte(c), 0o644)
		// pull defaults to stage=live; explicit --stage=live so the
		// "latest live" path is exercised under the new push default
		// (#123).
		_ = testutil.RunCLI(t, []string{"push", "given/x.md", "--stage=live"}, workdir, nil)
	}
	r := testutil.RunCLI(t, []string{"pull", "given/x.md"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if r.Stdout != "v3\n" {
		t.Errorf("got %q, want %q", r.Stdout, "v3\n")
	}
}

func TestRufioPull_AtVersionTag(t *testing.T) {
	workdir := pullFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("first\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("second\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"pull", "given/doc.md@v1"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if r.Stdout != "first\n" {
		t.Errorf("got %q, want %q", r.Stdout, "first\n")
	}
}

func TestRufioPull_StageDraft(t *testing.T) {
	workdir := pullFixture(t)
	file := filepath.Join(workdir, "given", "doc.md")
	_ = os.WriteFile(file, []byte("live-v1\n"), 0o644)
	// First push intentionally lands LIVE — needs explicit
	// --stage=live now that the bare default is draft (#123).
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md", "--stage=live"}, workdir, nil)
	_ = os.WriteFile(file, []byte("draft-v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md", "--stage=draft"}, workdir, nil)

	draft := testutil.RunCLI(t, []string{"pull", "given/doc.md", "--stage=draft"}, workdir, nil)
	if draft.Stdout != "draft-v1\n" {
		t.Errorf("draft pull: got %q, want %q", draft.Stdout, "draft-v1\n")
	}
	live := testutil.RunCLI(t, []string{"pull", "given/doc.md"}, workdir, nil)
	if live.Stdout != "live-v1\n" {
		t.Errorf("live pull: got %q, want %q", live.Stdout, "live-v1\n")
	}
}

func TestRufioPull_NoLiveErrors(t *testing.T) {
	workdir := pullFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "draft-only.md"), []byte("wip\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/draft-only.md", "--stage=draft"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"pull", "given/draft-only.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit when no live ref exists")
	}
	mustMatch(t, r.Stderr, `(?i)no version`)
}

func TestRufioPull_UnknownVersion(t *testing.T) {
	workdir := pullFixture(t)
	_ = os.WriteFile(filepath.Join(workdir, "given", "doc.md"), []byte("x\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/doc.md"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"pull", "given/doc.md@v999"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `(?i)no version`)
}

func TestRufioPull_NoRefsForPath(t *testing.T) {
	workdir := pullFixture(t)
	r := testutil.RunCLI(t, []string{"pull", "given/never-pushed.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `(?i)no version|no refs`)
}

func TestRufioPull_JSONShapeWithBase64(t *testing.T) {
	workdir := pullFixture(t)
	content := "binary-safe payload: \x00\x01\x02 bytes\n"
	_ = os.WriteFile(filepath.Join(workdir, "given", "data.md"), []byte(content), 0o644)
	// pull defaults to stage=live; explicit --stage=live now that the
	// bare push default is draft (#123).
	_ = testutil.RunCLI(t, []string{"push", "given/data.md", "--stage=live"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"pull", "given/data.md", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	lines := nonEmptyLines(r.Stdout)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("json: %v", err)
	}
	if obj["path"] != "given/data.md" {
		t.Errorf("path: got %v", obj["path"])
	}
	b64, _ := obj["contentBase64"].(string)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != content {
		t.Errorf("decoded content mismatch: got %q, want %q", decoded, content)
	}
}

func TestRufioPull_BinaryByteForByte(t *testing.T) {
	workdir := pullFixture(t)
	bytesContent := []byte{0x00, 0xff, 0xfe, 0x80, 0x7f, 0x01, 0x02, 0x03, 0xab, 0xcd, 0x0a, 0x0d}
	_ = os.WriteFile(filepath.Join(workdir, "given", "blob.bin"), bytesContent, 0o644)
	// pull defaults to stage=live (#123 default change for push).
	_ = testutil.RunCLI(t, []string{"push", "given/blob.bin", "--stage=live"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"pull", "given/blob.bin", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	var obj map[string]interface{}
	_ = json.Unmarshal([]byte(r.Stdout), &obj)
	decoded, _ := base64.StdEncoding.DecodeString(obj["contentBase64"].(string))
	if !bytes.Equal(decoded, bytesContent) {
		t.Errorf("byte-mismatch: got %v, want %v", decoded, bytesContent)
	}
	wantSHA := sha256.Sum256(bytesContent)
	if obj["sha256"] != hex.EncodeToString(wantSHA[:]) {
		t.Errorf("sha mismatch")
	}
}

func TestRufioPull_RejectsUnknownFlag(t *testing.T) {
	workdir := pullFixture(t)
	r := testutil.RunCLI(t, []string{"pull", "given/x.md", "--bogus"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2", r.Code)
	}
}

func TestRufioPull_QuietIsNoOpForBlobData(t *testing.T) {
	// Strict --quiet rule: blob is data; --quiet must not suppress.
	workdir := pullFixture(t)
	content := "policy data\n"
	_ = os.WriteFile(filepath.Join(workdir, "given", "policy.md"), []byte(content), 0o644)
	// pull defaults to stage=live (#123 default change for push).
	_ = testutil.RunCLI(t, []string{"push", "given/policy.md", "--stage=live"}, workdir, nil)

	r := testutil.RunCLI(t, []string{"pull", "given/policy.md", "--quiet"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if r.Stdout != content {
		t.Errorf("--quiet should NOT suppress blob data; got %q, want %q", r.Stdout, content)
	}
}
