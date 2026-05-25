package mirror

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/serve"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// initProject scaffolds a minimal rufio project on disk. Used as the
// "server" substrate root in mirror tests.
func initProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte("@config|name:test|version:1\n"), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention", ".rufio/.admin"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	return root
}

func seedThought(t *testing.T, root, author, scope, subject, content string) string {
	t.Helper()
	id, err := thought.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID:      id,
		Author:  author,
		Type:    "hypothesis",
		Subject: subject,
		Content: content,
		Scope:   scope,
		TS:      versioning.NowISO(),
		TTL:     0,
	})
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write: %v", err)
	}
	return id
}

// startServer starts an httptest server backed by `root` and returns its
// URL plus alice's bearer-token plaintext.
func startServer(t *testing.T, root string) (string, string) {
	t.Helper()
	plaintext, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	h, err := serve.Handler(serve.Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL, plaintext
}

func TestMirrorPull_FetchesVisibleRecords(t *testing.T) {
	srvRoot := initProject(t)
	_ = seedThought(t, srvRoot, "alice", "fleet", "test:1", "alice fleet hypothesis")
	srvURL, token := startServer(t, srvRoot)

	mirrorDir := t.TempDir()
	st, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: token, To: mirrorDir, InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if st.Wrote == 0 {
		t.Errorf("expected at least one record written; stats=%+v", st)
	}

	// Verify the alice outbox dir was populated.
	aliceDir := filepath.Join(mirrorDir, "live", "outbox", "alice")
	entries, err := os.ReadDir(aliceDir)
	if err != nil {
		t.Fatalf("read alice mirror dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("no files mirrored to alice's outbox")
	}
}

func TestMirrorPull_RespectsPrivacy(t *testing.T) {
	srvRoot := initProject(t)
	// Bob's scope=agent record should NOT mirror to alice.
	_ = seedThought(t, srvRoot, "bob", "agent", "test:1", "bob secret private")
	srvURL, aliceToken := startServer(t, srvRoot)

	mirrorDir := t.TempDir()
	if _, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: aliceToken, To: mirrorDir, InsecureTLS: true,
	}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Walk the mirror; nothing should contain bob's secret.
	leaked := false
	_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if bs, _ := os.ReadFile(p); strings.Contains(string(bs), "bob secret private") {
			leaked = true
		}
		return nil
	})
	if leaked {
		t.Fatal("alice's mirror contains bob's scope=agent record — privacy floor breached")
	}
}

func TestMirrorPull_Idempotent(t *testing.T) {
	srvRoot := initProject(t)
	_ = seedThought(t, srvRoot, "alice", "fleet", "test:1", "alice fleet")
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()

	first, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: token, To: mirrorDir, InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Pull 1: %v", err)
	}
	second, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: token, To: mirrorDir, InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Pull 2: %v", err)
	}
	if first.Wrote == 0 {
		t.Errorf("first pull should write something; stats=%+v", first)
	}
	if second.Wrote != 0 {
		t.Errorf("second pull should be idempotent (wrote=0); stats=%+v", second)
	}
	if second.Unchanged != first.Wrote {
		t.Errorf("second pull should report all-unchanged; stats=%+v", second)
	}
}

func TestMirrorPull_PreservesGDLFormat(t *testing.T) {
	srvRoot := initProject(t)
	_ = seedThought(t, srvRoot, "alice", "fleet", "test:1", "content with | pipe and : colon")
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()
	if _, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: token, To: mirrorDir, InsecureTLS: true,
	}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Walk + parse every .gdl file in the mirror; every line must
	// parse successfully via gdl.ParseDocument.
	_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".gdl") {
			return nil
		}
		bs, _ := os.ReadFile(p)
		recs, perr := gdl.ParseDocument(string(bs))
		if perr != nil {
			t.Errorf("mirror file %s did not round-trip through gdl parser: %v\n%s", p, perr, bs)
		}
		if len(recs) == 0 {
			t.Errorf("mirror file %s has no parseable records: %q", p, bs)
		}
		return nil
	})
}

// seedThoughtWithTopicsAndTTL writes a thought carrying both a non-empty
// topics: CSV and a non-zero ttl: integer — exercises the v1.0.4 bug #1
// regression path (recall's JSON shape pre-fix dropped both fields, and
// the mirror snapshot's renderRecord could not reconstruct them).
func seedThoughtWithTopicsAndTTL(t *testing.T, root, author, content string, topics []string, ttl int) string {
	t.Helper()
	id, err := thought.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID:      id,
		Author:  author,
		Type:    "hypothesis",
		Subject: "test:1",
		Content: content,
		Scope:   "fleet",
		Topics:  topics,
		TS:      versioning.NowISO(),
		TTL:     ttl,
	})
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write: %v", err)
	}
	return id
}

// TestMirrorPull_PreservesTopicsAndTTL (v1.0.4 bug #1 regression guard).
// A thought with non-empty topics + non-zero ttl on the server must
// round-trip through the snapshot path into the mirror file with both
// fields intact. Pre-fix, recall's JSON output dropped both keys, so
// renderRecord saw no entries for them and emitted a GDL line without
// them — silent data loss in the file-native local shadow.
func TestMirrorPull_PreservesTopicsAndTTL(t *testing.T) {
	srvRoot := initProject(t)
	_ = seedThoughtWithTopicsAndTTL(t, srvRoot, "alice", "topics+ttl regression", []string{"alpha", "beta"}, 600)
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()
	if _, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: token, To: mirrorDir, InsecureTLS: true,
	}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	gotTopics, gotTTL := false, false
	_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".gdl") {
			return nil
		}
		bs, _ := os.ReadFile(p)
		// On-disk form (GDL field escaping does not touch alpha or 600):
		//   ...|topics:alpha,beta|...|ttl:600|...
		if strings.Contains(string(bs), "topics:alpha,beta") {
			gotTopics = true
		}
		if strings.Contains(string(bs), "ttl:600") {
			gotTTL = true
		}
		return nil
	})
	if !gotTopics {
		t.Errorf("mirror file did not include topics:alpha,beta (v1.0.4 bug #1 regression)")
	}
	if !gotTTL {
		t.Errorf("mirror file did not include ttl:600 (v1.0.4 bug #1 regression)")
	}
}

func TestMirrorPull_RequiresFlags(t *testing.T) {
	_, err := Pull(context.Background(), SnapshotOptions{})
	if err == nil {
		t.Fatal("expected error with no flags")
	}
	_, err = Pull(context.Background(), SnapshotOptions{ServerURL: "x"})
	if err == nil {
		t.Fatal("expected error with no token")
	}
	_, err = Pull(context.Background(), SnapshotOptions{ServerURL: "x", Token: "y"})
	if err == nil {
		t.Fatal("expected error with no To")
	}
}

// TestMirrorPull_RejectsPathTraversal (security audit H1). The snapshot
// path assembles its on-disk location from untrusted record fields
// (id, author, content_path). A crafted record with traversal segments
// in any of those fields must be rejected by projectRecordToFile BEFORE
// the result reaches filepath.Join. This is the source-layer guard;
// joinUnderRoot is the downstream belt-and-suspenders.
func TestMirrorPull_RejectsPathTraversal(t *testing.T) {
	cases := []struct {
		name string
		rec  map[string]interface{}
	}{
		{
			"thought-author-traversal",
			map[string]interface{}{"_type": "thought", "id": "1-a", "author": "../../../etc/cron.d"},
		},
		{
			"thought-id-traversal",
			map[string]interface{}{"_type": "thought", "id": "../../etc/passwd", "author": "alice"},
		},
		{
			"thought-author-with-slash",
			map[string]interface{}{"_type": "thought", "id": "1-a", "author": "alice/../bob"},
		},
		{
			"thought-author-nul",
			map[string]interface{}{"_type": "thought", "id": "1-a", "author": "alice\x00bob"},
		},
		{
			"given-content_path-traversal",
			map[string]interface{}{"_type": "given", "content_path": "../../../etc/passwd"},
		},
		{
			"learned-content_path-absolute",
			map[string]interface{}{"_type": "learned", "content_path": "/etc/passwd"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, _, err := projectRecordToFile(c.rec)
			if err == nil {
				t.Errorf("projectRecordToFile accepted traversal record (%v), produced path=%q", c.rec, path)
			}
		})
	}
}

// TestMirrorPull_EndToEnd_RejectsTraversal exercises the downstream
// belt-and-suspenders: even if projectRecordToFile were ever bypassed
// (a future field added without a guard), joinUnderRoot in the Pull
// loop catches the escape attempt. This pins the second line of
// defense at the writeAtomic call-site granularity.
func TestMirrorPull_EndToEnd_RejectsTraversal(t *testing.T) {
	to := t.TempDir()
	for _, p := range []string{
		"live/../../../etc/cron.d/exploit",
		"/etc/passwd",
		"../../etc/passwd",
	} {
		if _, err := joinUnderRoot(to, p); err == nil {
			t.Errorf("joinUnderRoot accepted traversal %q — second line of defense breached", p)
		}
	}
}

// TestMirrorPull_AutoCreatesToDirIfMissing regression-pins the F-series
// bug for the snapshot path: `rufio mirror pull --to=DIR` where DIR
// does NOT exist beforehand must auto-create it and write the records.
// Sister test of TestMirrorSync_AutoCreatesToDirIfMissing.
//
// Pre-fix, the F4 symlink-defense walked up filepath.EvalSymlinks past
// the non-existent --to dir to an existing ancestor (e.g. /tmp), then
// found the ancestor's resolved form did not sit under --to and
// rejected every record as a "symlink escape". Result: Pull returned
// wrote=0 with no on-disk artifacts, and silent skips counted in
// SkippedNoPath.
func TestMirrorPull_AutoCreatesToDirIfMissing(t *testing.T) {
	srvRoot := initProject(t)
	_ = seedThought(t, srvRoot, "alice", "fleet", "test:1", "auto-create pull marker")
	srvURL, token := startServer(t, srvRoot)
	parent := t.TempDir()
	mirrorDir := filepath.Join(parent, "fresh-pull-does-not-exist-yet")
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Fatalf("test setup error: %s should not exist; stat=%v", mirrorDir, err)
	}

	st, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: token, To: mirrorDir, InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Pull on fresh --to should succeed: %v", err)
	}
	if st.Wrote == 0 {
		t.Errorf("expected at least one record written into fresh dir; stats=%+v", st)
	}
	if _, err := os.Stat(mirrorDir); err != nil {
		t.Errorf("--to directory must exist after Pull runs: %v", err)
	}
	// And the seeded record must actually be on disk.
	found := false
	_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if bs, _ := os.ReadFile(p); strings.Contains(string(bs), "auto-create pull marker") {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("seeded record not found in fresh --to dir after Pull")
	}
}

// TestMirrorPull_IncludesLearnedRecords (Gate 4 follow-up). Snapshot
// equivalent of TestMirrorSync_PropagatesLearnedRecords. Seed a
// promoted observation BEFORE pull, then pull, then assert the
// learned/.gdlm file landed in the mirror under the same subject-
// segmented path the server uses.
func TestMirrorPull_IncludesLearnedRecords(t *testing.T) {
	srvRoot := initProject(t)
	obsID := seedPromotedObservation(t, srvRoot, "demo:2", "has", "promoted-marker-pull")
	srvURL, token := startServer(t, srvRoot)
	mirrorDir := t.TempDir()

	st, err := Pull(context.Background(), SnapshotOptions{
		ServerURL: srvURL + "/mcp", Token: token, To: mirrorDir, InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if st.Wrote == 0 {
		t.Errorf("expected at least one record written; stats=%+v", st)
	}
	wantRel := filepath.Join("learned", "demo", "2", obsID+".gdlm")
	wantAbs := filepath.Join(mirrorDir, wantRel)
	bs, err := os.ReadFile(wantAbs)
	if err != nil {
		var paths []string
		_ = filepath.Walk(mirrorDir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				paths = append(paths, p)
			}
			return nil
		})
		t.Fatalf("learned observation NOT in pulled mirror. wantAbs=%s err=%v contents=%v", wantAbs, err, paths)
	}
	if !strings.Contains(string(bs), "promoted-marker-pull") {
		t.Errorf("learned mirror file content missing marker: %q", bs)
	}
}
