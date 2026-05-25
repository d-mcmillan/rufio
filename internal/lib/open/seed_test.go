package open

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// seedThought writes a minimal @thought record to live/outbox/<author>/<id>.gdl
// for tests. Mirrors thought.Write but lets the test caller pin id, ts,
// and topics so test assertions are deterministic. Uses the same wire
// format the real CLI emits — scanOutbox in recall picks the record up
// unchanged.
//
// scope defaults to "fleet" when caller passes empty; topics is omitted
// (per D4.9) when nil/empty.
func seedThought(t *testing.T, root, author, id, subject, content, scope string, topics []string, ts string) {
	t.Helper()
	if scope == "" {
		scope = "fleet"
	}
	in := thought.ThoughtInput{
		ID:      id,
		Author:  author,
		Type:    "hypothesis",
		Subject: subject,
		Content: content,
		Scope:   scope,
		Topics:  topics,
		TS:      ts,
		TTL:     0,
	}
	rec := thought.BuildThoughtRecord(in)
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("seedThought: %v", err)
	}
}

// seedAttention writes a minimal @attention record to
// live/attention/<agent>.gdl. Used to populate the fleet + attention
// sections deterministically.
func seedAttention(t *testing.T, root, agent string, lastSeen time.Time) {
	t.Helper()
	ts := lastSeen.UTC().Format(time.RFC3339Nano)
	rec := attention.BuildRecord(agent, "test intent for "+agent, "fleet",
		[]string{"test:1"}, nil, ts)
	if err := attention.Write(root, agent, rec); err != nil {
		t.Fatalf("seedAttention: %v", err)
	}
}

// counterID returns a fresh thought-id-shaped string from the
// per-test-process counter. Avoids reusing thought.GenerateID() which
// uses time.Now (collides under fast test loops).
var seedCounter int64

func freshID(t *testing.T) string {
	t.Helper()
	seedCounter++
	// Format matches thought.GenerateID's <unix-millis>-<rand6> shape so
	// idFromPath and ValidateSubject behave identically.
	base := time.Now().UnixNano() / int64(time.Millisecond)
	return strconv.FormatInt(base, 10) + "-" + strings.Repeat("a", 6) + strconv.FormatInt(seedCounter, 10)
}

// mustWriteFile is a tiny helper for tests that need to drop an arbitrary
// file at a relative path under root. Creates parent dirs as needed.
func mustWriteFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
}
