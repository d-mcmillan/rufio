package mirror

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestWriteAtomic_ConcurrentWritesNoClobber pins the M4 fix: 10
// goroutines all writing the SAME target file MUST all complete
// without errors, and the final file content MUST match exactly one
// of the writers' payloads (no half-written / interleaved bytes).
//
// Pre-fix, two writers both opened `<target>.tmp` for write; the
// second's write overwrote the first's tmp content, and one of the
// rename calls saw an empty / corrupt file. The unique-tmp fix
// (os.CreateTemp) gives each writer its own tmp inode.
func TestWriteAtomic_ConcurrentWritesNoClobber(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.gdl")

	const writers = 10
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	contents := make([]string, writers)
	for i := 0; i < writers; i++ {
		// Distinct content per writer so the final file's content
		// pins which writer "won" the rename race.
		contents[i] = "writer-" + string(rune('a'+i)) + "\n"
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if _, err := writeAtomic(target, contents[idx]); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent writeAtomic error: %v", err)
		}
	}
	final, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	// Final content MUST exactly equal one of the writers' payloads.
	// If we saw a partial / interleaved content, the test fails — no
	// writer should observe a torn write.
	got := string(final)
	matched := false
	for _, c := range contents {
		if got == c {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("final file content %q matches no writer payload — torn write detected", got)
	}

	// And no .tmp files MUST remain in dir (every CreateTemp result
	// is either renamed away or cleaned on error). A stranded .tmp
	// file means a writer crashed without cleaning up.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	tmpCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			tmpCount++
		}
	}
	if tmpCount > 0 {
		t.Errorf("found %d stranded .tmp files; cleanup leak", tmpCount)
	}
}

// TestWriteCursor_ConcurrentWritesNoClobber — audit M2 follow-up.
//
// writeCursor (the cursor checkpoint emitted after every applied
// stream event) used the same fixed `<path>.tmp` pattern that
// writeAtomic had pre-M4. Two concurrent `rufio mirror sync`
// processes against the same `--to` race on
// `<dir>/.rufio/.mirror-cursor.tmp` — the second writer's content
// stomps the first's tmp, and one rename loses to the kernel's
// view of the wrong bytes.
//
// Fix: same os.CreateTemp pattern as writeAtomic. This test
// drives 10 goroutines writing distinct cursor values to the same
// target; pre-fix the race shows up as occasional missing-file or
// torn-content errors, post-fix all writers succeed and the final
// content matches exactly one of the writers' payloads.
func TestWriteCursor_ConcurrentWritesNoClobber(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".mirror-cursor")

	const writers = 10
	cursors := make([]string, writers)
	for i := 0; i < writers; i++ {
		cursors[i] = "cursor-" + string(rune('a'+i))
	}

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := writeCursor(target, cursors[idx]); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent writeCursor error: %v", err)
		}
	}

	// Final content MUST exactly equal one of the writers' payloads
	// (trailing newline is what writeCursor adds).
	final, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read final cursor: %v", err)
	}
	got := strings.TrimRight(string(final), "\n")
	matched := false
	for _, c := range cursors {
		if got == c {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("final cursor %q matches no writer payload — torn write", got)
	}

	// No stranded .tmp files in dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || strings.Contains(e.Name(), ".tmp") {
			t.Errorf("found stranded tmp file %q — cleanup leak", e.Name())
		}
	}
}
