package mirror

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPersistEvent_RejectsTraversalPastTopLevel — audit H1 follow-up.
//
// The N1 fix in v1.0.5 wired validateCleanedTopLevel onto the snapshot
// path (projectRecordToFile), but the live-sync path (persistEvent in
// sync.go) was missed. A hostile or DNS-hijacked server emitting
// `path:"live/../.rufio/.mirror-cursor"` passes safeRelPath (it does
// NOT escape root — clean form is `.rufio/.mirror-cursor`, which is
// under root) and joinUnderRoot (same check, also passes), then
// writeAtomic clobbers files inside the mirror root but OUTSIDE the
// canonical substrate dirs.
//
// This is the exact attack N1 was supposed to close, just on the
// more commonly used long-running mode. Pre-fix the test fails;
// post-fix every case rejects.
func TestPersistEvent_RejectsTraversalPastTopLevel(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		// The headline N1 exploit — collapses to .rufio/.mirror-cursor,
		// inside root but outside {live, learned, given}.
		{"live-then-traverse-to-dotrufio", "live/../.rufio/.mirror-cursor"},
		// Variant: collapses to .git/hooks/post-merge — could trigger
		// arbitrary code execution next time the mirror is used as a
		// git checkout.
		{"learned-then-traverse-to-githook", "learned/../.git/hooks/post-merge"},
		// Direct top-level paths outside the allow-list.
		{"raw-dotrufio", ".rufio/.mirror-cursor"},
		{"raw-bashrc", ".bashrc"},
		{"raw-githook", ".git/hooks/post-merge"},
		// And the cleaned-to-dot edge case.
		{"learned-collapse-to-dot", "learned/.."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			to := t.TempDir()
			ev := map[string]interface{}{
				"_type": "thought",
				"path":  c.path,
				"raw":   "@thought|id:x|author:attacker|content:exploit|scope:fleet|ts:2026-05-22T00:00:00Z",
				"ts":    "2026-05-22T00:00:00Z",
			}
			bs, _ := json.Marshal(ev)
			_, err := persistEvent(string(bs), to)
			if err == nil {
				t.Fatalf("persistEvent accepted N1-class path %q (must reject after the audit-H1 fix)", c.path)
			}
			if !strings.Contains(err.Error(), "rejected suspicious path") {
				t.Errorf("error should mention 'rejected suspicious path'; got %v", err)
			}
			// Belt-and-suspenders: walk the mirror root and confirm
			// no file landed outside {live, learned, given}.
			_ = filepath.Walk(to, func(p string, info os.FileInfo, walkErr error) error {
				if walkErr != nil || info == nil || info.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(to, p)
				top := strings.SplitN(rel, string(filepath.Separator), 2)[0]
				if top != "live" && top != "learned" && top != "given" {
					t.Errorf("found unexpected file %q (top-level %q) — N1 bypassed", rel, top)
				}
				return nil
			})
		})
	}
}

// TestPersistEvent_HappyPath_StillAccepts — regression guard so the
// N1 tightening doesn't reject legitimate substrate paths.
func TestPersistEvent_HappyPath_StillAccepts(t *testing.T) {
	cases := []string{
		"live/outbox/alice/1779000000000-abc123.gdl",
		"live/channels/active/ch-001/meta.gdl",
		"learned/customer/5821/1779000000000-xyz.gdlm",
		"given/policy/x.md",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			to := t.TempDir()
			ev := map[string]interface{}{
				"_type": "thought",
				"path":  p,
				"raw":   "@thought|id:x|author:alice|content:ok|scope:fleet|ts:2026-05-22T00:00:00Z",
				"ts":    "2026-05-22T00:00:00Z",
			}
			bs, _ := json.Marshal(ev)
			if _, err := persistEvent(string(bs), to); err != nil {
				t.Errorf("persistEvent rejected legitimate path %q: %v", p, err)
			}
		})
	}
}
