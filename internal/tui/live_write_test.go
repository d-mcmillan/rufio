// live_write_test.go — G-interact: deterministic proof that each emit*
// writes the CORRECT substrate record via the CORRECT lib as `me`,
// against a t.TempDir() real root. NO live fsnotify / NO wall-clock
// assertions (the ts is written by versioning.NowISO like the CLI; we
// assert the record fields + author, never a golden-pinned ts). These
// are the (c)-leg controller proofs: each verb → the right record by the
// right lib as the resolved operator identity.
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// readGDLFiles reads every *.gdl/*.gdlm under dir (recursive) and returns
// the parsed records (one slice per file, flattened). Used to assert the
// on-disk record an emit* produced.
func readGDLFiles(t *testing.T, dir string) []gdl.Record {
	t.Helper()
	var out []gdl.Record
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".gdl") && !strings.HasSuffix(p, ".gdlm") {
			return nil
		}
		bs, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		recs, perr := gdl.ParseDocument(string(bs))
		if perr != nil {
			t.Fatalf("parse %s: %v", p, perr)
		}
		out = append(out, recs...)
		return nil
	})
	return out
}

// findRec returns the first record of the given @type, or fails.
func findRec(t *testing.T, recs []gdl.Record, typ string) gdl.Record {
	t.Helper()
	for _, r := range recs {
		if r.Type == typ {
			return r
		}
	}
	t.Fatalf("no @%s record found in %d records", typ, len(recs))
	return gdl.Record{}
}

const testMe = "operator"

func TestEmitThought_WritesBroadcastFocusAsMe(t *testing.T) {
	root := t.TempDir()
	if err := emitThought(root, testMe, "customer:5821", "downgrade approved — notify"); err != nil {
		t.Fatalf("emitThought: %v", err)
	}
	recs := readGDLFiles(t, filepath.Join(root, "live", "outbox", testMe))
	r := findRec(t, recs, "thought")
	if r.Get("author") != testMe {
		t.Errorf("author=%q, want %q (must be authored as the resolved operator)", r.Get("author"), testMe)
	}
	if r.Get("type") != opThoughtType {
		t.Errorf("type=%q, want %q (approved free-text default)", r.Get("type"), opThoughtType)
	}
	if r.Get("scope") != opThoughtScope {
		t.Errorf("scope=%q, want %q (approved free-text default)", r.Get("scope"), opThoughtScope)
	}
	if r.Get("subject") != "customer:5821" {
		t.Errorf("subject=%q, want customer:5821 (the resolved focused entity)", r.Get("subject"))
	}
	if r.Get("content") != "downgrade approved — notify" {
		t.Errorf("content=%q", r.Get("content"))
	}
}

func TestEmitThought_RejectsEmptyContent(t *testing.T) {
	root := t.TempDir()
	if err := emitThought(root, testMe, "general", "   "); err == nil {
		t.Fatalf("emitThought with empty content must error (thought.ValidateContent)")
	}
	// No record must be written on a validation failure.
	if _, err := os.Stat(filepath.Join(root, "live", "outbox")); err == nil {
		t.Errorf("validation failure must not produce a write side effect")
	}
}

// seedThought writes a thought by `author` and returns its id (for the
// confirm/refute target-exists check — retract.Lookup globs live/outbox).
func seedThought(t *testing.T, root, author, subject, content string) string {
	t.Helper()
	if err := emitThought(root, author, subject, content); err != nil {
		t.Fatalf("seedThought: %v", err)
	}
	dir := filepath.Join(root, "live", "outbox", author)
	ents, _ := os.ReadDir(dir)
	if len(ents) == 0 {
		t.Fatalf("seedThought wrote nothing under %s", dir)
	}
	// id == filename without .gdl (thought.Write target is <id>.gdl).
	return strings.TrimSuffix(ents[len(ents)-1].Name(), ".gdl")
}

// seedDecision writes a real `type=decision` @thought by `author` and
// returns its id (so projectThread emits a roleDecision row that carries
// a Quorum once confirms accumulate — the (b) demo path). Distinct from
// seedThought (which goes through emitThought → type=focus).
func seedDecision(t *testing.T, root, author, subject, content string) string {
	t.Helper()
	id, err := thought.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: author, Type: "decision", Subject: subject,
		Content: content, Scope: "fleet", TS: versioning.NowISO(),
	})
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write decision: %v", err)
	}
	return id
}

func TestEmitConfirm_AppendsConfirmByMe(t *testing.T) {
	root := t.TempDir()
	id := seedThought(t, root, "claude-code", "customer:5821", "decision: offer downgrade")
	if err := emitConfirm(root, testMe, id, "verified by ops"); err != nil {
		t.Fatalf("emitConfirm: %v", err)
	}
	tally, err := confirm.ReadAll(root, id)
	if err != nil {
		t.Fatalf("confirm.ReadAll: %v", err)
	}
	found := false
	for _, c := range tally.Confirms {
		if c == testMe {
			found = true
		}
	}
	if !found {
		t.Errorf("confirm tally %v missing %q (confirm must be authored as me)", tally.Confirms, testMe)
	}
}

func TestEmitConfirm_RejectsMissingTarget(t *testing.T) {
	root := t.TempDir()
	if err := emitConfirm(root, testMe, "0-nope", ""); err == nil {
		t.Fatalf("emitConfirm on a nonexistent thought-id must error (retract.Lookup)")
	}
}

func TestEmitRefute_AppendsRefuteByMe(t *testing.T) {
	root := t.TempDir()
	id := seedThought(t, root, "claude-code", "customer:5821", "decision: churn-save discount")
	if err := emitRefute(root, testMe, id, "contradicts prior preference", ""); err != nil {
		t.Fatalf("emitRefute: %v", err)
	}
	tally, _ := confirm.ReadAll(root, id)
	found := false
	for _, r := range tally.Refutes {
		if r == testMe {
			found = true
		}
	}
	if !found {
		t.Errorf("refute tally %v missing %q", tally.Refutes, testMe)
	}
}

func TestEmitRefute_RequiresReason(t *testing.T) {
	root := t.TempDir()
	id := seedThought(t, root, "claude-code", "customer:5821", "a decision")
	if err := emitRefute(root, testMe, id, "  ", ""); err == nil {
		t.Fatalf("emitRefute with empty reason must error (mirrors cli/refute.go)")
	}
}

func TestEmitGoal_WritesActiveGoalByMe(t *testing.T) {
	root := t.TempDir()
	if err := emitGoal(root, testMe, "stabilise customer:5821", "EOW", ""); err != nil {
		t.Fatalf("emitGoal: %v", err)
	}
	recs := readGDLFiles(t, filepath.Join(root, "live", "goals", "active"))
	r := findRec(t, recs, "goal")
	if r.Get("author") != testMe {
		t.Errorf("goal author=%q, want %q", r.Get("author"), testMe)
	}
	if r.Get("statement") != "stabilise customer:5821" {
		t.Errorf("goal statement=%q", r.Get("statement"))
	}
}

func TestEmitObserve_WritesObservationByMe(t *testing.T) {
	root := t.TempDir()
	if err := emitObserve(root, testMe, "customer:5821", "prefers", "email-contact", ""); err != nil {
		t.Fatalf("emitObserve: %v", err)
	}
	recs := readGDLFiles(t, filepath.Join(root, "learned"))
	r := findRec(t, recs, "observation")
	if r.Get("author") != testMe {
		t.Errorf("observation author=%q, want %q", r.Get("author"), testMe)
	}
	if r.Get("subject") != "customer:5821" || r.Get("predicate") != "prefers" || r.Get("object") != "email-contact" {
		t.Errorf("observation triple wrong: s=%q p=%q o=%q", r.Get("subject"), r.Get("predicate"), r.Get("object"))
	}
}

func TestEmitAttend_WritesAttentionByMe(t *testing.T) {
	root := t.TempDir()
	if err := emitAttend(root, testMe, "steering the churn arc", []string{"customer:5821"}); err != nil {
		t.Fatalf("emitAttend: %v", err)
	}
	recs := readGDLFiles(t, filepath.Join(root, "live", "attention"))
	r := findRec(t, recs, "attention")
	if r.Get("agent") != testMe {
		t.Errorf("attention agent=%q, want %q", r.Get("agent"), testMe)
	}
}

func TestEmitSummon_WritesPendingSummonFromMe(t *testing.T) {
	root := t.TempDir()
	if err := emitSummon(root, testMe, "claude-code", "customer:5821", "discuss downgrade"); err != nil {
		t.Fatalf("emitSummon: %v", err)
	}
	recs := readGDLFiles(t, filepath.Join(root, "live", "summons", "pending"))
	r := findRec(t, recs, "summon")
	if r.Get("from") != testMe {
		t.Errorf("summon from=%q, want %q", r.Get("from"), testMe)
	}
	if r.Get("to") != "claude-code" {
		t.Errorf("summon to=%q, want claude-code", r.Get("to"))
	}
}

func TestEmitSay_RejectsSayIntoNonexistentChannel(t *testing.T) {
	root := t.TempDir()
	// No channel exists — say must surface NoSuchChannel (the CLI gate),
	// never crash, never write.
	if err := emitSay(root, testMe, "ch-nope", "hello"); err == nil {
		t.Fatalf("emitSay into a nonexistent channel must error (channels.LoadMeta)")
	}
}

// TestEmitDirected_SummonsWhenNoChannel proves the @agent transport falls
// back to a SUMMON when no reusable channel exists (the honest substrate
// semantics — a channel needs the summon→accept handshake; the operator
// cannot unilaterally create one).
func TestEmitDirected_SummonsWhenNoChannel(t *testing.T) {
	root := t.TempDir()
	note, err := emitDirected(root, testMe, "claude-code", "can you look at customer:5821?")
	if err != nil {
		t.Fatalf("emitDirected: %v", err)
	}
	if !strings.Contains(note, "summoned") {
		t.Errorf("note=%q, want a summon note (no channel exists yet)", note)
	}
	recs := readGDLFiles(t, filepath.Join(root, "live", "summons", "pending"))
	r := findRec(t, recs, "summon")
	if r.Get("from") != testMe || r.Get("to") != "claude-code" {
		t.Errorf("directed summon from=%q to=%q, want %q→claude-code", r.Get("from"), r.Get("to"), testMe)
	}
}

// TestEmitDirected_SaysIntoReusableChannel proves that once a channel
// exists with `me` as a current member and the agent as the other party,
// the @agent transport REUSES it (a say, not a second summon) — exactly
// the CLI say behaviour.
func TestEmitDirected_SaysIntoReusableChannel(t *testing.T) {
	root := t.TempDir()
	chID := seedActiveChannel(t, root, testMe, "claude-code", "customer:5821")
	note, err := emitDirected(root, testMe, "claude-code", "status?")
	if err != nil {
		t.Fatalf("emitDirected (reuse): %v", err)
	}
	if !strings.Contains(note, "said") {
		t.Errorf("note=%q, want a say note (reusable channel exists)", note)
	}
	recs := readGDLFiles(t, filepath.Join(root, "live", "channels", "active", chID, "messages"))
	// Issue #107: on-disk Type is now "channel-message" (see
	// channels.BuildSayRecord). CLI verb still `say`.
	r := findRec(t, recs, "channel-message")
	if r.Get("by") != testMe {
		t.Errorf("say by=%q, want %q", r.Get("by"), testMe)
	}
	if r.Get("content") != "status?" {
		t.Errorf("say content=%q", r.Get("content"))
	}
}

// seedActiveChannel writes a live/channels/active/<ch>/meta.gdl with
// opener+target as members (exactly the shape cli/accept.go produces) so
// the reuse path can find it. Returns the ch-id.
func seedActiveChannel(t *testing.T, root, opener, target, topic string) string {
	t.Helper()
	chID := "ch-test-1"
	dir := filepath.Join(root, "live", "channels", "active", chID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "channel", Fields: []gdl.RecordField{
		{Key: "id", Value: chID},
		{Key: "opener", Value: opener},
		{Key: "target", Value: target},
		{Key: "topic", Value: topic},
		{Key: "intent", Value: "seed"},
		{Key: "created-at", Value: "2026-05-16T00:00:00Z"},
	}}
	if err := os.WriteFile(filepath.Join(dir, "meta.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return chID
}
