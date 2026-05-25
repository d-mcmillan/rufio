// Package identity resolves and validates the current agent id used by
// every cognitive primitive. Resolution order:
//
//  1. RUFIO_AGENT_ID environment variable
//  2. .rufio/identity.local.gdl (one @identity record)
//  3. *NoIdentityError
//
// Validation regex: [a-z0-9][a-z0-9-]{0,63}. Rejection at every entry
// point — env read, file read, `identity --as=` write. v1 trust model
// is single-user; spoofing is trivial (followup WK3-FOLLOWUP-3).
package identity

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

var idRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Validate returns *InvalidIdentityError if id fails the format rules.
func Validate(id string) error {
	if !idRegex.MatchString(id) {
		return &rufioerr.InvalidIdentityError{ID: id}
	}
	return nil
}

// localFilePath is the project-relative location of the persisted identity
// record. The .rufio/ prefix is gitignored at repo level (the file never
// travels with the project).
func localFilePath(root string) string {
	return filepath.Join(root, ".rufio", "identity.local.gdl")
}

// ReadLocalFile returns the agent id stored in .rufio/identity.local.gdl,
// or "" if the file doesn't exist. Returns *InvalidIdentityError if the
// stored id fails Validate; underlying parse failures bubble up.
//
// Note: the `set-at` field written by WriteLocalFile is deliberately not
// read here — it's metadata reserved for future `whoami --verbose`
// consumption.
func ReadLocalFile(root string) (string, error) {
	bs, err := os.ReadFile(localFilePath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return "", err
	}
	for _, rec := range records {
		if rec.Type != "identity" {
			continue
		}
		id := rec.Get("agent")
		if err := Validate(id); err != nil {
			return "", err
		}
		return id, nil
	}
	return "", nil
}

// WriteLocalFile validates id then writes a single @identity record to
// .rufio/identity.local.gdl, overwriting any existing content.
//
// Atomic via .tmp + rename so a partial write can never produce a
// half-parsed identity file.
func WriteLocalFile(root, id string) error {
	if err := Validate(id); err != nil {
		return err
	}
	dir := filepath.Join(root, ".rufio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rec := gdl.Record{Type: "identity", Fields: []gdl.RecordField{
		{Key: "agent", Value: id},
		{Key: "set-at", Value: versioning.NowISO()},
	}}
	contents := gdl.RenderLine(rec) + "\n"
	tmp := localFilePath(root) + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// identity.local.gdl.tmp under .rufio/ (#141). Success path: Rename
	// already moved tmp, so this Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmp) }()
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, localFilePath(root))
}

// EnvOverride returns the value of RUFIO_AGENT_ID, or "" if unset/empty.
// Whitespace stripped. Used by the `identity --as=` warning logic AND
// by Resolve.
func EnvOverride() string {
	return strings.TrimSpace(os.Getenv("RUFIO_AGENT_ID"))
}

// Resolve returns (id, source, error) where source is "env" or "file".
//
//	env > file > NoIdentityError
//
// Both env and file values are validated; an invalid env value short-
// circuits with InvalidIdentityError before consulting the file.
func Resolve(root string) (string, string, error) {
	if env := EnvOverride(); env != "" {
		if err := Validate(env); err != nil {
			return "", "", err
		}
		return env, "env", nil
	}
	id, err := ReadLocalFile(root)
	if err != nil {
		return "", "", err
	}
	if id == "" {
		return "", "", &rufioerr.NoIdentityError{}
	}
	return id, "file", nil
}
