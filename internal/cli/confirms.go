// Package cli — #181 `rufio confirms <thought-id>` detail-view verb.
//
// Detail view for one target's social-validation state. Reads existing
// on-disk artifacts only (live/confirms/<id>.gdl + live/retracted/<id>.gdl
// + live/promoted/<id>.gdl) and surfaces:
//
//   - target metadata (author, type, subject, scope, content/object)
//   - every @confirm with optional evidence
//   - every @refute WITH reason text (the gap Cursor's diary named —
//     `thoughts list` shows +N/-M counts but never the refute prose)
//   - quorum projection: confidence, distinct-confirmer count, threshold
//     ✓/✗ marks, and a status of PROMOTED / RETRACTED / PENDING /
//     CONTESTED / OPEN with the math needed to clear the gate
//
// Thresholds come from autopromote.MinDistinctConfirmers and
// autopromote.MinConfidence — the same constants the engine enforces, so
// the projection NEVER drifts from the actual promotion gate. Hardcoding
// here would be a footgun: cold readers would derive a different rule
// from the projection vs. what the daemon applies.
//
// Out of scope (per #181): no new event types, no changes to
// `thoughts list` rendering, no changes to auto-promote write semantics.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/autopromote"
	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
)

// Status enum strings. Locked field values for JSON consumers; do not
// rename without a versioning bump on the `_type:"confirms"` envelope.
const (
	confirmsStatusPromoted  = "promoted"
	confirmsStatusRetracted = "retracted"
	confirmsStatusPending   = "pending"
	confirmsStatusContested = "contested"
	confirmsStatusOpen      = "open"
)

// NewConfirmsCmd returns the `rufio confirms <thought-id>` Cobra command.
// Detail view (read-only) for a single target's social-validation state.
//
// Mirrors NewLineageCmd's structure (#171) — same flag set, same error
// envelope, same RenderOpts pipeline. The verb plural ("confirms") is
// deliberate: it's the read-side counterpart to the singular write verbs
// `confirm` and `refute`, and reads "show me ALL the confirms for X."
func NewConfirmsCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "confirms <thought-id>",
		Short: "Show the full confirm/refute/retract + quorum state of one target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runConfirms(cwd, args[0], opts)
			}
			if err != nil {
				HandleError("confirms", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// confirmsTarget is the minimal target metadata `confirms` surfaces.
// Type is the @thought type (decision|hypothesis|focus|question|
// observation), or "observation" when the target lives under learned/.
// Content is the @thought content field OR the @observation object field
// (one of them is the human-readable payload).
type confirmsTarget struct {
	ID      string
	Type    string
	Author  string
	Subject string
	Scope   string
	Content string
}

// confirmsQuorum projects the auto-promote gate's view onto the current
// confirm tally. Status is the locked enum string; ProjectionConfirms /
// ProjectionRefutes are the "needs +N / -M" hints derived from the
// thresholds the engine uses.
type confirmsQuorum struct {
	Confirms            int
	Refutes             int
	Total               int
	Confidence          float64
	DistinctConfirmers  int
	ThresholdDistinct   int
	ThresholdConfidence float64
	Status              string

	// Projection hints. NeedsConfirms is the additional distinct
	// confirmers required to clear MinDistinctConfirmers; NeedsLessRefutes
	// is the number of refutes that would have to be retracted to clear
	// the confidence threshold given the current confirm count.
	NeedsConfirms     int
	NeedsLessRefutes  int
	ProjectionMessage string // pre-rendered projection line for text mode
}

// confirmsRetract is a thin shape-mirror of retract.Record so the
// text/JSON renderers can stay decoupled from the lib type.
type confirmsRetract struct {
	Present bool
	TS      string
	By      string
	Reason  string
}

// confirmsPromoted is the post-promote artifact view: ts + path to the
// learned/.../<obs-id>.gdlm file the daemon wrote.
type confirmsPromoted struct {
	Present     bool
	TS          string
	LearnedPath string
}

// runConfirms is the pure logic for `rufio confirms`. Returns typed
// errors; the caller maps them to exit codes via HandleError.
//
// Pipeline:
//  1. Resolve project root + identity.
//  2. retract.Resolve maps a short-id suffix to canonical (or returns
//     ambiguous/not-found). #172 helper — do NOT duplicate the scan.
//  3. Look up the target (thought first, then learned observation).
//  4. Privacy gate (#147) — non-author scope:agent looks like
//     not-found to preserve the existence-leak fence.
//  5. Read confirms/refutes/retract/promote artifacts.
//  6. Compute the quorum projection using autopromote constants.
//  7. Render text or JSON.
func runConfirms(cwd, idOrSuffix string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// R29a/#172: accept short-id suffix. Identity errors are non-fatal
	// for this read-only verb — fall back to "" so anonymous firehose
	// semantics apply (admin/test paths).
	currentAgent, _, _ := identity.Resolve(root)
	resolved, err := retract.Resolve(root, idOrSuffix, currentAgent)
	if err != nil {
		return err
	}

	target, err := lookupConfirmsTarget(root, resolved)
	if err != nil {
		return err
	}

	// Privacy floor (#147): non-author scope:agent → not-found (existence
	// is not leaked). For non-author callers the resolver already filters
	// short-id candidates, so this branch fires when a canonical full id
	// passes through resolve() unchanged.
	if !privacy.IsVisible(targetRef{author: target.Author, scope: target.Scope}, currentAgent) {
		return &rufioerr.NoSuchThoughtError{ID: idOrSuffix}
	}

	records, err := confirm.ReadRecords(root, resolved)
	if err != nil {
		return err
	}
	confirms, refutes := splitSocials(records)

	tally, err := confirm.ReadAll(root, resolved)
	if err != nil {
		return err
	}

	retRec, _ := retract.ReadByTarget(root, resolved)
	cret := confirmsRetract{Present: retRec.Present, TS: retRec.TS, By: retRec.By, Reason: retRec.Reason}

	prom := readPromoted(root, resolved)
	quorum := computeQuorum(confirms, refutes, tally, cret.Present, prom.Present)

	if opts.JSON {
		return renderConfirmsJSON(target, confirms, refutes, cret, prom, quorum)
	}
	return renderConfirmsText(target, confirms, refutes, cret, prom, quorum, opts)
}

// lookupConfirmsTarget tries live/outbox/*/<id>.gdl for an @thought
// first; on miss, walks learned/**/<id>.gdlm for an @observation. Mirrors
// retract.LookupTarget but returns a richer view (subject, content, type)
// since the renderer needs to surface them.
func lookupConfirmsTarget(root, id string) (confirmsTarget, error) {
	// Outbox: live/outbox/<author>/<id>.gdl.
	pattern := filepath.Join(root, "live", "outbox", "*", id+".gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return confirmsTarget{}, err
	}
	if len(matches) > 0 {
		path := matches[0]
		author := filepath.Base(filepath.Dir(path))
		bs, err := os.ReadFile(path)
		if err != nil {
			return confirmsTarget{}, err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return confirmsTarget{}, err
		}
		for _, r := range records {
			if r.Type != "thought" {
				continue
			}
			return confirmsTarget{
				ID:      id,
				Type:    r.Get("type"),
				Author:  author,
				Subject: r.Get("subject"),
				Scope:   r.Get("scope"),
				Content: r.Get("content"),
			}, nil
		}
		// Outbox file exists but no @thought record — treat as miss
		// (same posture as lineage.LookupDecision).
		return confirmsTarget{}, &rufioerr.NoSuchThoughtError{ID: id}
	}

	// Fallback: walk learned/ for <id>.gdlm. Re-uses retract's helper
	// shape implicitly — we duplicate the walk here because the package's
	// findLearnedRecord is unexported. Worst case is the learned tree
	// (bounded; one .gdlm per observation).
	cand, ok := scanLearnedForConfirmsTarget(root, id)
	if ok {
		return cand, nil
	}
	return confirmsTarget{}, &rufioerr.NoSuchThoughtError{ID: id}
}

// scanLearnedForConfirmsTarget walks <root>/learned/ for <id>.gdlm and
// returns the first @observation it finds. Author = the on-disk
// `author:` field (matches retract.findLearnedRecord's semantics).
// Content is mapped from the observation's `object` field — the
// human-readable payload.
func scanLearnedForConfirmsTarget(root, id string) (confirmsTarget, bool) {
	learnedDir := filepath.Join(root, "learned")
	if _, err := os.Stat(learnedDir); err != nil {
		return confirmsTarget{}, false
	}
	want := id + ".gdlm"
	var hit string
	_ = filepath.Walk(learnedDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == want {
			hit = p
			return filepath.SkipDir
		}
		return nil
	})
	if hit == "" {
		return confirmsTarget{}, false
	}
	bs, err := os.ReadFile(hit)
	if err != nil {
		return confirmsTarget{}, false
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return confirmsTarget{}, false
	}
	for _, r := range records {
		if r.Type != "observation" {
			continue
		}
		return confirmsTarget{
			ID:      id,
			Type:    "observation",
			Author:  r.Get("author"),
			Subject: r.Get("subject"),
			Scope:   r.Get("scope"),
			Content: r.Get("object"),
		}, true
	}
	return confirmsTarget{}, false
}

// readPromoted reads live/promoted/<id>.gdl and returns the ts plus the
// learned/<subject>/<obs-id>.gdlm path if the marker is an @auto-promote.
// A @promote-skipped marker (D13.9) is NOT surfaced as "promoted"
// because the on-disk record explicitly says "skipped".
func readPromoted(root, id string) confirmsPromoted {
	path := filepath.Join(root, "live", "promoted", id+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		return confirmsPromoted{}
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return confirmsPromoted{}
	}
	for _, r := range records {
		if r.Type != "auto-promote" {
			continue
		}
		obsID := r.Get("observation")
		ts := r.Get("ts")
		// Locate the learned/.../<obsID>.gdlm via a learned-tree walk —
		// we don't know the subject from this record (D13.8 only carries
		// the obs id) so a walk is the cheapest correct lookup.
		learned := findLearnedPath(root, obsID)
		return confirmsPromoted{Present: true, TS: ts, LearnedPath: learned}
	}
	return confirmsPromoted{}
}

// findLearnedPath walks learned/ for <obsID>.gdlm and returns the
// relative path under <root>/. Empty when not found (the renderer
// degrades to "(observation file missing)" — auto-promote could have
// raced with a manual cleanup; never panic the audit view).
func findLearnedPath(root, obsID string) string {
	learnedDir := filepath.Join(root, "learned")
	if _, err := os.Stat(learnedDir); err != nil {
		return ""
	}
	want := obsID + ".gdlm"
	var hit string
	_ = filepath.Walk(learnedDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == want {
			hit = p
			return filepath.SkipDir
		}
		return nil
	})
	if hit == "" {
		return ""
	}
	// Render as <root>-relative for clean stdout.
	if rel, err := filepath.Rel(root, hit); err == nil {
		return rel
	}
	return hit
}

// computeQuorum derives the quorum projection from the on-disk records.
// The thresholds come from autopromote — never hardcode here, that would
// drift from the engine.
//
// Status resolution order matches the engine's idempotency contract:
//  1. promoted on disk → PROMOTED (terminal)
//  2. retracted on disk → RETRACTED (terminal)
//  3. confidence ≥ threshold AND distinct ≥ threshold → would-be
//     promoted but the daemon hasn't run yet OR the marker hasn't
//     landed. Surface PENDING with no projection (math already clears
//     the gate).
//  4. distinct ≥ threshold OR refutes > 0 → CONTESTED with projection
//  5. else → OPEN
func computeQuorum(confirms, refutes []confirm.Record, tally confirm.Tally, retracted, promoted bool) confirmsQuorum {
	q := confirmsQuorum{
		Confirms:            len(confirms),
		Refutes:             len(refutes),
		Total:               len(confirms) + len(refutes),
		Confidence:          tally.Confidence(),
		DistinctConfirmers:  len(tally.Confirms),
		ThresholdDistinct:   autopromote.MinDistinctConfirmers,
		ThresholdConfidence: autopromote.MinConfidence,
	}

	switch {
	case promoted:
		q.Status = confirmsStatusPromoted
	case retracted:
		q.Status = confirmsStatusRetracted
	case q.Confidence >= q.ThresholdConfidence && q.DistinctConfirmers >= q.ThresholdDistinct:
		// Threshold cleared on the read side but no marker yet — engine
		// will catch up. Surface PENDING (engine race) rather than a
		// fabricated PROMOTED.
		q.Status = confirmsStatusPending
	case q.DistinctConfirmers >= q.ThresholdDistinct || q.Refutes > 0:
		q.Status = confirmsStatusContested
		q.ProjectionMessage = projectionForContested(q)
	case q.DistinctConfirmers > 0 || q.Confirms > 0:
		// Has some confirms but neither distinct ≥ threshold nor any
		// refutes — needs more distinct confirmers to clear.
		q.Status = confirmsStatusPending
		q.NeedsConfirms = q.ThresholdDistinct - q.DistinctConfirmers
		q.ProjectionMessage = fmt.Sprintf("needs %d more distinct confirmer", q.NeedsConfirms)
		if q.NeedsConfirms != 1 {
			q.ProjectionMessage += "s"
		}
		q.ProjectionMessage += " to reach quorum"
	default:
		q.Status = confirmsStatusOpen
	}

	return q
}

// projectionForContested computes the +N / -M hint for a CONTESTED
// target. The shape is "needs +X more confirms OR -Y fewer refutes to
// clear <threshold>" — gives the reader BOTH levers the engine respects.
//
// X = additional confirmers needed so confirms/(confirms+refutes) ≥
//
//	threshold, holding refutes constant. Equivalently:
//	X ≥ threshold*(confirms+refutes) - confirms, solved for the smallest
//	integer ≥ 0.
//
// Y = refutes that would need to drop so the same inequality holds,
//
//	holding confirms constant: Y = refutes - floor((1-threshold)/
//	threshold * confirms), clamped at 0.
//
// We also track distinct-confirmer shortfall — the gate has TWO
// independent conditions and a projection that ignores one is misleading.
func projectionForContested(q confirmsQuorum) string {
	var parts []string

	if q.DistinctConfirmers < q.ThresholdDistinct {
		need := q.ThresholdDistinct - q.DistinctConfirmers
		s := "s"
		if need == 1 {
			s = ""
		}
		parts = append(parts, fmt.Sprintf("needs +%d more distinct confirmer%s", need, s))
	}

	if q.Confidence < q.ThresholdConfidence {
		// Solve for X: (c+X) / (c+X+r) ≥ threshold
		// → X ≥ (threshold*r - (1-threshold)*c) / (1-threshold)
		// Integer math via float, then ceil.
		c := float64(q.Confirms)
		r := float64(q.Refutes)
		t := q.ThresholdConfidence
		needConfirms := 0
		if 1-t > 0 {
			raw := (t*r - (1-t)*c) / (1 - t)
			if raw > 0 {
				needConfirms = int(raw) + 1
				// If raw is integer, +1 would over-count. Verify the
				// minimum: try needConfirms-1 first.
				if needConfirms > 0 {
					test := (c + float64(needConfirms-1)) / (c + float64(needConfirms-1) + r)
					if test >= t {
						needConfirms--
					}
				}
			}
		}
		// Solve for Y: c / (c+r-Y) ≥ threshold
		// → Y ≥ r - (1-threshold)/threshold * c
		needLessRefutes := 0
		if t > 0 {
			raw := r - (1-t)/t*c
			if raw > 0 {
				needLessRefutes = int(raw)
				if float64(needLessRefutes) < raw {
					needLessRefutes++
				}
			}
		}

		var confSegment string
		if needConfirms > 0 {
			s := "s"
			if needConfirms == 1 {
				s = ""
			}
			confSegment = fmt.Sprintf("+%d more confirm%s", needConfirms, s)
		}
		var refuteSegment string
		if needLessRefutes > 0 {
			s := "s"
			if needLessRefutes == 1 {
				s = ""
			}
			refuteSegment = fmt.Sprintf("-%d fewer refute%s", needLessRefutes, s)
		}
		var clause string
		switch {
		case confSegment != "" && refuteSegment != "":
			clause = fmt.Sprintf("%s OR %s to clear %.2f confidence", confSegment, refuteSegment, q.ThresholdConfidence)
		case confSegment != "":
			clause = fmt.Sprintf("%s to clear %.2f confidence", confSegment, q.ThresholdConfidence)
		case refuteSegment != "":
			clause = fmt.Sprintf("%s to clear %.2f confidence", refuteSegment, q.ThresholdConfidence)
		}
		if clause != "" {
			parts = append(parts, clause)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

// renderConfirmsText emits the human-readable detail view. WriteData
// (never quiet-suppressed) — the rendered detail IS the primary output.
func renderConfirmsText(t confirmsTarget, confirms, refutes []confirm.Record, ret confirmsRetract, prom confirmsPromoted, q confirmsQuorum, opts output.RenderOpts) error {
	var b strings.Builder

	typ := t.Type
	if typ == "" {
		typ = "thought"
	}
	fmt.Fprintf(&b, "Target: %s (%s)\n", t.ID, typ)
	fmt.Fprintf(&b, "  Author: %s\n", t.Author)
	if t.Subject != "" {
		fmt.Fprintf(&b, "  Subject: %s\n", t.Subject)
	}
	if t.Scope != "" {
		fmt.Fprintf(&b, "  Scope: %s\n", t.Scope)
	}
	if t.Content != "" {
		fmt.Fprintf(&b, "  Content: %q\n", t.Content)
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "Confirms (%d)", len(confirms))
	if len(confirms) == 0 {
		b.WriteString("\n")
	} else {
		b.WriteString(":\n")
		for _, r := range confirms {
			writeConfirmsLine(&b, "+", r.Agent, r.TS, r.Evidence)
		}
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "Refutes (%d)", len(refutes))
	if len(refutes) == 0 {
		b.WriteString("\n")
	} else {
		b.WriteString(":\n")
		for _, r := range refutes {
			// Refute reason is load-bearing (Cursor's diary surfaced
			// this exact gap) — inline it on the row.
			text := r.Reason
			if text == "" {
				text = r.Evidence
			}
			writeConfirmsLine(&b, "-", r.Agent, r.TS, text)
		}
	}

	b.WriteString("\n")
	if ret.Present {
		fmt.Fprintf(&b, "Retraction: %s by %s — %q\n", ret.TS, ret.By, ret.Reason)
	} else {
		b.WriteString("Retractions: none\n")
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "Confidence: %s (%d confirms / %d total)\n",
		formatConfidence(q.Confidence), q.Confirms, q.Total)
	distinctMark := "✗"
	if q.DistinctConfirmers >= q.ThresholdDistinct {
		distinctMark = "✓"
	}
	fmt.Fprintf(&b, "Distinct confirmers: %d (threshold: ≥%d) %s\n",
		q.DistinctConfirmers, q.ThresholdDistinct, distinctMark)

	// Status line carries the projection inline when present.
	switch q.Status {
	case confirmsStatusPromoted:
		if prom.LearnedPath != "" {
			fmt.Fprintf(&b, "Status: PROMOTED at %s → %s\n", prom.TS, prom.LearnedPath)
		} else {
			fmt.Fprintf(&b, "Status: PROMOTED at %s\n", prom.TS)
		}
	case confirmsStatusRetracted:
		fmt.Fprintf(&b, "Status: RETRACTED at %s by %s — %q\n", ret.TS, ret.By, ret.Reason)
	case confirmsStatusPending:
		if q.ProjectionMessage != "" {
			fmt.Fprintf(&b, "Status: PENDING — %s\n", q.ProjectionMessage)
		} else {
			b.WriteString("Status: PENDING — quorum met; awaiting auto-promote engine\n")
		}
	case confirmsStatusContested:
		if q.ProjectionMessage != "" {
			fmt.Fprintf(&b, "Status: CONTESTED — %s\n", q.ProjectionMessage)
		} else {
			b.WriteString("Status: CONTESTED\n")
		}
	default:
		b.WriteString("Status: OPEN\n")
	}

	output.WriteData(strings.TrimRight(b.String(), "\n"), opts)
	return nil
}

// writeConfirmsLine renders one Confirms/Refutes row. Format:
//
//	<sign> <agent> (<ts>) — "<text>"
//
// ts is rendered raw (RFC3339Nano) — JSON consumers and humans both want
// the precise stamp; relative-time gloss is a cosmetic polish reserved
// for the secondary `thoughts list` view that needs columnar density.
func writeConfirmsLine(b *strings.Builder, sign, agent, ts, text string) {
	b.WriteString("  ")
	b.WriteString(sign)
	b.WriteString(" ")
	b.WriteString(agent)
	if ts != "" {
		fmt.Fprintf(b, " (%s)", ts)
	}
	if text != "" {
		fmt.Fprintf(b, " — %q", text)
	}
	b.WriteString("\n")
}

// formatConfidence drops trailing zeros so 1.0 renders as "1" but 0.67
// stays "0.67". Two-decimal precision matches what the auto-promote
// engine writes to learned/. Locked here so JSON and text agree.
func formatConfidence(c float64) string {
	s := fmt.Sprintf("%.2f", c)
	// Trim trailing zeros AND trailing dot.
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

// renderConfirmsJSON emits the locked-shape JSON payload. Field set is
// stable per the issue spec — every key present on every call (null for
// absent), so consumers don't need to defensive-check.
func renderConfirmsJSON(t confirmsTarget, confirms, refutes []confirm.Record, ret confirmsRetract, prom confirmsPromoted, q confirmsQuorum) error {
	confirmRows := make([]map[string]interface{}, 0, len(confirms))
	for _, r := range confirms {
		confirmRows = append(confirmRows, map[string]interface{}{
			"agent":    r.Agent,
			"ts":       r.TS,
			"evidence": r.Evidence,
		})
	}
	refuteRows := make([]map[string]interface{}, 0, len(refutes))
	for _, r := range refutes {
		refuteRows = append(refuteRows, map[string]interface{}{
			"agent":  r.Agent,
			"ts":     r.TS,
			"reason": r.Reason,
			// Refute MAY carry evidence too; surface it so the JSON
			// shape matches text (text shows reason + evidence both).
			"evidence": r.Evidence,
		})
	}

	target := map[string]interface{}{
		"id":      t.ID,
		"type":    t.Type,
		"author":  t.Author,
		"subject": t.Subject,
		"scope":   t.Scope,
		"content": t.Content,
	}

	quorum := map[string]interface{}{
		"confidence":           q.Confidence,
		"distinct_confirmers":  q.DistinctConfirmers,
		"threshold_distinct":   q.ThresholdDistinct,
		"threshold_confidence": q.ThresholdConfidence,
		"status":               q.Status,
		// Projection numbers (zero when N/A) — flat for consumers.
		"confirms": q.Confirms,
		"refutes":  q.Refutes,
		"total":    q.Total,
	}
	if q.ProjectionMessage != "" {
		quorum["projection"] = q.ProjectionMessage
	} else {
		quorum["projection"] = nil
	}

	payload := map[string]interface{}{
		"_type":    "confirms",
		"_version": "1",
		"target":   target,
		"confirms": confirmRows,
		"refutes":  refuteRows,
		"quorum":   quorum,
	}
	if ret.Present {
		payload["retract"] = map[string]interface{}{
			"ts":     ret.TS,
			"by":     ret.By,
			"reason": ret.Reason,
		}
	} else {
		payload["retract"] = nil
	}
	if prom.Present {
		payload["promoted"] = map[string]interface{}{
			"ts":           prom.TS,
			"learned_path": prom.LearnedPath,
		}
	} else {
		payload["promoted"] = nil
	}

	return output.WriteJSONL(payload, output.RenderOpts{})
}
