package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/lineage"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
)

// NewLineageCmd returns the `rufio lineage <decision-id>` Cobra command.
// Read-only audit reconstruction: parses the @thought + @context-bundle
// in live/outbox/*/<id>.gdl (or live/expired/*/<id>.gdl), resolves each
// bundle sha against .rufio/refs/, and walks the @reason chain under
// live/reasoning/<author>/<id>/.
//
// All failure modes propagate to HandleError which prints the canonical
// `rufio lineage: <msg>` envelope. Success output (the rendered tree)
// is unprefixed per the single-prefix invariant.
func NewLineageCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "lineage <decision-id>",
		Short: "Reconstruct the audit trail of a decision (bundle + reasoning chain)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runLineage(cwd, args[0], opts)
			}
			if err != nil {
				HandleError("lineage", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runLineage is the pure logic for `rufio lineage`. Returns typed errors;
// the caller maps them to exit codes via HandleError.
func runLineage(cwd, id string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// R29a: accept short-id suffix. Identity errors are non-fatal here
	// (lineage is read-only and may run pre-identity for inspection) —
	// fall back to "" so Resolve uses anonymous firehose semantics.
	currentAgentForResolve, _, _ := identity.Resolve(root)
	id, err = retract.Resolve(root, id, currentAgentForResolve)
	if err != nil {
		// Resolve only errors with *AmbiguousIDError or
		// *NoSuchThoughtError; both want to surface to the user.
		// Translate NoSuchThoughtError → NoSuchDecisionError so
		// lineage's error envelope stays consistent with its
		// "decision not found" wording — a thought-id miss against
		// the lineage verb is semantically a decision miss.
		if _, isAmbig := err.(*rufioerr.AmbiguousIDError); isAmbig {
			return err
		}
		if _, isNoSuch := err.(*rufioerr.NoSuchThoughtError); isNoSuch {
			return &rufioerr.NoSuchDecisionError{ID: id}
		}
		return err
	}

	decision, err := lineage.LookupDecision(root, id)
	if err != nil {
		return err
	}
	refs, err := lineage.ResolveBundleRefs(root, decision.Bundle)
	if err != nil {
		return err
	}
	chain, err := lineage.WalkReasoning(root, decision.ID)
	if err != nil {
		return err
	}
	// #125: privacy filter. With the @reason `scope:` field, other agents'
	// scope:agent reasoning must not appear in this caller's lineage
	// (matches recall/stream/goals — every read surface enforces the
	// same rule via privacy.IsVisible). Anonymous callers
	// (currentAgent == "") preserve the firehose path. Identity errors
	// are non-fatal here: lineage is read-only and may run pre-identity
	// (e.g. inspection) — fall back to "" and emit the full chain.
	currentAgent, _, _ := identity.Resolve(root)
	chain = filterReasoningPrivacy(chain, currentAgent)

	// K1 / R28: topic-adjacent voices. Same subject, post-decision-ts,
	// scope-visible — any @thought (any type) or @observation. Surfaces
	// the contributions of agents who voice via `think --type=focus|
	// hypothesis` rather than `reason --decision=`, so non-Claude
	// cognition vocabularies don't become dark matter in lineage.
	voices, err := lineage.WalkTopicAdjacent(root, decision.Subject, decision.TS, decision.ID)
	if err != nil {
		return err
	}
	voices = filterTopicVoicesPrivacy(voices, currentAgent)

	// CLI-layer join: read social-validation records (confirms +
	// refutes share one file at live/confirms/<id>.gdl). Best-effort
	// per the package's read posture — a parse error degrades to no
	// social annotations rather than failing the whole audit.
	socials, err := confirm.ReadRecords(root, decision.ID)
	if err != nil {
		return err
	}
	confirms, refutes := splitSocials(socials)

	// #148: retract-state join. Best-effort like the confirm join — a
	// read error degrades to "not retracted" rather than aborting the
	// audit. When present, the renderer adds a Retracted: line under
	// the Decision: header and tags any reason that landed AFTER the
	// retract ts as [POST-RETRACT].
	retractRec, rerr := retract.ReadByTarget(root, decision.ID)
	if rerr != nil {
		retractRec = retract.Record{}
	}

	if opts.JSON {
		return renderLineageJSON(decision, refs, chain, voices, confirms, refutes, retractRec)
	}
	return renderLineageColumnar(decision, refs, chain, voices, confirms, refutes, retractRec, opts)
}

// filterTopicVoicesPrivacy drops scope:agent voices authored by other
// agents (#147). currentAgent="" preserves every voice (anonymous
// firehose, matching privacy.IsVisible). Mirrors filterReasoningPrivacy
// — uniform privacy gate across every lineage section.
func filterTopicVoicesPrivacy(voices []lineage.TopicVoice, currentAgent string) []lineage.TopicVoice {
	if currentAgent == "" {
		return voices
	}
	out := make([]lineage.TopicVoice, 0, len(voices))
	for _, v := range voices {
		if !privacy.IsVisible(v, currentAgent) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// filterReasoningPrivacy drops scope:agent reasoning steps authored by
// other agents (#125). currentAgent="" preserves every step (anonymous
// firehose, matching privacy.IsVisible). Order is preserved (the
// post-sortByChain order from WalkReasoning); Depth is not renumbered
// because hiding a node does not promote children — they remain in the
// chain with their original depth so the tree shape stays comprehensible
// even with a gap. A hidden node's children with the same author (= the
// caller's view of their OWN sub-tree under a peer's hidden step)
// remain visible; lineage.sortByChain treats parents-not-in-set as
// roots, but here we leave Depth unchanged so the columnar render shows
// the indentation the agent built it under. This is a conservative
// choice — the alternative (recomputing Depth after a hide) is a
// followup if user feedback requests it.
func filterReasoningPrivacy(chain []lineage.ReasoningStep, currentAgent string) []lineage.ReasoningStep {
	if currentAgent == "" {
		return chain
	}
	out := make([]lineage.ReasoningStep, 0, len(chain))
	for _, s := range chain {
		if !privacy.IsVisible(s, currentAgent) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// splitSocials partitions confirm.ReadRecords output into confirms +
// refutes, preserving file-order (chronological).
func splitSocials(all []confirm.Record) (confirms, refutes []confirm.Record) {
	for _, r := range all {
		switch r.Kind {
		case "confirm":
			confirms = append(confirms, r)
		case "refute":
			refutes = append(refutes, r)
		}
	}
	return confirms, refutes
}

// renderLineageColumnar prints the human-readable tree shape from
// spec lines 440-453. Uses WriteData (never suppressed by --quiet)
// because the rendered tree IS the command's primary output, not
// chatter.
//
// Confirmations and Refutations sections are appended when records
// exist (#132). Empty sections are omitted entirely — no header for
// a zero-row block.
func renderLineageColumnar(d lineage.Decision, refs []lineage.ContextRef, chain []lineage.ReasoningStep, voices []lineage.TopicVoice, confirms, refutes []confirm.Record, retractRec retract.Record, opts output.RenderOpts) error {
	var b strings.Builder

	b.WriteString("Decision: ")
	b.WriteString(d.ID)
	b.WriteString("\n")

	// #148: retract surface — placed right under the Decision: header
	// so a cold reader sees the negative-branch state BEFORE the
	// content. Without this line a retracted decision was rendered
	// identically to a live one, defeating the social-validation loop.
	if retractRec.Present {
		fmt.Fprintf(&b, "Retracted: %s by %s — %q\n", retractRec.TS, retractRec.By, retractRec.Reason)
	}

	b.WriteString("Made at: ")
	b.WriteString(d.TS)
	b.WriteString(" by ")
	b.WriteString(d.Author)
	if d.Expired {
		b.WriteString(" (expired)")
	}
	b.WriteString("\n")

	b.WriteString("Subject: ")
	b.WriteString(d.Subject)
	b.WriteString("\n")

	b.WriteString("Scope: ")
	b.WriteString(d.Scope)
	b.WriteString("\n")

	b.WriteString("Content: ")
	b.WriteString(d.Content)
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString("Context bundle:\n")
	if len(refs) == 0 {
		b.WriteString("  (no context bundle)\n")
	} else {
		for _, r := range refs {
			if r.Resolved {
				short := r.SHA256
				if len(short) > 8 {
					short = short[:8]
				}
				fmt.Fprintf(&b, "  %s@v%d (sha: %s)\n", r.Path, r.Version, short)
			} else {
				fmt.Fprintf(&b, "  (unknown sha: %s)\n", r.SHA256)
			}
		}
	}

	b.WriteString("\n")
	b.WriteString("Reasoning chain:\n")
	if len(chain) == 0 {
		b.WriteString("  (no reasoning chain)\n")
	} else {
		for i, step := range chain {
			indent := strings.Repeat("  ", step.Depth+1)
			// Each step surfaces its author (#138): cross-agent
			// reasoning is first-class, so the reader must be able to
			// tell who wrote what at a glance.
			// #148: tag steps that landed AFTER the retract — a
			// cold reader otherwise can't tell which reasoning is
			// chained onto a walked-back decision. ts comparison is
			// lexicographic on RFC3339Nano (writer pins UTC), matching
			// the sort contract used elsewhere.
			postRetract := ""
			if retractRec.Present && step.TS != "" && retractRec.TS != "" && step.TS > retractRec.TS {
				postRetract = " [POST-RETRACT]"
			}
			fmt.Fprintf(&b, "%s%d. [%s]%s %q\n", indent, i+1, step.Author, postRetract, step.Content)
		}
	}

	// K1 / R28 topic-adjacent voices. Same subject, post-decision-ts,
	// scope-visible @thought / @observation records. Sits between
	// Reasoning chain and Confirmations: a cold reader scans in the
	// natural order — what was decided, the chained reasoning, the
	// peer voices that came in around it, then the social-validation
	// tally. Section is only emitted when it has at least one voice.
	if len(voices) > 0 {
		b.WriteString("\n")
		b.WriteString("Topic-adjacent voices:\n")
		// 20-row text-mode cap per spec; JSON returns all. Truncate
		// silently and surface a one-line "(… N more)" footer so the
		// reader knows the section was paginated.
		const maxVoicesText = 20
		shown := voices
		extra := 0
		if len(shown) > maxVoicesText {
			extra = len(shown) - maxVoicesText
			shown = shown[:maxVoicesText]
		}
		for _, v := range shown {
			label := v.ThoughtType
			if v.Type == "observation" {
				label = "observation"
			}
			if label == "" {
				label = v.Type
			}
			payload := v.Content
			if payload == "" {
				payload = v.Object
			}
			fmt.Fprintf(&b, "  - [%s] %s — %q\n", v.Author, label, payload)
		}
		if extra > 0 {
			fmt.Fprintf(&b, "  (… %d more — use --json for full list)\n", extra)
		}
	}

	// #132 social-validation surfacing. Each section is only emitted
	// when it has at least one record — no empty headers.
	if len(confirms) > 0 {
		b.WriteString("\n")
		b.WriteString("Confirmations:\n")
		for _, r := range confirms {
			writeSocialLine(&b, r)
		}
	}
	if len(refutes) > 0 {
		b.WriteString("\n")
		b.WriteString("Refutations:\n")
		for _, r := range refutes {
			writeSocialLine(&b, r)
		}
	}

	output.WriteData(strings.TrimRight(b.String(), "\n"), opts)
	return nil
}

// writeSocialLine renders one Confirmations/Refutations row. Format:
//
//   - <agent> (<ts>) — "<text>"
//
// For confirms, <text> is the optional evidence. For refutes, <text>
// is the (required) reason; when evidence is ALSO set on a refute we
// append it parenthetically so neither field gets dropped — the
// disagreement reason is load-bearing and must surface.
func writeSocialLine(b *strings.Builder, r confirm.Record) {
	b.WriteString("  - ")
	b.WriteString(r.Agent)
	if r.TS != "" {
		b.WriteString(" (")
		b.WriteString(r.TS)
		b.WriteString(")")
	}
	primary, secondary := "", ""
	switch r.Kind {
	case "confirm":
		primary = r.Evidence
	case "refute":
		// Reason is the load-bearing free-text; evidence is the
		// optional supporting pointer. Render reason first, then
		// evidence as "(evidence: …)" when both are populated.
		primary = r.Reason
		if r.Evidence != "" && r.Reason != "" {
			secondary = r.Evidence
		} else if r.Reason == "" {
			primary = r.Evidence
		}
	}
	if primary != "" {
		b.WriteString(" — ")
		fmt.Fprintf(b, "%q", primary)
	}
	if secondary != "" {
		fmt.Fprintf(b, " (evidence: %q)", secondary)
	}
	b.WriteString("\n")
}

// renderLineageJSON emits a single JSON object on stdout per the
// `--json` contract documented in the lineage spec.
//
// Slices are normalised: nil → []. Optional reason fields (parent) emit
// "" when absent so consumers don't need to handle both null and
// missing-key cases.
//
// #132: decision.confirmed_by and decision.refuted_by are ALWAYS
// present (possibly []), never null or absent — matches the topics
// array convention in attend.go so callers can iterate without
// nil-checks.
func renderLineageJSON(d lineage.Decision, refs []lineage.ContextRef, chain []lineage.ReasoningStep, voices []lineage.TopicVoice, confirms, refutes []confirm.Record, retractRec retract.Record) error {
	bundle := make([]map[string]interface{}, 0, len(refs))
	for _, r := range refs {
		bundle = append(bundle, map[string]interface{}{
			"sha256":   r.SHA256,
			"path":     r.Path,
			"version":  r.Version,
			"stage":    r.Stage,
			"resolved": r.Resolved,
		})
	}
	reasoning := make([]map[string]interface{}, 0, len(chain))
	for _, s := range chain {
		reasoning = append(reasoning, map[string]interface{}{
			"id":      s.ID,
			"author":  s.Author,
			"content": s.Content,
			"ts":      s.TS,
			"parent":  s.Parent,
			"depth":   s.Depth,
		})
	}
	decision := map[string]interface{}{
		"id":           d.ID,
		"author":       d.Author,
		"subject":      d.Subject,
		"content":      d.Content,
		"scope":        d.Scope,
		"ts":           d.TS,
		"expired":      d.Expired,
		"confirmed_by": socialsToJSON(confirms),
		"refuted_by":   socialsToJSON(refutes),
	}
	// #148: retracted_at/by/reason ALWAYS present (null when no
	// retract), matching the confirmed_by/refuted_by stability
	// convention from #132.
	if retractRec.Present {
		decision["retracted_at"] = retractRec.TS
		decision["retracted_by"] = retractRec.By
		decision["retract_reason"] = retractRec.Reason
	} else {
		decision["retracted_at"] = nil
		decision["retracted_by"] = nil
		decision["retract_reason"] = nil
	}
	// K1 / R28: topic-adjacent voices. ALWAYS present (possibly []),
	// never null or absent — matches the confirmed_by/refuted_by
	// stability convention from #132 so consumers can iterate without
	// nil-checks. Each row carries kind + author + content/object so a
	// downstream consumer can render whichever subset matters.
	topicAdjacent := make([]map[string]interface{}, 0, len(voices))
	for _, v := range voices {
		topicAdjacent = append(topicAdjacent, map[string]interface{}{
			"type":         v.Type, // "thought" | "observation"
			"id":           v.ID,
			"author":       v.Author,
			"thought_type": v.ThoughtType,
			"content":      v.Content,
			"object":       v.Object,
			"predicate":    v.Predicate,
			"scope":        v.Scope,
			"ts":           v.TS,
		})
	}
	payload := map[string]interface{}{
		"_type":                 "lineage",
		"_version":              "1",
		"decision":              decision,
		"bundle":                bundle,
		"reasoning":             reasoning,
		"topic_adjacent_voices": topicAdjacent,
	}
	return output.WriteJSONL(payload, output.RenderOpts{})
}

// socialsToJSON renders confirm.Record entries as the {agent, ts,
// evidence} shape locked by #132. Nil input → empty slice (never
// null). Refute reason is included as `reason` only when populated,
// so confirm rows stay clean of refute-only fields.
func socialsToJSON(rs []confirm.Record) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(rs))
	for _, r := range rs {
		row := map[string]interface{}{
			"agent":    r.Agent,
			"ts":       r.TS,
			"evidence": r.Evidence,
		}
		if r.Kind == "refute" && r.Reason != "" {
			row["reason"] = r.Reason
		}
		out = append(out, row)
	}
	return out
}
