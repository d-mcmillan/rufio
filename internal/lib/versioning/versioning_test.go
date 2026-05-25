package versioning

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
)

// makeWorkdir creates a workdir with rufio.gdl + an initialised .rufio/.
func makeWorkdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, "rufio.gdl"), []byte("@config|name:t|version:1\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := EnsureRufioDir(real); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return real
}

func TestSHA256Of_Deterministic(t *testing.T) {
	got := SHA256Of([]byte("hello"))
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteBlob_Idempotent(t *testing.T) {
	root := makeWorkdir(t)
	sha1, err := WriteBlob(root, []byte("xyz"))
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}
	sha2, err := WriteBlob(root, []byte("xyz"))
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}
	if sha1 != sha2 {
		t.Errorf("sha mismatch: %s vs %s", sha1, sha2)
	}
}

func TestReadBlob_RoundTrips(t *testing.T) {
	root := makeWorkdir(t)
	sha, err := WriteBlob(root, []byte("payload"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadBlob(root, sha)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("got %q, want %q", got, "payload")
	}
}

func TestParsePathSelector_Plain(t *testing.T) {
	path, sel := ParsePathSelector("given/x.md")
	if path != "given/x.md" || sel != nil {
		t.Errorf("got (%q, %+v), want plain", path, sel)
	}
}

func TestParsePathSelector_VersionTag(t *testing.T) {
	path, sel := ParsePathSelector("given/x.md@v3")
	if path != "given/x.md" || sel == nil || sel.Kind != SelectorVersion || sel.Version != 3 {
		t.Errorf("got (%q, %+v), want version=3", path, sel)
	}
}

func TestParsePathSelector_StageTag(t *testing.T) {
	path, sel := ParsePathSelector("p@draft")
	if path != "p" || sel == nil || sel.Kind != SelectorStage || sel.Stage != StageDraft {
		t.Errorf("got (%q, %+v), want draft", path, sel)
	}
}

func TestParsePathSelector_AtSignInFilename(t *testing.T) {
	path, sel := ParsePathSelector("posts/@username.md")
	if path != "posts/@username.md" || sel != nil {
		t.Errorf("got (%q, %+v), want plain (week-1 Phase 2 fix)", path, sel)
	}
}

func TestParsePathSelector_UnknownTailFallsBack(t *testing.T) {
	for _, in := range []string{"path@v999xyz", "path@bogus", "path@v"} {
		p, s := ParsePathSelector(in)
		if p != in || s != nil {
			t.Errorf("%q: got (%q, %+v), want plain fallback", in, p, s)
		}
	}
}

func TestAppendRef_RoundTrip(t *testing.T) {
	root := makeWorkdir(t)
	intent := RefIntent{
		Path: "given/x.md", SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Stage: StageLive, Timestamp: NowISO(), Author: "unknown",
	}
	written, err := AppendRef(root, intent)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if written.Version != 1 {
		t.Errorf("first push version: got %d, want 1", written.Version)
	}
	refs, err := ReadRefs(root, "given/x.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1", len(refs))
	}
	if refs[0].Version != written.Version || refs[0].SHA256 != written.SHA256 {
		t.Errorf("round-trip mismatch: written=%+v read=%+v", written, refs[0])
	}
}

func TestAppendRef_IncrementsAutomatically(t *testing.T) {
	root := makeWorkdir(t)
	intent := RefIntent{
		Path: "given/incr.md", SHA256: "1111111111111111111111111111111111111111111111111111111111111111",
		Stage: StageLive, Timestamp: NowISO(), Author: "unknown",
	}
	v1, _ := AppendRef(root, intent)
	v2, _ := AppendRef(root, intent)
	v3, _ := AppendRef(root, intent)
	if v1.Version != 1 || v2.Version != 2 || v3.Version != 3 {
		t.Errorf("got %d/%d/%d, want 1/2/3", v1.Version, v2.Version, v3.Version)
	}
}

// TestAppendRef_SerialisesConcurrentCallers proves the TOCTOU fix from
// week-1 Phase 4 review I1: 10 concurrent callers must all get distinct
// versions [1..10], not duplicates.
//
// Without the fix (version computed outside the lock), this test would
// observe versions like [1,1,1,...] because every caller would read an
// empty refs file before any writer acquired the lock.
func TestAppendRef_SerialisesConcurrentCallers(t *testing.T) {
	root := makeWorkdir(t)
	intent := RefIntent{
		Path:   "given/concurrent.md",
		SHA256: "2222222222222222222222222222222222222222222222222222222222222222",
		Stage:  StageLive, Timestamp: NowISO(), Author: "unknown",
	}
	const n = 10
	results := make([]RefRecord, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = AppendRef(root, intent)
		}(i)
	}
	wg.Wait()

	versions := make([]int, 0, n)
	for i, e := range errs {
		if e != nil {
			t.Errorf("call %d: %v", i, e)
			continue
		}
		versions = append(versions, results[i].Version)
	}
	sort.Ints(versions)
	for i, v := range versions {
		if v != i+1 {
			t.Errorf("got versions %v, want [1..%d]", versions, n)
			break
		}
	}
}

func TestNextVersion_EmptyAndIncrements(t *testing.T) {
	if got := NextVersion(nil); got != 1 {
		t.Errorf("empty: got %d, want 1", got)
	}
	refs := []RefRecord{
		{Path: "p", Version: 1}, {Path: "p", Version: 2},
	}
	if got := NextVersion(refs); got != 3 {
		t.Errorf("after [1,2]: got %d, want 3", got)
	}
}

func TestLatestRef_ThrowsEmptyRefsError(t *testing.T) {
	_, err := LatestRef(nil)
	var empty *rufioerr.EmptyRefsError
	if !errors.As(err, &empty) {
		t.Errorf("got %T, want *EmptyRefsError", err)
	}
}

func TestLatestRefByStage_FiltersCorrectly(t *testing.T) {
	refs := []RefRecord{
		{Path: "p", Version: 1, Stage: StageDraft},
		{Path: "p", Version: 2, Stage: StageLive},
		{Path: "p", Version: 3, Stage: StageDraft},
	}
	if got := LatestRefByStage(refs, StageDraft); got == nil || got.Version != 3 {
		t.Errorf("draft latest: got %+v, want v3", got)
	}
	if got := LatestRefByStage(refs, StageLive); got == nil || got.Version != 2 {
		t.Errorf("live latest: got %+v, want v2", got)
	}
	if got := LatestRefByStage(refs, StageStaged); got != nil {
		t.Errorf("staged: got %+v, want nil", got)
	}
}

func TestEnsureRufioDir_CreatesSubdirs(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRufioDir(root); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, sub := range []string{"history", "refs", "snapshots", "locks"} {
		p := filepath.Join(root, ".rufio", sub)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", sub, err)
		}
	}
}

func TestEnsureRufioDir_Idempotent(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRufioDir(root); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureRufioDir(root); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestRolledBackFrom_RoundTripsThroughGDL(t *testing.T) {
	root := makeWorkdir(t)
	one := 1
	intent := RefIntent{
		Path:   "given/r.md",
		SHA256: "3333333333333333333333333333333333333333333333333333333333333333",
		Stage:  StageLive, Timestamp: NowISO(), Author: "unknown",
		RolledBackFrom: &one,
	}
	written, err := AppendRef(root, intent)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if written.RolledBackFrom == nil || *written.RolledBackFrom != 1 {
		t.Errorf("written rolledBackFrom: got %+v, want 1", written.RolledBackFrom)
	}
	refs, _ := ReadRefs(root, "given/r.md")
	if len(refs) != 1 || refs[0].RolledBackFrom == nil || *refs[0].RolledBackFrom != 1 {
		t.Errorf("read: %+v", refs)
	}
}

func TestAppendRef_RendersApprovedBy(t *testing.T) {
	root := makeWorkdir(t)
	ref, err := AppendRef(root, RefIntent{
		Path: "given/x.md", SHA256: "sha", Stage: StageStaged,
		Timestamp: "ts", Author: "a", ApprovedBy: "lead-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ApprovedBy != "lead-1" {
		t.Errorf("ApprovedBy=%q", ref.ApprovedBy)
	}
	bs, _ := os.ReadFile(paths.RefsPath(root, "given/x.md"))
	if !strings.Contains(string(bs), "approved-by:lead-1") {
		t.Errorf("rendered ref missing approved-by:\n%s", bs)
	}
}

func TestAppendRef_RendersPromotedFrom(t *testing.T) {
	root := makeWorkdir(t)
	ref, err := AppendRef(root, RefIntent{
		Path: "given/x.md", SHA256: "sha", Stage: StageLive,
		Timestamp: "ts", Author: "a", PromotedFrom: "staged",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.PromotedFrom != "staged" {
		t.Errorf("PromotedFrom=%q", ref.PromotedFrom)
	}
	bs, _ := os.ReadFile(paths.RefsPath(root, "given/x.md"))
	if !strings.Contains(string(bs), "promoted-from:staged") {
		t.Errorf("rendered ref missing promoted-from:\n%s", bs)
	}
}

func TestAppendRef_OmitsFieldsWhenEmpty(t *testing.T) {
	root := makeWorkdir(t)
	if _, err := AppendRef(root, RefIntent{
		Path: "given/x.md", SHA256: "sha", Stage: StageDraft,
		Timestamp: "ts", Author: "a",
	}); err != nil {
		t.Fatal(err)
	}
	bs, _ := os.ReadFile(paths.RefsPath(root, "given/x.md"))
	if strings.Contains(string(bs), "approved-by") {
		t.Errorf("approved-by leaked into ref:\n%s", bs)
	}
	if strings.Contains(string(bs), "promoted-from") {
		t.Errorf("promoted-from leaked into ref:\n%s", bs)
	}
}

func TestRefRecord_RoundTripsApprovedBy(t *testing.T) {
	root := makeWorkdir(t)
	written, err := AppendRef(root, RefIntent{
		Path: "given/x.md", SHA256: "sha", Stage: StageStaged,
		Timestamp: "ts", Author: "a", ApprovedBy: "approver",
	})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := ReadRefs(root, "given/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("len=%d", len(refs))
	}
	if refs[0].ApprovedBy != "approver" {
		t.Errorf("round-trip lost ApprovedBy: got=%q want=approver", refs[0].ApprovedBy)
	}
	if refs[0].Version != written.Version {
		t.Errorf("version mismatch")
	}
}

func TestRefRecord_RoundTripsPromotedFrom(t *testing.T) {
	root := makeWorkdir(t)
	_, err := AppendRef(root, RefIntent{
		Path: "given/x.md", SHA256: "sha", Stage: StageLive,
		Timestamp: "ts", Author: "a", PromotedFrom: "draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	refs, _ := ReadRefs(root, "given/x.md")
	if refs[0].PromotedFrom != "draft" {
		t.Errorf("round-trip lost PromotedFrom: got=%q", refs[0].PromotedFrom)
	}
}
