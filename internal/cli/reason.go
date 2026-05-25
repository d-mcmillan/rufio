package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/reason"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewReasonCmd returns the `rufio reason` Cobra command. Captures a
// reasoning step under live/reasoning/<me>/[<decision-id>/]<id>.gdl.
//
//	rufio reason --content=<text> [--subject=<entity>] [--parent=<reason-id>] [--decision=<decision-id>] [--topics=<csv>] [--scope=<...>]
//
// --scope is part of the #125 verb-pattern-consistency fix. Every other
// write verb (think/observe/attend/goal) accepts --scope; before #125
// reason rejected it with "unknown flag", which silently let agents who
// learned the pattern from a peer verb write reasons without the scope
// they intended. Default is "fleet" — reasoning under a decision is a
// broadcast primitive (mirrors the decision's typical scope:fleet).
//
// --subject (P2/R31) is the canonical "what this reasoning is about"
// slot, mirroring think/observe. Entity-id form per
// thought.ValidateSubject (e.g. customer:5821). Optional today so
// legacy reason rows stay valid; the strong recommendation is to set
// it. --topics keeps its existing record-label semantics (plural CSV,
// labels on the reason step) — the two coexist exactly like
// `observe --subject ... --topics=...`.
func NewReasonCmd() *cobra.Command {
	var contentFlag, parentFlag, decisionFlag, topicsFlag, scopeFlag, subjectFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "reason",
		Short: "Capture a step in the agent's reasoning chain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runReason(cwd, reasonArgs{
					Content: contentFlag, Parent: parentFlag,
					Decision: decisionFlag, Topics: topicsFlag,
					Scope: scopeFlag, Subject: subjectFlag,
				}, opts)
			}
			if err != nil {
				HandleError("reason", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&contentFlag, "content", "", "reasoning step content (required)")
	cmd.Flags().StringVar(&subjectFlag, "subject", "", "entity id this reasoning is about, namespace:local e.g. customer:5821 (optional; canonical 'what this is about' slot — same shape as think/observe --subject)")
	cmd.Flags().StringVar(&parentFlag, "parent", "", "parent reason id (optional)")
	cmd.Flags().StringVar(&decisionFlag, "decision", "", "decision thought id this reasons under (optional)")
	cmd.Flags().StringVar(&topicsFlag, "topics", "", "comma-separated topic tokens labelling this reasoning step (plural; record-side labels — distinct from `summon --topic` which names a channel)")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "visibility scope (agent|deployment|fleet); default fleet")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

type reasonArgs struct {
	Content, Parent, Decision, Topics, Scope string
	// Subject (P2/R31) — canonical "what this reasoning is about" slot,
	// mirroring think/observe's --subject. Singular entity-id form per
	// thought.ValidateSubject (e.g. customer:5821). Optional today (empty
	// allowed) to stay backward-compat with legacy reason rows that pre-
	// date the flag. --topics remains as the record-label slot (plural
	// CSV), matching the existing observe shape (subject + topics).
	Subject string
}

func runReason(cwd string, a reasonArgs, opts output.RenderOpts) error {
	content := strings.TrimSpace(a.Content)
	if err := thought.ValidateContent(content); err != nil {
		return err
	}
	if err := thought.ValidateParent(a.Parent); err != nil {
		return err
	}
	// P2/R31: --subject is OPTIONAL on reason (legacy reason rows had
	// no subject field). When set, validate against the same entity-id
	// regex as think/observe so a typo errors at write time.
	subject := strings.TrimSpace(a.Subject)
	if subject != "" {
		if err := thought.ValidateSubject(subject); err != nil {
			return err
		}
	}
	// v1.0.5: --server routes through the remote MCP reason tool.
	// Identity comes from the bearer token. We DO NOT resolve the
	// short-id --decision suffix locally in remote mode — the server
	// holds the canonical substrate. Validate cheap-and-syntactic
	// here (content/parent/subject already done above; topics +
	// scope after this block); the server re-runs everything.
	topics := splitCSVTrim(a.Topics)
	if err := attention.ValidateTopics(topics); err != nil {
		return err
	}
	scope := strings.TrimSpace(a.Scope)
	if scope == "" {
		scope = "fleet"
	}
	if err := thought.ValidateScope(scope); err != nil {
		return err
	}
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"content":  content,
			"subject":  subject,
			"parent":   a.Parent,
			"decision": a.Decision,
			"topics":   topics,
			"scope":    scope,
		})
		return remoteCallAndRender("reason", "reason", args, opts)
	}
	// R29a: accept short-id suffix on --decision. The shape validator
	// downstream (ValidateDecision) only knows the full canonical form,
	// so we resolve FIRST and replace a.Decision with the canonical id.
	// Empty --decision is a no-op (free reasoning step, not chained).
	// Resolution needs the project root — we fold the lookup into the
	// same FindProjectRoot block below by pulling root forward.
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	// R29a: ONLY attempt suffix resolution when the value has the
	// short-id shape `[a-z0-9]{6}`. Garbage strings (e.g. "BAD") must
	// flow through to ValidateDecision so they raise the canonical
	// *InvalidDecisionError{exit 2}, not a "no such record" miss (exit
	// 1) that masks the shape error. Full canonical ids and the empty
	// string pass through unchanged — only the short-form shape opts
	// into the resolver.
	if a.Decision != "" && retract.LooksLikeShortID(a.Decision) {
		// Anonymous (currentAgent="") here intentionally — reason hasn't
		// resolved identity yet at this point and the privacy floor for
		// --decision is enforced by ValidateDecisionTarget downstream
		// (which reads the target scope), not by candidate filtering at
		// the resolver.
		resolved, rerr := retract.Resolve(root, a.Decision, "")
		if rerr != nil {
			return rerr
		}
		a.Decision = resolved
	}
	if err := reason.ValidateDecision(a.Decision); err != nil {
		return err
	}
	// `root` is already resolved above (the R29a --decision-suffix
	// resolution needed it before the shape validators ran). Re-use
	// rather than re-walk the parent chain.
	// Resolve + type-check --decision BEFORE writing the reason record:
	// a non-empty --decision must name an existing type:decision thought
	// (consistent with lineage's decision-only read contract), else we'd
	// strand an unviewable orphan reason chain (GH #77). Empty --decision
	// is a no-op here (free reasoning step, unchanged).
	if err := reason.ValidateDecisionTarget(root, a.Decision); err != nil {
		return err
	}
	// #148: advisory stderr warning when reasoning is chained under a
	// RETRACTED decision. We don't BLOCK — the reason record may still
	// be useful audit signal (e.g. capturing counter-reasoning after a
	// retract) — but a cold author of the reason needs to know the
	// target was walked back. --quiet suppresses (matches the #108
	// attend-overwrite pattern).
	if a.Decision != "" {
		warnIfRetracted("reason", root, a.Decision, opts)
	}
	agent, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	id, err := thought.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()

	rec := reason.BuildRecord(reason.ReasonInput{
		ID: id, Author: agent, Content: content, Scope: scope,
		Subject: subject, Topics: topics,
		Parent: a.Parent, Decision: a.Decision, TS: ts,
	})

	if err := reason.Write(root, agent, a.Decision, id, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "reason",
			"_version": "1",
			"id":       id,
			"author":   agent,
			"content":  content,
			"scope":    scope,
			"ts":       ts,
		}
		// P2/R31: subject is OPTIONAL — emit as nullable string so
		// machine consumers can range it unconditionally without the
		// key sprouting from nowhere on legacy rows. Empty subject
		// renders JSON null (matches the parent/decision pattern
		// above).
		if subject != "" {
			payload["subject"] = subject
		} else {
			payload["subject"] = nil
		}
		if topics == nil {
			payload["topics"] = []string{}
		} else {
			payload["topics"] = topics
		}
		if a.Parent != "" {
			payload["parent"] = a.Parent
		} else {
			payload["parent"] = nil
		}
		if a.Decision != "" {
			payload["decision"] = a.Decision
		} else {
			payload["decision"] = nil
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("reason: id="+id+" scope="+scope, opts)
	return nil
}
