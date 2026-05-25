package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// writeFile is a small test-only helper: create the parent dir, write
// the given contents. Used by fleet-broaden seeding so we don't lean on
// every per-source write path (attend/think/observe/summon/say) — we
// just plant the on-disk shapes the fleet enumerator must see.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestFleet_EnumeratesAgentsByActivity is the GitHub #115 regression
// guard. Seeds a substrate with four agents — one per kind of activity
// (attention, outbox thought, inbox delivery, learned-author
// observation) — and asserts ALL FOUR appear in `rufio fleet` output.
//
// Pre-fix behaviour: only agent-A (with a current @attention record)
// would appear; the other three were invisible to `fleet` even though
// they had real on-disk activity. The cold-start vet (s1-channel +
// s2-discover, both 2026-05-20) hit exactly that wall: they could see
// peer agents via `ls live/inbox/` + `ls live/outbox/` but `rufio
// fleet` returned empty, so there was no canonical "who is on this
// substrate" command.
func TestFleet_EnumeratesAgentsByActivity(t *testing.T) {
	root := initProject(t)

	// agent-A: attention-only (existing source — pre-fix this was the
	// only source `fleet` consulted). Use the real `attend` command so
	// the on-disk shape is exactly what production writers produce.
	mustAttend(t, root, "agent-a", "debugging auth", []string{"customer:1"})

	// agent-B: outbox-only — has authored a @thought but never
	// attended. Pre-fix: invisible to `fleet`.
	writeFile(t, filepath.Join(root, "live", "outbox", "agent-b", "1747700000000-aaaaaa.gdl"),
		"@thought|id:1747700000000-aaaaaa|author:agent-b|type:hypothesis|subject:customer:1|content:test hypothesis|scope:fleet|ts:2026-05-20T10:00:00Z|ttl:0\n")

	// agent-C: inbox-only — has received a routed thought / summon /
	// channel-message but never attended or written anything. The dir
	// is created lazily by routing on first delivery; a non-empty dir
	// means the agent has been addressed by at least one peer.
	writeFile(t, filepath.Join(root, "live", "inbox", "agent-c", "1747700001000-bbbbbb.gdl"),
		"@thought|id:1747700001000-bbbbbb|author:other|type:hypothesis|subject:customer:1|content:routed in|scope:fleet|ts:2026-05-20T10:00:01Z|ttl:0\n@route|to:agent-c|from:other|ts:2026-05-20T10:00:02Z\n")

	// agent-D: learned-author-only — authored a hand-written
	// @observation under learned/<subject>/<id>.gdlm. Skip the
	// "auto-promote" synthetic author (the daemon, not a real agent).
	writeFile(t, filepath.Join(root, "learned", "customer", "1", "1747700002000-cccccc.gdlm"),
		"@observation|id:1747700002000-cccccc|author:agent-d|subject:customer:1|predicate:tier|object:enterprise|scope:fleet|confidence:1|ts:2026-05-20T10:00:03Z\n")

	// Also seed an auto-promote learned/ record to assert the skip rule
	// (an auto-promoted observation should NOT register "auto-promote"
	// as a fleet member — it's a synthetic author, not an agent).
	writeFile(t, filepath.Join(root, "learned", "customer", "1", "1747700003000-dddddd.gdlm"),
		"@observation|id:1747700003000-dddddd|author:auto-promote|subject:customer:1|predicate:churn-risk|object:high|scope:fleet|confidence:1|ts:2026-05-20T10:00:04Z\n")

	res := testutil.RunCLI(t, []string{"fleet"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	for _, want := range []string{"agent-a", "agent-b", "agent-c", "agent-d"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing %s — fleet must enumerate by ANY activity (not just attention):\n%s",
				want, res.Stdout)
		}
	}
	if strings.Contains(res.Stdout, "auto-promote") {
		t.Errorf("stdout contains 'auto-promote' — that's a synthetic author, not a real agent:\n%s",
			res.Stdout)
	}

	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 4 {
		t.Errorf("want exactly 4 lines (one per real agent), got %d:\n%s", len(lines), res.Stdout)
	}
}
