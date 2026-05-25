// Package cli — #181 confirms-detail-view tests. Covers `rufio confirms
// <thought-id>`, the detail-view counterpart to the existing write verbs
// (confirm, refute). The verb composes confirm.ReadRecords +
// retract.ReadByTarget + autopromote thresholds with no new on-disk
// event types.
//
// Tests are RED-first per the project's TDD posture and the issue's
// non-negotiables:
//
//   - quorum threshold MUST come from autopromote (not hardcoded here)
//   - privacy floor (#147) applies — other-author scope:agent invisible
//   - short-id suffix MUST resolve via the #172 retract.Resolve helper
//   - JSON shape stability (all fields always present, locked field set)
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/autopromote"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// confirmsProject is a thin alias around shortIDProject so the tests
// read locally — same identity-pinning + cwd-isolation contract.
func confirmsProject(t *testing.T, agent string) string {
	t.Helper()
	return shortIDProject(t, agent)
}

// seedConfirm appends a @confirm record into live/confirms/<id>.gdl.
func seedConfirm(t *testing.T, root, targetID, by, evidence, ts string) {
	t.Helper()
	dir := filepath.Join(root, "live", "confirms")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir confirms: %v", err)
	}
	line := "@confirm|target:" + targetID + "|by:" + by
	if evidence != "" {
		line += "|evidence:" + evidence
	}
	line += "|ts:" + ts + "\n"
	f, err := os.OpenFile(filepath.Join(dir, targetID+".gdl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open confirms: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write confirm: %v", err)
	}
}

// seedRefute appends an @refute record into live/confirms/<id>.gdl
// (confirms+refutes share one file per package design).
func seedRefute(t *testing.T, root, targetID, by, reason, ts string) {
	t.Helper()
	dir := filepath.Join(root, "live", "confirms")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir confirms: %v", err)
	}
	line := "@refute|target:" + targetID + "|by:" + by + "|reason:" + reason + "|ts:" + ts + "\n"
	f, err := os.OpenFile(filepath.Join(dir, targetID+".gdl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open confirms: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write refute: %v", err)
	}
}

// seedRetract writes live/retracted/<id>.gdl.
func seedRetract(t *testing.T, root, targetID, by, reason, ts string) {
	t.Helper()
	dir := filepath.Join(root, "live", "retracted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir retracted: %v", err)
	}
	line := "@retract|target:" + targetID + "|reason:" + reason + "|by:" + by + "|ts:" + ts + "\n"
	if err := os.WriteFile(filepath.Join(dir, targetID+".gdl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write retract: %v", err)
	}
}

// seedAutoPromote writes live/promoted/<id>.gdl with an @auto-promote
// record AND the corresponding learned/.../<obs-id>.gdlm. Matches what
// the autopromote engine writes.
func seedAutoPromote(t *testing.T, root, targetID, obsID, subject, ts string) {
	t.Helper()
	dir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir promoted: %v", err)
	}
	line := "@auto-promote|thought:" + targetID + "|observation:" + obsID + "|by:auto-promote|ts:" + ts + "\n"
	if err := os.WriteFile(filepath.Join(dir, targetID+".gdl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write auto-promote: %v", err)
	}
	// Also place the learned observation so the renderer can quote the
	// learned path. Use the same SubjectPath shape autopromote uses.
	segments := strings.Split(subject, ":")
	parts := append([]string{root, "learned"}, segments...)
	parts = append(parts, obsID+".gdlm")
	obsPath := filepath.Join(parts...)
	if err := os.MkdirAll(filepath.Dir(obsPath), 0o755); err != nil {
		t.Fatalf("mkdir learned: %v", err)
	}
	obsLine := "@observation|id:" + obsID + "|author:auto-promote|subject:" + subject +
		"|predicate:asserted|object:crowd-confirmed object|scope:fleet|confidence:1|ts:" + ts + "\n"
	if err := os.WriteFile(obsPath, []byte(obsLine), 0o644); err != nil {
		t.Fatalf("write observation: %v", err)
	}
}

// captureConfirmsStdout swaps os.Stdout for a pipe, runs fn, and returns
// the captured bytes plus fn's error. Distinct from the lower-arity
// captureStdout in dev_quiet_test.go — confirms tests need fn's error
// back.
func captureConfirmsStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	runErr := fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String(), runErr
}

// ----------------------------------------------------------------------
// PROMOTED — 4 confirms, auto-promote marker on disk.
// ----------------------------------------------------------------------

func TestConfirms_PromotedDecision_RendersFullState(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-5vcklg"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")
	seedConfirm(t, root, id, "agent-cursor", "revised after peer refute", "2026-05-21T03:18:00Z")
	seedConfirm(t, root, id, "agent-claude", "synthesis I can defend", "2026-05-21T03:19:00Z")
	seedConfirm(t, root, id, "agent-gemini", "quorum alignment", "2026-05-21T03:19:30Z")
	seedConfirm(t, root, id, "agent-codex", "implementation surface view", "2026-05-21T03:20:00Z")
	seedAutoPromote(t, root, id, "1779333639324-t60kgb", "roadmap:v1-2", "2026-05-21T03:20:39Z")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, id, output.RenderOpts{Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms: %v", err)
	}
	wants := []string{
		"Target: " + id,
		"decision",
		"Author: agent-cursor",
		"Subject: roadmap:v1-2",
		"Confirms (4)",
		"agent-cursor",
		"agent-claude",
		"agent-gemini",
		"agent-codex",
		"revised after peer refute",
		"Refutes (0)",
		"Confidence: 1",
		"Distinct confirmers: 4",
		"PROMOTED",
		"1779333639324-t60kgb.gdlm",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

// ----------------------------------------------------------------------
// PENDING — confidence ≥ 0.85 but distinct < 3.
// ----------------------------------------------------------------------

func TestConfirms_PendingDecision_StatusAndProjection(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-pp1111"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")
	seedConfirm(t, root, id, "agent-cursor", "self confirm", "2026-05-21T03:18:00Z")
	seedConfirm(t, root, id, "agent-claude", "agree", "2026-05-21T03:19:00Z")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, id, output.RenderOpts{Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms: %v", err)
	}
	if !strings.Contains(out, "PENDING") {
		t.Errorf("expected status PENDING, got:\n%s", out)
	}
	if !strings.Contains(out, "Distinct confirmers: 2") {
		t.Errorf("expected distinct confirmers 2, got:\n%s", out)
	}
	// Projection: needs +1 more distinct confirmer to clear 3.
	if !strings.Contains(out, "1 more") {
		t.Errorf("expected projection mentioning 1 more, got:\n%s", out)
	}
}

// ----------------------------------------------------------------------
// CONTESTED — refutes present, must inline refute reasons.
// ----------------------------------------------------------------------

func TestConfirms_ContestedDecision_StatusAndProjection(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-cc2222"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")
	seedConfirm(t, root, id, "agent-cursor", "self confirm", "2026-05-21T03:18:00Z")
	seedRefute(t, root, id, "agent-claude",
		"the 'observability not capability' framing collapses two distinct properties",
		"2026-05-21T03:19:00Z")
	seedRefute(t, root, id, "agent-gemini",
		"agree with Claude: 'rufio open' is a productivity enhancement",
		"2026-05-21T03:19:30Z")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, id, output.RenderOpts{Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms: %v", err)
	}
	if !strings.Contains(out, "CONTESTED") {
		t.Errorf("expected status CONTESTED, got:\n%s", out)
	}
	// Refute reasons must be inlined.
	for _, snippet := range []string{
		"observability not capability",
		"productivity enhancement",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("expected refute reason %q inlined, got:\n%s", snippet, out)
		}
	}
}

// ----------------------------------------------------------------------
// RETRACTED — terminal status; ts + by + reason rendered.
// ----------------------------------------------------------------------

func TestConfirms_RetractedDecision_StatusTerminal(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-rr3333"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")
	seedRetract(t, root, id, "agent-cursor", "walked back after Gemini's counter", "2026-05-21T04:00:00Z")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, id, output.RenderOpts{Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms: %v", err)
	}
	if !strings.Contains(out, "RETRACTED") {
		t.Errorf("expected status RETRACTED, got:\n%s", out)
	}
	if !strings.Contains(out, "2026-05-21T04:00:00Z") {
		t.Errorf("retract ts missing from output:\n%s", out)
	}
	if !strings.Contains(out, "agent-cursor") {
		t.Errorf("retract author missing from output:\n%s", out)
	}
	if !strings.Contains(out, "walked back") {
		t.Errorf("retract reason missing from output:\n%s", out)
	}
}

// ----------------------------------------------------------------------
// OPEN — no social state yet.
// ----------------------------------------------------------------------

func TestConfirms_OpenDecision_NoConfirmsYet(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-oo4444"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, id, output.RenderOpts{Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms: %v", err)
	}
	if !strings.Contains(out, "Confirms (0)") {
		t.Errorf("expected zero confirms surface, got:\n%s", out)
	}
	if !strings.Contains(out, "Refutes (0)") {
		t.Errorf("expected zero refutes surface, got:\n%s", out)
	}
	if !strings.Contains(out, "OPEN") {
		t.Errorf("expected status OPEN, got:\n%s", out)
	}
}

// ----------------------------------------------------------------------
// SHORT-ID resolver — #172 helper must do the job (no duplication).
// ----------------------------------------------------------------------

func TestConfirms_ShortIDSuffix_Resolves(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-shrt22"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")
	seedConfirm(t, root, id, "agent-cursor", "ok", "2026-05-21T03:18:00Z")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, "shrt22", output.RenderOpts{Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms short id: %v", err)
	}
	if !strings.Contains(out, id) {
		t.Errorf("short-id should resolve to canonical %q, got:\n%s", id, out)
	}
}

// ----------------------------------------------------------------------
// AMBIGUOUS short-id — disambiguation surfaces every candidate.
// ----------------------------------------------------------------------

func TestConfirms_AmbiguousShortID_ListsCandidates(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	// Two records with the same trailing suffix.
	seedThought(t, root, "agent-cursor", "1779111111111-ambamb", "decision", "svc:auth", "fleet")
	seedThought(t, root, "agent-claude", "1779222222222-ambamb", "hypothesis", "svc:auth", "fleet")

	err := runConfirms(root, "ambamb", output.RenderOpts{Quiet: true, NoColor: true})
	if err == nil {
		t.Fatal("ambiguous short id: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "1779111111111-ambamb") || !strings.Contains(msg, "1779222222222-ambamb") {
		t.Errorf("ambiguous error missing one or both ids: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "ambiguous") {
		t.Errorf("ambiguous error should name the condition: %s", msg)
	}
}

// ----------------------------------------------------------------------
// UNKNOWN id — clear error.
// ----------------------------------------------------------------------

func TestConfirms_UnknownID_ErrorsClearly(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	err := runConfirms(root, "xxxxxx", output.RenderOpts{Quiet: true, NoColor: true})
	if err == nil {
		t.Fatal("unknown id: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no such record") {
		t.Errorf("expected canonical 'no such record' wording, got: %s", err.Error())
	}
}

// ----------------------------------------------------------------------
// JSON shape stability — all locked fields present.
// ----------------------------------------------------------------------

func TestConfirms_JSON_ShapeStability(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-jjj555"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")
	seedConfirm(t, root, id, "agent-cursor", "self", "2026-05-21T03:18:00Z")
	seedRefute(t, root, id, "agent-claude", "disagree", "2026-05-21T03:19:00Z")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, id, output.RenderOpts{JSON: true, Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms json: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("json parse: %v\n%s", err, out)
	}

	// Top-level locked field set.
	wantKeys := []string{"_type", "_version", "target", "confirms", "refutes", "retract", "quorum", "promoted"}
	for _, k := range wantKeys {
		if _, ok := payload[k]; !ok {
			t.Errorf("missing top-level key %q in JSON: %s", k, out)
		}
	}
	if payload["_type"] != "confirms" {
		t.Errorf("_type should be 'confirms', got %v", payload["_type"])
	}
	if payload["_version"] != "1" {
		t.Errorf("_version should be '1', got %v", payload["_version"])
	}

	// Target shape.
	target, ok := payload["target"].(map[string]interface{})
	if !ok {
		t.Fatalf("target not an object: %v", payload["target"])
	}
	for _, k := range []string{"id", "type", "author", "subject", "scope", "content"} {
		if _, ok := target[k]; !ok {
			t.Errorf("target missing key %q", k)
		}
	}

	// Quorum shape with locked field names.
	q, ok := payload["quorum"].(map[string]interface{})
	if !ok {
		t.Fatalf("quorum not an object: %v", payload["quorum"])
	}
	for _, k := range []string{"confidence", "distinct_confirmers", "threshold_distinct", "threshold_confidence", "status"} {
		if _, ok := q[k]; !ok {
			t.Errorf("quorum missing key %q", k)
		}
	}

	// Optional payloads always present (null or value) — never missing keys.
	if _, ok := payload["retract"]; !ok {
		t.Error("retract key must always be present (null when absent)")
	}
	if _, ok := payload["promoted"]; !ok {
		t.Error("promoted key must always be present (null when absent)")
	}
}

// ----------------------------------------------------------------------
// Quorum thresholds locked to autopromote engine constants.
// ----------------------------------------------------------------------

func TestConfirms_QuorumMath_MatchesAutoPromoteEngine(t *testing.T) {
	root := confirmsProject(t, "agent-cursor")
	id := "1779333500364-qm6666"
	seedThought(t, root, "agent-cursor", id, "decision", "roadmap:v1-2", "fleet")
	seedConfirm(t, root, id, "agent-cursor", "self", "2026-05-21T03:18:00Z")

	out, err := captureConfirmsStdout(t, func() error {
		return runConfirms(root, id, output.RenderOpts{JSON: true, Quiet: true, NoColor: true})
	})
	if err != nil {
		t.Fatalf("runConfirms json: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("json parse: %v\n%s", err, out)
	}
	q := payload["quorum"].(map[string]interface{})

	gotDistinct, ok := q["threshold_distinct"].(float64)
	if !ok {
		t.Fatalf("threshold_distinct not a number: %v", q["threshold_distinct"])
	}
	if int(gotDistinct) != autopromote.MinDistinctConfirmers {
		t.Errorf("threshold_distinct = %d, want autopromote.MinDistinctConfirmers = %d",
			int(gotDistinct), autopromote.MinDistinctConfirmers)
	}
	gotConf, ok := q["threshold_confidence"].(float64)
	if !ok {
		t.Fatalf("threshold_confidence not a number: %v", q["threshold_confidence"])
	}
	if gotConf != autopromote.MinConfidence {
		t.Errorf("threshold_confidence = %v, want autopromote.MinConfidence = %v",
			gotConf, autopromote.MinConfidence)
	}
}

// ----------------------------------------------------------------------
// Privacy floor (#147) — non-author scope:agent invisible.
// ----------------------------------------------------------------------

func TestConfirms_PrivacyFloor_NonAuthorScopeAgent(t *testing.T) {
	root := confirmsProject(t, "bob")
	id := "1779333500364-pvt777"
	// alice's private (scope:agent) thought — bob should not be able to
	// query it. Existence must NOT be revealed.
	seedThought(t, root, "alice", id, "hypothesis", "agent:alice", "agent")
	seedConfirm(t, root, id, "alice", "self", "2026-05-21T03:18:00Z")

	err := runConfirms(root, id, output.RenderOpts{Quiet: true, NoColor: true})
	if err == nil {
		t.Fatal("bob -> alice's scope:agent: want error")
	}
	// Per privacy floor: NoSuchThoughtError, not Private*Authz* (the
	// latter would itself confirm existence).
	if !strings.Contains(err.Error(), "no such record") {
		t.Errorf("privacy leak: expected 'no such record', got: %s", err.Error())
	}
}
