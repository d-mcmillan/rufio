package cli

import (
	"os"
	"strings"

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

// NewRefuteCmd returns the `rufio refute <thought-id> --reason=<text>
// [--evidence=<text>]` Cobra command. Appends an @refute record to the
// shared live/confirms/<thought-id>.gdl file (same file confirms append
// to — single per-thought tally). Like confirm, anyone may refute any
// thought; we only verify the target exists.
//
// R32 vocab-mirror: refute long-standing accepts BOTH --reason (required,
// motivational) and --evidence (optional, supporting facts). The cold-agent
// friction was using --evidence ALONE (without --reason) — the agent typed
// the confirm word and got "reason is required". We absorb that STOP by
// promoting --evidence to --reason WHEN AND ONLY WHEN --reason is empty.
// When both are passed, their long-standing distinct semantics stay intact
// (reason = motivation, evidence = facts).
func NewRefuteCmd() *cobra.Command {
	var reasonFlag, evidenceFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "refute <thought-id>",
		Short: "Refute another agent's thought (or your own)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			// R32 alias resolution: if the caller passed --evidence ALONE
			// (no --reason), promote it to the canonical reason slot — they
			// used the confirm-side word but clearly meant "my prose
			// explanation". When BOTH are non-empty, keep them distinct
			// (reason=motivation, evidence=facts) — that's the pre-R32
			// contract the both-flags-distinct guard locks in tests.
			reason := strings.TrimSpace(reasonFlag)
			evidence := evidenceFlag
			if reason == "" && strings.TrimSpace(evidence) != "" {
				reason = evidence
				evidence = ""
			}
			cwd, err := os.Getwd()
			if err == nil {
				err = runRefute(cwd, args[0], reason, evidence, opts)
			}
			if err != nil {
				HandleError("refute", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reasonFlag, "reason", "", "free-text reason for the refutation (required unless --evidence is given alone as an alias)")
	cmd.Flags().StringVar(&evidenceFlag, "evidence", "", "optional supporting evidence; if --reason is empty this value becomes the reason (R32 vocab-mirror alias)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runRefute(cwd, targetID, reasonRaw, evidence string, opts output.RenderOpts) error {
	reason := strings.TrimSpace(reasonRaw)
	if reason == "" {
		return &rufioerr.InvalidContentError{Field: "reason"}
	}

	// v1.0.4: --server routes refute through the remote MCP tool.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"thought_id": targetID,
			"reason":     reason,
			"evidence":   evidence,
		})
		return remoteCallAndRender("refute", "refute", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	agent, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	// R29a: accept short-id suffix (the form `thoughts list` displays).
	// See runConfirm for the full rationale — symmetric here.
	targetID, err = retract.Resolve(root, targetID, agent)
	if err != nil {
		return err
	}

	// Verify the target exists AND check the privacy gate (#147). For
	// scope:agent thoughts only the author may refute — see the parallel
	// runConfirm for the full rationale. Broader-scope thoughts retain
	// crowd-refutation semantics.
	author, scope, err := retract.LookupTarget(root, targetID)
	if err != nil {
		return err
	}
	if !privacy.CanWriteAgainst(targetRef{author: author, scope: scope}, agent) {
		return &rufioerr.PrivateRecordAuthzError{Verb: "refute", ID: targetID, Author: author}
	}

	// #148: advisory stderr warning when the target has been retracted.
	// See confirm.go for rationale — refute is the symmetric case.
	warnIfRetracted("refute", root, targetID, opts)

	ts := versioning.NowISO()
	rec := confirm.BuildRefute(targetID, agent, reason, evidence, ts)
	if err := confirm.Append(root, targetID, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "refute",
			"_version": "1",
			"target":   targetID,
			"reason":   reason,
			"by":       agent,
			"ts":       ts,
		}
		if evidence != "" {
			payload["evidence"] = evidence
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("refute: target="+targetID+" by="+agent, opts)
	return nil
}
