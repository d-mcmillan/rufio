package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// warnIfRetracted emits a single-line stderr advisory when the named
// target has an @retract record on disk. Pattern matches #108's attend
// overwrite warn: never block the write, just surface the prior state
// so the writer knows their record lands in a degraded context.
// --quiet suppresses (matches the attend-warn contract). Read errors
// are swallowed — the underlying write either succeeds (clean) or
// fails (and its error surfaces); a noisy advisory on top of a write
// failure helps no one.
func warnIfRetracted(verb, root, targetID string, opts output.RenderOpts) {
	if opts.Quiet {
		return
	}
	rec, err := retract.ReadByTarget(root, targetID)
	if err != nil || !rec.Present {
		return
	}
	fmt.Fprintf(os.Stderr,
		"rufio %s: %s was retracted at %s by %s (%q); %s will land but won't show as live\n",
		verb, targetID, rec.TS, rec.By, rec.Reason, verb)
}

// targetRef adapts (author, scope) to the privacy.Record interface for
// the confirm/refute authz check. Two-method shim; kept local because
// confirm and refute are the only callers.
type targetRef struct{ author, scope string }

func (t targetRef) GetAuthor() string { return t.author }
func (t targetRef) GetScope() string  { return t.scope }

// NewConfirmCmd returns the `rufio confirm <thought-id> [--evidence=<text>]`
// Cobra command. Appends an @confirm record to live/confirms/<thought-id>.gdl.
// Unlike retract, anyone may confirm — author matching is NOT enforced; we
// only verify the target thought exists.
//
// R32 vocab-mirror: --reason is accepted as a paired-verb alias for --evidence
// so a caller who typed the refute word at confirm doesn't STOP for a help-text
// look-up. On-disk record shape is unchanged: the prose lands as `evidence:`
// regardless of which flag was used. Mutual-exclusion guards a caller passing
// both flags (Cobra MarkFlagsMutuallyExclusive — error before any FS touch).
func NewConfirmCmd() *cobra.Command {
	var evidenceFlag, reasonAliasFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "confirm <thought-id>",
		Short: "Confirm another agent's thought (or your own)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			// R32 alias resolution: --reason is a paired-verb alias for
			// --evidence. Mutual-exclusion is enforced by Cobra below; here
			// we just collapse to the canonical value the runConfirm path
			// expects.
			evidence := evidenceFlag
			if evidence == "" {
				evidence = reasonAliasFlag
			}
			cwd, err := os.Getwd()
			if err == nil {
				err = runConfirm(cwd, args[0], evidence, opts)
			}
			if err != nil {
				HandleError("confirm", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&evidenceFlag, "evidence", "", "optional free-text evidence supporting the confirmation")
	// R32: paired-verb alias. Help-text labels it explicitly so cold agents
	// see both forms when they read --help. The on-disk field stays `evidence:`.
	cmd.Flags().StringVar(&reasonAliasFlag, "reason", "", "alias for --evidence (paired-verb mirror with refute --reason); on-disk field is evidence:")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	cmd.MarkFlagsMutuallyExclusive("evidence", "reason")
	return cmd
}

func runConfirm(cwd, targetID, evidence string, opts output.RenderOpts) error {
	// v1.0.4: --server routes through the remote MCP confirm tool.
	// The server resolves the target id and the privacy gate
	// (CanWriteAgainst) against the bearer-token agent.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"thought_id": targetID,
			"evidence":   evidence,
		})
		return remoteCallAndRender("confirm", "confirm", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	agent, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	// R29a: accept the 6-char suffix that `thoughts list` and `recall`
	// display in text mode. Resolve maps it to the canonical full id
	// (or surfaces *AmbiguousIDError with the candidate list); a full id
	// passes through unchanged. Privacy-floor filtering inside Resolve
	// keeps non-author scope:agent candidates invisible — see the
	// LookupTarget call below, which still enforces the per-target
	// authz check for the resolved id.
	targetID, err = retract.Resolve(root, targetID, agent)
	if err != nil {
		return err
	}

	// Verify the target exists AND check the privacy gate (#147). For
	// scope:agent thoughts only the author may confirm — non-author
	// confirms are rejected with PrivateRecordAuthzError. Broader-scope
	// thoughts (deployment/fleet) continue to admit crowd validation
	// from any peer, matching the pre-#147 contract.
	author, scope, err := retract.LookupTarget(root, targetID)
	if err != nil {
		return err
	}
	if !privacy.CanWriteAgainst(targetRef{author: author, scope: scope}, agent) {
		return &rufioerr.PrivateRecordAuthzError{Verb: "confirm", ID: targetID, Author: author}
	}

	// #148: advisory stderr warning when the target has been retracted.
	// We don't BLOCK — confirms (and refutes) against a retracted thought
	// are still useful as audit signal, and a non-author may not know
	// the retract happened. --quiet suppresses (matches #108's pattern).
	warnIfRetracted("confirm", root, targetID, opts)

	ts := versioning.NowISO()
	rec := confirm.BuildConfirm(targetID, agent, evidence, ts)
	if err := confirm.Append(root, targetID, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "confirm",
			"_version": "1",
			"target":   targetID,
			"by":       agent,
			"ts":       ts,
		}
		if evidence != "" {
			payload["evidence"] = evidence
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("confirm: target="+targetID+" by="+agent, opts)
	return nil
}
