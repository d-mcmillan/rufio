package mirror

import (
	"strings"
	"testing"
)

// TestProjectRecordToFile_RejectsTraversalPastTopLevel pins the v1.0.5
// security audit N1 fix. A hostile server emitting a wire path whose
// RAW form starts with `live/` but whose CLEANED form escapes the
// canonical substrate dirs MUST be rejected before the bytes touch
// disk. Pre-fix, `live/../.rufio/.mirror-cursor` passed the raw
// HasPrefix gate and landed under .rufio/ — bypass.
func TestProjectRecordToFile_RejectsTraversalPastTopLevel(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"live-then-traverse-to-dotrufio", "live/../.rufio/.mirror-cursor"},
		{"learned-then-traverse", "learned/../.rufio/foo"},
		{"given-then-traverse", "given/../.rufio/bar"},
		{"learned-collapse-to-dot", "learned/.."},
		// And the direct rejection cases (top-level not in allow-list).
		{"raw-dotrufio", ".rufio/.mirror-cursor"},
		{"raw-etc", "etc/passwd"},
		{"raw-relative-dot", "./.rufio/foo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := map[string]interface{}{
				"_type": "thought",
				"path":  c.path,
			}
			_, _, err := projectRecordToFile(rec)
			if err == nil {
				t.Fatalf("projectRecordToFile(%q) accepted malicious wire path — N1 floor breached", c.path)
			}
			if !strings.Contains(err.Error(), "rejected suspicious path") {
				t.Errorf("error %q lacks 'rejected suspicious path' prefix", err)
			}
		})
	}
}

// TestProjectRecordToFile_HappyPath_StillAccepts pins that the N1 fix
// doesn't reject legitimate canonical paths. Every substrate root
// segment must continue to work — outbox, channels, summons, goals
// under live/; subject-pathed observations under learned/;
// content-addressed given/ blobs.
func TestProjectRecordToFile_HappyPath_StillAccepts(t *testing.T) {
	cases := []string{
		"live/outbox/alice/1779000000000-abc123.gdl",
		"live/channels/active/ch-001/meta.gdl",
		"live/goals/active/g-001.gdl",
		"learned/customer/5821/1779000000000-xyz.gdlm",
		"given/policy/x.md",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			rec := map[string]interface{}{
				"_type":  "thought",
				"path":   p,
				"id":     "1779000000000-abc123",
				"author": "alice",
			}
			gotPath, _, err := projectRecordToFile(rec)
			if err != nil {
				t.Fatalf("projectRecordToFile(%q) rejected legitimate path: %v", p, err)
			}
			if gotPath != p {
				t.Errorf("expected wire path %q to round-trip; got %q", p, gotPath)
			}
		})
	}
}
