package identity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		id      string
		wantErr bool
	}{
		{"claude-code", false},
		{"agent-001", false},
		{"a", false},
		{"a1", false},
		{"a-b-c-d", false},
		{"", true},
		{"-leading-dash", true},
		{"UPPERCASE", true},
		{"with_underscore", true},
		{"with.dot", true},
		{"with space", true},
		{"with/slash", true},
		{"with..traversal", true},
	}
	// 65-char id is one past the cap (1 + 64 = 65)
	tooLong := "a"
	for i := 0; i < 64; i++ {
		tooLong += "a"
	}
	cases = append(cases, struct {
		id      string
		wantErr bool
	}{tooLong, true})

	for _, tc := range cases {
		err := Validate(tc.id)
		got := err != nil
		if got != tc.wantErr {
			t.Errorf("Validate(%q): err=%v, wantErr=%v", tc.id, err, tc.wantErr)
		}
		if tc.wantErr {
			var ie *rufioerr.InvalidIdentityError
			if !errors.As(err, &ie) {
				t.Errorf("Validate(%q): want *InvalidIdentityError, got %T", tc.id, err)
			}
		}
	}
}

func TestReadWriteFile(t *testing.T) {
	dir := t.TempDir()

	// Read on missing file returns ("", nil) — not an error, just absence.
	got, err := ReadLocalFile(dir)
	if err != nil {
		t.Fatalf("ReadLocalFile on missing file: %v", err)
	}
	if got != "" {
		t.Errorf("ReadLocalFile on missing file: got %q, want empty", got)
	}

	// Write valid id; read it back.
	if err := WriteLocalFile(dir, "claude-code"); err != nil {
		t.Fatalf("WriteLocalFile: %v", err)
	}
	got, err = ReadLocalFile(dir)
	if err != nil {
		t.Fatalf("ReadLocalFile: %v", err)
	}
	if got != "claude-code" {
		t.Errorf("got %q, want claude-code", got)
	}

	// Overwrite with a different id.
	if err := WriteLocalFile(dir, "cursor"); err != nil {
		t.Fatalf("WriteLocalFile overwrite: %v", err)
	}
	got, _ = ReadLocalFile(dir)
	if got != "cursor" {
		t.Errorf("after overwrite: got %q, want cursor", got)
	}

	// Write rejects invalid ids.
	if err := WriteLocalFile(dir, "BAD ID"); err == nil {
		t.Error("WriteLocalFile accepted invalid id")
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()

	// Neither env nor file → NoIdentityError
	t.Setenv("RUFIO_AGENT_ID", "")
	id, src, err := Resolve(dir)
	var nie *rufioerr.NoIdentityError
	if !errors.As(err, &nie) {
		t.Fatalf("expected NoIdentityError, got %v (id=%q src=%q)", err, id, src)
	}

	// File only → file source
	if err := WriteLocalFile(dir, "from-file"); err != nil {
		t.Fatal(err)
	}
	id, src, err = Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve with file: %v", err)
	}
	if id != "from-file" || src != "file" {
		t.Errorf("got id=%q src=%q, want from-file/file", id, src)
	}

	// Env wins over file
	t.Setenv("RUFIO_AGENT_ID", "from-env")
	id, src, err = Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve with env: %v", err)
	}
	if id != "from-env" || src != "env" {
		t.Errorf("got id=%q src=%q, want from-env/env", id, src)
	}

	// Bad env value rejected
	t.Setenv("RUFIO_AGENT_ID", "BAD ID")
	_, _, err = Resolve(dir)
	var iie *rufioerr.InvalidIdentityError
	if !errors.As(err, &iie) {
		t.Errorf("Resolve with bad env: want InvalidIdentityError, got %v", err)
	}
}

// TestReadLocalFile_TolerantOfComments pins the regression caught by PR #3
// branch review (M1): ReadLocalFile previously did a manual blank/comment
// filter that only recognised `#`, then handed every other line to
// gdl.ParseLine. gdl.ParseLine returns (nil, nil) for `//` comments too —
// so a `//` line slipped past the manual filter, ParseLine returned nil,
// and the next rec.Type access panicked.
//
// Fix: ReadLocalFile now uses gdl.ParseDocument which centralises the
// blank/comment skipping (matches versioning.go usage). This test covers
// both `//` and `#` comments, before AND after the @identity record.
func TestReadLocalFile_TolerantOfComments(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".rufio"), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "// slash-style comment before record\n" +
		"# hash-style comment before record\n" +
		"\n" +
		"@identity|agent:hand-edited|set-at:2026-05-11T00:00:00Z\n" +
		"// slash-style comment after record\n" +
		"# hash-style comment after record\n"
	if err := os.WriteFile(filepath.Join(dir, ".rufio", "identity.local.gdl"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLocalFile(dir)
	if err != nil {
		t.Fatalf("ReadLocalFile: %v", err)
	}
	if got != "hand-edited" {
		t.Errorf("got %q, want hand-edited", got)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteLocalFile(dir, "from-file"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUFIO_AGENT_ID", "from-env")
	if EnvOverride() != "from-env" {
		t.Error("EnvOverride should return from-env")
	}
}
