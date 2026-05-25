// project_test.go — PR-G0: table-driven unit tests for the pure
// substrate→display projection layer (project.go / project_walk.go).
//
// These tests ARE the deliverable's value (handoff "Tests"): they prove
// the headless shape-reconciliation produces structs FIELD-IDENTICAL to
// fixtures.go's, so the LATER G1 slice can swap fixtures→projection with
// zero UI/struct change. Every test:
//
//   - is deterministic — `now` is always an injected fixed time.Time;
//     nothing reads the wall clock (handoff hard constraint);
//   - writes synthetic on-disk records via the REAL lib writers
//     (thought.Write / observation.Write / confirm.Append /
//     attention.Write) or minimal hand-written records EXACTLY matching
//     the libs' parse format (the @route inbox file, whose writer is an
//     unexported routing internal) under t.TempDir();
//   - asserts EXACT struct equality vs hand-built expected display
//     structs (reflect.DeepEqual against fixtures.go's types).
package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/observation"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// fixedNow is the deterministic clock injected everywhere a `now` is
// needed. Chosen 2026-05-15 14:10:00Z so the customer:5821-arc tss
// (14:02:xx) land minutes-ago for the tsToAgo bucket tests.
var fixedNow = time.Date(2026, 5, 15, 14, 10, 0, 0, time.UTC)

// ── tsToClock ─────────────────────────────────────────────────────────

func TestTsToClock(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "rfc3339nano UTC → HH:MM:SS",
			// versioning.NowISO format: RFC3339Nano UTC (versioning.go:78).
			in:   "2026-05-15T14:02:46.123456789Z",
			want: "14:02:46",
		},
		{
			name: "rfc3339 no fractional seconds still parses",
			in:   "2026-05-15T14:02:09Z",
			want: "14:02:09",
		},
		{
			name: "offset zone normalised to UTC clock",
			// 14:02:46+02:00 == 12:02:46Z — display is the substrate's
			// own UTC clock (versioning writes UTC).
			in:   "2026-05-15T14:02:46+02:00",
			want: "12:02:46",
		},
		{
			name: "parse failure → empty (row omits the timestamp slot)",
			in:   "not-a-timestamp",
			want: "",
		},
		{
			name: "empty input → empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tsToClock(tc.in); got != tc.want {
				t.Fatalf("tsToClock(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ── tsToAgo (boundary cases: secs/min/hour/day) ───────────────────────

func TestTsToAgo(t *testing.T) {
	base := fixedNow
	at := func(d time.Duration) string {
		return base.Add(-d).Format(time.RFC3339Nano)
	}
	tests := []struct {
		name string
		ts   string
		want string
	}{
		{"instant → 0s", at(0), "0s"},
		{"sub-minute → Ns", at(5 * time.Second), "5s"},
		{"59s still seconds", at(59 * time.Second), "59s"},
		{"exactly 60s → 1m (minute bucket boundary)", at(60 * time.Second), "1m"},
		{"2m", at(2 * time.Minute), "2m"},
		{"59m still minutes", at(59 * time.Minute), "59m"},
		{"exactly 60m → 1h (hour bucket boundary)", at(60 * time.Minute), "1h"},
		{"2h", at(2 * time.Hour), "2h"},
		{"23h still hours", at(23 * time.Hour), "23h"},
		{"exactly 24h → 1d (day bucket boundary)", at(24 * time.Hour), "1d"},
		{"3d", at(72 * time.Hour), "3d"},
		{"parse failure → empty", "garbage", ""},
		{"future ts → empty (no negative ago)", base.Add(time.Hour).Format(time.RFC3339Nano), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tsToAgo(tc.ts, base); got != tc.want {
				t.Fatalf("tsToAgo(%q) = %q, want %q", tc.ts, got, tc.want)
			}
		})
	}
}

// ── projectThread ─────────────────────────────────────────────────────
//
// Helpers build stream.Event values EXACTLY as the stream lib projects a
// parsed record (stream.go:140-152): Type from @type, the fixed columns
// from their fields, Raw = gdl.RenderLine(record). Going through
// thought.BuildThoughtRecord / observation.BuildObservationRecord (the
// REAL builders) guarantees the Raw line is byte-identical to what the
// live watcher would emit.

func eventFromRecord(r gdl.Record) stream.Event {
	return stream.Event{
		Type:      r.Type,
		TS:        r.Get("ts"),
		Author:    r.Get("author"),
		Subject:   r.Get("subject"),
		Predicate: r.Get("predicate"),
		Object:    r.Get("object"),
		Content:   r.Get("content"),
		Scope:     r.Get("scope"),
		Path:      "live/outbox/" + r.Get("author") + "/" + r.Get("id") + ".gdl",
		Raw:       gdl.RenderLine(r),
	}
}

func thoughtEvent(id, author, typ, subject, content, ts, parent string) stream.Event {
	return eventFromRecord(thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: author, Type: typ, Subject: subject,
		Content: content, Scope: "fleet", TS: ts, TTL: 0, Parent: parent,
	}))
}

func observationEvent(id, author, subject, predicate, object, ts, decision string) stream.Event {
	r := observation.BuildObservationRecord(observation.ObservationInput{
		ID: id, Author: author, Subject: subject, Predicate: predicate,
		Object: object, Scope: "fleet", Confidence: 1.0, TS: ts,
	})
	// Observations thread under a plan via a `decision:` link
	// (data-mapping §1 :116). BuildObservationRecord has no decision
	// field, so append it the way the substrate would carry the link.
	if decision != "" {
		r.Fields = append(r.Fields, gdl.RecordField{Key: "decision", Value: decision})
	}
	return eventFromRecord(r)
}

func TestProjectThread(t *testing.T) {
	const op = "operator"

	// Canonical-ish customer:5821 arc (mirrors SubstrateThread's rhythm,
	// fixtures.go:110-154) but sourced from real builders.
	opEv := thoughtEvent("1747-op0", op, "focus", "customer:5821",
		"investigate customer:5821 churn risk — rufio fleet",
		"2026-05-15T14:02:09Z", "")
	hypEv := thoughtEvent("1747-h01", "claude-code", "hypothesis", "customer:5821",
		"14-day silence, customer mentioned cancel — churn signals",
		"2026-05-15T14:02:11Z", "")
	obsEv := observationEvent("1747-o01", "cursor", "customer:5821",
		"prefers", "email", "2026-05-15T14:02:12Z", "1747-h01")
	decEv := thoughtEvent("1747-d29", "claude-code", "decision", "customer:5821",
		"decision: offer downgrade, not churn-save discount",
		"2026-05-15T14:02:46Z", "")

	events := []stream.Event{opEv, hypEv, obsEv, decEv}
	tallies := map[string]confirm.Tally{
		// confirm.ReadAll returns sorted+deduped (confirm.go:120-121).
		"1747-d29": {Confirms: []string{"cursor", "data-analyst"}},
	}

	got := projectThread(events, tallies, op, fixedNow)

	want := []ThreadMsg{
		{
			Who: op, Role: "focus", Time: "14:02:09", Kind: kindOp,
			Text: "investigate customer:5821 churn risk — rufio fleet",
		},
		{
			Who: "claude-code", Role: "hypothesis", Time: "14:02:11", Kind: kindPlan,
			Text: "14-day silence, customer mentioned cancel — churn signals",
		},
		{
			Who: "cursor", Role: "observation", Time: "14:02:12", Kind: kindReply,
			Text: "email", // observation Content is empty; object surfaces below — see note
		},
		{
			Who: "claude-code", Role: roleDecision, Time: "14:02:46", Kind: kindPlan,
			Text:   "decision: offer downgrade, not churn-save discount",
			Quorum: &Quorum{Yes: []string{"cursor", "data-analyst"}, Total: 0},
			Last:   true,
		},
	}
	// NOTE: @observation has no `content` field (observation.go:101-116);
	// stream.Event.Content is therefore "" for an observation row. The
	// v8 ThreadMsg.Text for a reply sourced from an @observation is a
	// documented SHAPE GAP (see PR body): the substrate observation
	// carries subject/predicate/object, NOT a free-text body. G0 does
	// NOT invent a body; Text is the empty Content. Fix the expectation
	// to that truth rather than fabricate data.
	want[2].Text = ""

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projectThread mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestProjectThread_KindDerivation(t *testing.T) {
	const op = "operator"
	tests := []struct {
		name     string
		events   []stream.Event
		wantKind []string
		wantRole []string
	}{
		{
			name: "operator-authored thought → kindOp",
			events: []stream.Event{
				thoughtEvent("1-aaa111", op, "focus", "x:1", "go", "2026-05-15T14:00:00Z", ""),
			},
			wantKind: []string{kindOp},
			wantRole: []string{"focus"},
		},
		{
			name: "non-decision root thought, no parent → kindPlan (else default)",
			events: []stream.Event{
				thoughtEvent("1-bbb222", "claude-code", "hypothesis", "x:1", "h", "2026-05-15T14:00:00Z", ""),
			},
			wantKind: []string{kindPlan},
			wantRole: []string{"hypothesis"},
		},
		{
			name: "decision thought → kindPlan, Role=decision",
			events: []stream.Event{
				thoughtEvent("1-ccc333", "claude-code", "decision", "x:1", "d", "2026-05-15T14:00:00Z", ""),
			},
			wantKind: []string{kindPlan},
			wantRole: []string{roleDecision},
		},
		{
			name: "thought with parent → kindReply (one nesting level)",
			events: []stream.Event{
				thoughtEvent("1-ddd444", "claude-code", "hypothesis", "x:1", "root", "2026-05-15T14:00:00Z", ""),
				thoughtEvent("1-eee555", "cursor", "observation", "x:1", "child", "2026-05-15T14:00:01Z", "1-ddd444"),
			},
			wantKind: []string{kindPlan, kindReply},
			wantRole: []string{"hypothesis", "observation"},
		},
		{
			name: "observation linking to a visible decision via decision: → kindReply",
			events: []stream.Event{
				thoughtEvent("1-fff666", "claude-code", "decision", "x:1", "dec", "2026-05-15T14:00:00Z", ""),
				observationEvent("1-ggg777", "cursor", "x:1", "prefers", "email", "2026-05-15T14:00:01Z", "1-fff666"),
			},
			wantKind: []string{kindPlan, kindReply},
			wantRole: []string{roleDecision, "observation"},
		},
		{
			name: "child whose parent is NOT a visible plan → falls through to plan default",
			events: []stream.Event{
				// parent points at an id not present in the feed → not a
				// visible plan, so the else→plan default applies (NOT
				// reply): a dangling link can't anchor a reply
				// (data-mapping §1 :116 requires a VISIBLE plan).
				thoughtEvent("1-hhh888", "cursor", "observation", "x:1", "orphan", "2026-05-15T14:00:00Z", "9-missing"),
			},
			wantKind: []string{kindPlan},
			wantRole: []string{"observation"},
		},
		{
			name: "reply whose parent is an OPERATOR row → still a reply if parent is a plan? operator rows are NOT plans",
			events: []stream.Event{
				thoughtEvent("1-iii999", op, "focus", "x:1", "op opens", "2026-05-15T14:00:00Z", ""),
				thoughtEvent("1-jjj000", "claude-code", "hypothesis", "x:1", "under op", "2026-05-15T14:00:01Z", "1-iii999"),
			},
			// operator rows are kindOp and explicitly excluded from
			// planIDs (project.go) — a child linking to the op row has
			// no VISIBLE plan parent, so it takes the else→plan default.
			wantKind: []string{kindOp, kindPlan},
			wantRole: []string{"focus", "hypothesis"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := projectThread(tc.events, nil, op, fixedNow)
			if len(got) != len(tc.wantKind) {
				t.Fatalf("len=%d, want %d (%#v)", len(got), len(tc.wantKind), got)
			}
			for i := range got {
				if got[i].Kind != tc.wantKind[i] {
					t.Errorf("row %d Kind=%q want %q", i, got[i].Kind, tc.wantKind[i])
				}
				if got[i].Role != tc.wantRole[i] {
					t.Errorf("row %d Role=%q want %q", i, got[i].Role, tc.wantRole[i])
				}
			}
			// Last is always on the freshest (final) row.
			if len(got) > 0 && !got[len(got)-1].Last {
				t.Errorf("freshest row missing Last=true")
			}
		})
	}
}

func TestProjectThread_Quorum(t *testing.T) {
	const op = "operator"
	dec := thoughtEvent("1-dec001", "claude-code", "decision", "x:1", "d", "2026-05-15T14:00:00Z", "")

	t.Run("decision with a tally → Quorum.Yes sorted+deduped, Total=0 (OPEN-2 deferred)", func(t *testing.T) {
		got := projectThread([]stream.Event{dec},
			map[string]confirm.Tally{"1-dec001": {Confirms: []string{"data-analyst", "cursor", "cursor"}}},
			op, fixedNow)
		want := &Quorum{Yes: []string{"cursor", "data-analyst"}, Total: 0}
		if !reflect.DeepEqual(got[0].Quorum, want) {
			t.Fatalf("Quorum=%#v want %#v", got[0].Quorum, want)
		}
		if got[0].Quorum.Total != 0 {
			t.Fatalf("OPEN-2 violation: Total=%d, must be 0 (denominator is a G1 render-time call)", got[0].Quorum.Total)
		}
	})

	t.Run("decision with NO tally → Quorum nil", func(t *testing.T) {
		got := projectThread([]stream.Event{dec}, nil, op, fixedNow)
		if got[0].Quorum != nil {
			t.Fatalf("Quorum=%#v want nil (no tally for this id)", got[0].Quorum)
		}
	})

	t.Run("decision with empty tally → Quorum.Yes nil, Total 0", func(t *testing.T) {
		got := projectThread([]stream.Event{dec},
			map[string]confirm.Tally{"1-dec001": {}}, op, fixedNow)
		want := &Quorum{Yes: nil, Total: 0}
		if !reflect.DeepEqual(got[0].Quorum, want) {
			t.Fatalf("Quorum=%#v want %#v", got[0].Quorum, want)
		}
	})

	// #131 (2026-05-18): SUPERSEDES the old "non-decision row never gets
	// a Quorum even if a tally keyed by its id exists" assertion. The
	// auto-promote engine is type-agnostic, so the projection follows it:
	// a NON-decision thought WITH a tally now gets a Quorum exactly like
	// a decision does (Yes sorted+deduped, Total left 0 — the OPEN-2
	// denominator is still the render-time call). The decision-only
	// scoping was a projection choice, never an engine constraint.
	t.Run("non-decision row WITH a tally → Quorum.Yes sorted+deduped, Total=0 (#131: engine is type-agnostic)", func(t *testing.T) {
		hyp := thoughtEvent("1-hyp001", "claude-code", "hypothesis", "x:1", "h", "2026-05-15T14:00:00Z", "")
		got := projectThread([]stream.Event{hyp},
			map[string]confirm.Tally{"1-hyp001": {Confirms: []string{"data-analyst", "cursor", "cursor"}}}, op, fixedNow)
		want := &Quorum{Yes: []string{"cursor", "data-analyst"}, Total: 0}
		if !reflect.DeepEqual(got[0].Quorum, want) {
			t.Fatalf("non-decision Quorum=%#v want %#v", got[0].Quorum, want)
		}
	})

	// The tally lookup is the real gate (#131): a non-decision thought
	// with NO tally still gets NO Quorum — the no-`0/3`-clutter property
	// holds at the projection layer too (loadConfirmTallies only
	// registers ≥1-confirm ids, so the live path never passes an
	// unconfirmed id's tally here).
	t.Run("non-decision row with NO tally → Quorum nil (#131: tally lookup is the guard)", func(t *testing.T) {
		hyp := thoughtEvent("1-hyp002", "claude-code", "hypothesis", "x:1", "h", "2026-05-15T14:00:00Z", "")
		got := projectThread([]stream.Event{hyp}, nil, op, fixedNow)
		if got[0].Quorum != nil {
			t.Fatalf("unconfirmed non-decision Quorum=%#v want nil", got[0].Quorum)
		}
	})
}

func TestProjectThread_Empty(t *testing.T) {
	got := projectThread(nil, nil, "operator", fixedNow)
	if len(got) != 0 {
		t.Fatalf("empty feed → %#v, want empty", got)
	}
}

// ── walkLearned ───────────────────────────────────────────────────────

func TestWalkLearned(t *testing.T) {
	t.Run("missing learned/ → nil,nil (empty knowledge base is not an error)", func(t *testing.T) {
		got, err := walkLearned(t.TempDir(), fixedNow)
		if err != nil || got != nil {
			t.Fatalf("got (%#v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("nested subjects, real observation.Write, deterministic order + injected Ago", func(t *testing.T) {
		root := t.TempDir()
		// observation.Write maps subject → learned/<seg>/<seg>/<id>.gdlm
		// (observation.go:64-78,118-134). Use the REAL writer so the
		// on-disk format is exactly the lib's.
		writeObs := func(id, author, subject, pred, obj, ts string) {
			t.Helper()
			rec := observation.BuildObservationRecord(observation.ObservationInput{
				ID: id, Author: author, Subject: subject, Predicate: pred,
				Object: obj, Scope: "fleet", Confidence: 1.0, TS: ts,
			})
			if err := observation.Write(root, subject, id, rec); err != nil {
				t.Fatalf("observation.Write: %v", err)
			}
		}
		// Two flat + one NESTED subject (customer:5821:contact →
		// learned/customer/5821/contact/) to exercise the recursive walk.
		writeObs("ts2-bbb", "data-analyst", "customer:5821", "usage-trend", "contraction",
			fixedNow.Add(-1*time.Minute).Format(time.RFC3339Nano))
		writeObs("ts1-aaa", "cursor", "customer:5821", "prefers", "email",
			fixedNow.Add(-2*time.Minute).Format(time.RFC3339Nano))
		writeObs("ts3-ccc", "cursor", "customer:5821:contact", "channel", "email",
			fixedNow.Add(-2*time.Hour).Format(time.RFC3339Nano))

		got, err := walkLearned(root, fixedNow)
		if err != nil {
			t.Fatalf("walkLearned: %v", err)
		}
		// Deterministic sort = (Subject, ts, path). "customer:5821" <
		// "customer:5821:contact"; within customer:5821, ts1 (2m) < ts2
		// (1m) by raw-ts string.
		want := []MemoryEntry{
			{Subject: "customer:5821", Predicate: "prefers", Object: "email", Author: "cursor", Ago: "2m"},
			{Subject: "customer:5821", Predicate: "usage-trend", Object: "contraction", Author: "data-analyst", Ago: "1m"},
			{Subject: "customer:5821:contact", Predicate: "channel", Object: "email", Author: "cursor", Ago: "2h"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("walkLearned mismatch:\n got=%#v\nwant=%#v", got, want)
		}
	})

	t.Run("empty learned/ dir (exists, no files) → empty slice", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "learned"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := walkLearned(root, fixedNow)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %#v, want empty", got)
		}
	})

	t.Run("malformed .gdlm file is skipped, valid sibling still projected", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "learned", "customer", "5821")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Not a GDL line (no leading @) → ParseDocument errors → file skipped.
		if err := os.WriteFile(filepath.Join(dir, "bad.gdlm"), []byte("garbage not gdl\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		good := observation.BuildObservationRecord(observation.ObservationInput{
			ID: "good1", Author: "cursor", Subject: "customer:5821",
			Predicate: "tier", Object: "standard", Scope: "fleet",
			Confidence: 1.0, TS: fixedNow.Add(-2 * time.Hour).Format(time.RFC3339Nano),
		})
		if err := os.WriteFile(filepath.Join(dir, "good.gdlm"), []byte(gdl.RenderLine(good)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := walkLearned(root, fixedNow)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		want := []MemoryEntry{
			{Subject: "customer:5821", Predicate: "tier", Object: "standard", Author: "cursor", Ago: "2h"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	})
}

// ── deriveMeshEdgesLive (outbox ∩ inbox, @route from) ─────────────────

// writeOutbox writes a minimal @thought outbox file via the real
// thought.Write (thought.go:269-286) so the on-disk layout is the lib's.
func writeOutbox(t *testing.T, root, author, id string) {
	t.Helper()
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: author, Type: "hypothesis", Subject: "x:1",
		Content: "c", Scope: "fleet", TS: "2026-05-15T14:00:00Z", TTL: 0,
	})
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write: %v", err)
	}
}

// writeInbox writes live/inbox/<recipient>/<id>.gdl in EXACTLY the
// format routing.deliverToInbox produces (routing.go:446-457):
// the thought line + an @route|to:<recipient>|from:<author>|ts:… line.
// deliverToInbox is unexported, so this is the documented "minimal
// hand-written record matching the lib's parse format" case (handoff
// "Tests"); the @route record is built via gdl so the line is canonical.
func writeInbox(t *testing.T, root, recipient, id, from string) {
	t.Helper()
	dir := filepath.Join(root, "live", "inbox", recipient)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	thoughtRec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: from, Type: "hypothesis", Subject: "x:1",
		Content: "c", Scope: "fleet", TS: "2026-05-15T14:00:00Z", TTL: 0,
	})
	routeRec := gdl.Record{Type: "route", Fields: []gdl.RecordField{
		{Key: "to", Value: recipient},
		{Key: "from", Value: from},
		{Key: "ts", Value: "2026-05-15T14:00:01Z"},
	}}
	contents := gdl.RenderLine(thoughtRec) + "\n" + gdl.RenderLine(routeRec) + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveMeshEdgesLive(t *testing.T) {
	t.Run("missing live dirs → no edges", func(t *testing.T) {
		got, err := deriveMeshEdgesLive(t.TempDir())
		if err != nil || got != nil {
			t.Fatalf("got (%#v, %v) want (nil, nil)", got, err)
		}
	})

	t.Run("id in outbox AND inbox → A–B edge from @route from", func(t *testing.T) {
		root := t.TempDir()
		writeOutbox(t, root, "claude-code", "1-aaa111")
		writeInbox(t, root, "cursor", "1-aaa111", "claude-code")
		got, err := deriveMeshEdgesLive(root)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		want := [][2]string{{"claude-code", "cursor"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	})

	t.Run("id ONLY in outbox (never delivered) → NO edge", func(t *testing.T) {
		root := t.TempDir()
		writeOutbox(t, root, "claude-code", "1-bbb222")
		got, err := deriveMeshEdgesLive(root)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if got != nil {
			t.Fatalf("got=%#v want nil (presence in BOTH required, data-mapping §0 :39-40)", got)
		}
	})

	t.Run("id ONLY in inbox (no matching outbox) → NO edge", func(t *testing.T) {
		root := t.TempDir()
		writeInbox(t, root, "cursor", "1-ccc333", "claude-code")
		// No outbox file for 1-ccc333 → not a derived edge.
		got, err := deriveMeshEdgesLive(root)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if got != nil {
			t.Fatalf("got=%#v want nil", got)
		}
	})

	t.Run("bidirectional + duplicate deliveries dedupe to one ordered edge", func(t *testing.T) {
		root := t.TempDir()
		// claude-code → cursor (id1) AND cursor → claude-code (id2):
		// both yield the SAME undirected edge → must appear once.
		writeOutbox(t, root, "claude-code", "1-id00001")
		writeInbox(t, root, "cursor", "1-id00001", "claude-code")
		writeOutbox(t, root, "cursor", "1-id00002")
		writeInbox(t, root, "claude-code", "1-id00002", "cursor")
		// A third delivery on the same pair (id3) → still one edge.
		writeOutbox(t, root, "claude-code", "1-id00003")
		writeInbox(t, root, "cursor", "1-id00003", "claude-code")
		got, err := deriveMeshEdgesLive(root)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		want := [][2]string{{"claude-code", "cursor"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	})

	t.Run("self-delivery (A→A) drops the edge; multi-pair deterministic order", func(t *testing.T) {
		root := t.TempDir()
		// self: claude-code delivers id1 to its own inbox → no edge.
		writeOutbox(t, root, "claude-code", "1-self001")
		writeInbox(t, root, "claude-code", "1-self001", "claude-code")
		// two real cross edges, written out of lexical order to prove
		// the output is sorted (data-analyst<cursor<… ; edges sorted).
		writeOutbox(t, root, "cursor", "1-pair001")
		writeInbox(t, root, "data-analyst", "1-pair001", "cursor")
		writeOutbox(t, root, "claude-code", "1-pair002")
		writeInbox(t, root, "data-analyst", "1-pair002", "claude-code")
		got, err := deriveMeshEdgesLive(root)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		// Sorted: ("claude-code","data-analyst") < ("cursor","data-analyst").
		want := [][2]string{
			{"claude-code", "data-analyst"},
			{"cursor", "data-analyst"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	})
}

// ── projectMeshNodes (deterministic 9×36 placement, N=1,4,7) ──────────

func attns(ids ...string) []attention.Attention {
	out := make([]attention.Attention, len(ids))
	for i, id := range ids {
		out[i] = attention.Attention{Agent: id, Intent: "x", Entities: []string{"x:1"}, TS: "2026-05-15T14:00:00Z"}
	}
	return out
}

func TestProjectMeshNodes(t *testing.T) {
	t.Run("empty attention set → no nodes (OPEN-4: operator NOT synthesized)", func(t *testing.T) {
		if got := projectMeshNodes(nil); got != nil {
			t.Fatalf("got=%#v want nil", got)
		}
	})

	t.Run("N=1 centre-placed spoke (no synthesized hub)", func(t *testing.T) {
		got := projectMeshNodes(attns("claude-code"))
		want := []MeshNode{{ID: "claude-code", R: 4, C: 18, Glyph: "●"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%#v want=%#v", got, want)
		}
	})

	// For N=4 and N=7 we assert the STRUCTURAL invariants that matter
	// for the rail (handoff: "deterministic layout fn for arbitrary N")
	// rather than pinning exact trig coords (the precise circle is an
	// implementation detail; determinism + in-grid + no-overlap +
	// sorted-input order are the contract).
	for _, n := range []int{4, 7} {
		t.Run("N="+strconv.Itoa(n)+" determinism + in-grid + unique cells + sorted order + all spokes", func(t *testing.T) {
			ids := make([]string, n)
			for i := 0; i < n; i++ {
				ids[i] = "agent-" + string(rune('a'+i))
			}
			in := attns(ids...)

			got := projectMeshNodes(in)
			if len(got) != n {
				t.Fatalf("len=%d want %d", len(got), n)
			}

			// 1. Determinism: a second call (and an UNSORTED input
			//    permutation) yields byte-identical output.
			again := projectMeshNodes(in)
			if !reflect.DeepEqual(got, again) {
				t.Fatalf("not deterministic:\n a=%#v\n b=%#v", got, again)
			}
			shuffled := attns(append([]string{ids[n-1]}, ids[:n-1]...)...)
			if !reflect.DeepEqual(projectMeshNodes(shuffled), got) {
				t.Fatalf("input order leaked into output (must sort by agent)")
			}

			seen := map[[2]int]bool{}
			for i, node := range got {
				// 2. Sorted-input order: ids were already sorted; output
				//    must follow it.
				if node.ID != ids[i] {
					t.Fatalf("node %d ID=%q want %q (sorted-input order)", i, node.ID, ids[i])
				}
				// 3. In-grid (MeshNode coord space, fixtures.go:324-327).
				if node.R < 0 || node.R > 8 || node.C < 0 || node.C > 35 {
					t.Fatalf("node %q (%d,%d) out of 9×36 grid", node.ID, node.R, node.C)
				}
				// 4. Unique cells (no two nodes stacked).
				key := [2]int{node.R, node.C}
				if seen[key] {
					t.Fatalf("node %q collides at (%d,%d)", node.ID, node.R, node.C)
				}
				seen[key] = true
				// 5. All spokes (no synthesized "◉" hub — OPEN-4).
				if node.Glyph != "●" {
					t.Fatalf("node %q Glyph=%q want ● (hub is OPEN-4/G2, not G0)", node.ID, node.Glyph)
				}
			}
		})
	}
}

// ── projectLineage (exact cli/lineage.go render format) ───────────────

func TestProjectLineage(t *testing.T) {
	root := t.TempDir()

	// A decision lives in live/outbox/<author>/<id>.gdl with a sibling
	// @context-bundle in the SAME file (thought.go:191-200,261-286). Use
	// the real builders so LookupDecision parses it exactly.
	decID := "1747-d29"
	author := "claude-code"
	thoughtRec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: decID, Author: author, Type: "decision", Subject: "customer:5821",
		Content: "offer downgrade, not churn-save discount", Scope: "fleet",
		TS: "2026-05-15T14:02:46Z", TTL: 0,
	})
	bundleRec := thought.BuildContextBundle(decID, []string{"deadbeefcafe0001"})
	if err := thought.Write(root, author, decID, []gdl.Record{thoughtRec, bundleRec}); err != nil {
		t.Fatalf("thought.Write decision: %v", err)
	}

	// Resolve the bundle sha against .rufio/refs/<path>.gdl (the @ref
	// format ResolveBundleRefs walks, lineage.go:230-250).
	refDir := filepath.Join(root, ".rufio", "refs", "given")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	refRec := gdl.Record{Type: "ref", Fields: []gdl.RecordField{
		{Key: "path", Value: "given/refund-policy.md"},
		{Key: "version", Value: "1"},
		{Key: "sha256", Value: "deadbeefcafe0001"},
		{Key: "stage", Value: "live"},
		{Key: "ts", Value: "2026-05-15T13:00:00Z"},
		{Key: "author", Value: "operator"},
	}}
	if err := os.WriteFile(filepath.Join(refDir, "refund-policy.md.gdl"), []byte(gdl.RenderLine(refRec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two @reason steps under live/reasoning/<author>/<decID>/ (the
	// format WalkReasoning parses, lineage.go:306-318). Root + child so
	// the chain order is deterministic.
	reasonDir := filepath.Join(root, "live", "reasoning", author, decID)
	if err := os.MkdirAll(reasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeReason := func(file, id, parent, content, ts string) {
		r := gdl.Record{Type: "reason", Fields: []gdl.RecordField{
			{Key: "id", Value: id},
			{Key: "author", Value: author},
			{Key: "content", Value: content},
			{Key: "ts", Value: ts},
			{Key: "parent", Value: parent},
			{Key: "decision", Value: decID},
		}}
		if err := os.WriteFile(filepath.Join(reasonDir, file), []byte(gdl.RenderLine(r)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeReason("r1.gdl", "r1", "", "customer requested downgrade, not cancellation", "2026-05-15T14:02:40Z")
	writeReason("r2.gdl", "r2", "r1", "policy: downgrade offers < $500 auto-approve", "2026-05-15T14:02:41Z")

	got, err := projectLineage(root, decID)
	if err != nil {
		t.Fatalf("projectLineage: %v", err)
	}

	want := &DecisionLineage{
		ID:        "1747-d29",
		Author:    "claude-code",
		Subject:   "customer:5821",
		Statement: "offer downgrade, not churn-save discount",
		Time:      "14:02:46",
		// EXACT cli/lineage.go:118 format: "<path>@v<ver> (sha: <8>)".
		Bundle: []string{"given/refund-policy.md@v1 (sha: deadbeef)"},
		// @reason Content in audit order (root then child).
		Chain: []string{
			"customer requested downgrade, not cancellation",
			"policy: downgrade offers < $500 auto-approve",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projectLineage mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestProjectLineage_UnresolvedBundle(t *testing.T) {
	root := t.TempDir()
	decID := "1-unr001"
	author := "cursor"
	thoughtRec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: decID, Author: author, Type: "decision", Subject: "x:1",
		Content: "d", Scope: "fleet", TS: "2026-05-15T14:00:00Z", TTL: 0,
	})
	// sha with no matching @ref → unresolved.
	bundleRec := thought.BuildContextBundle(decID, []string{"00unknownsha00"})
	if err := thought.Write(root, author, decID, []gdl.Record{thoughtRec, bundleRec}); err != nil {
		t.Fatal(err)
	}
	got, err := projectLineage(root, decID)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	// EXACT cli/lineage.go:120 unresolved format.
	want := []string{"(unknown sha: 00unknownsha00)"}
	if !reflect.DeepEqual(got.Bundle, want) {
		t.Fatalf("Bundle=%#v want %#v", got.Bundle, want)
	}
}

func TestProjectLineage_NotFound(t *testing.T) {
	// Missing decision → error propagates (G0 does not swallow, matching
	// cli/lineage.go which lets it reach HandleError).
	_, err := projectLineage(t.TempDir(), "9-missing")
	if err == nil {
		t.Fatalf("want error for missing decision, got nil")
	}
}
