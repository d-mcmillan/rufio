package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// newGoalCompleteCmd returns the `rufio goal complete <goal-id>
// --outcome=<text>` Cobra subcommand. Moves live/goals/active/<id>.gdl to
// live/goals/completed/<id>.gdl and appends the @goal-complete audit
// record (D17.7/D17.9). State transitions are one-way (D17.13) — a goal
// that is no longer in active surfaces as *NoSuchGoalError, including
// the already-completed and already-abandoned cases, so the caller can't
// distinguish "never existed" from "already handled" by exit code.
//
// Lowercase / package-internal: only NewGoalCmd() references it. The
// HandleError prefix is "goal" (parent name) to preserve the
// single-prefix invariant — users see `rufio goal: <err>` regardless
// of whether they ran the parent or this subcommand.
// R32 vocab-mirror: --reason is accepted as a paired-verb alias for
// --outcome so a caller who typed the abandon word at complete doesn't
// STOP for a help-text look-up. On-disk record SHAPE is unchanged: the
// prose lands as `outcome:` on the @goal-complete audit record. Mutual-
// exclusion via Cobra MarkFlagsMutuallyExclusive — both flags simultaneously
// errors before any state transition.
func newGoalCompleteCmd() *cobra.Command {
	var outcomeFlag, reasonAliasFlag string
	var jsonFlag, quietFlag, noColorFlag, forceFlag bool
	cmd := &cobra.Command{
		Use:   "complete <goal-id>",
		Short: "Mark an active goal as completed (author-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			// R32 alias resolution: collapse --reason → --outcome. Mutual
			// exclusion is enforced by Cobra below; here we just pick the
			// canonical value to pass downstream.
			outcome := outcomeFlag
			if outcome == "" {
				outcome = reasonAliasFlag
			}
			cwd, err := os.Getwd()
			if err == nil {
				err = runGoalComplete(cwd, args[0], outcome, forceFlag, opts)
			}
			if err != nil {
				HandleError("goal", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outcomeFlag, "outcome", "", "outcome description (required)")
	cmd.Flags().StringVar(&reasonAliasFlag, "reason", "", "alias for --outcome (paired-verb mirror with goal abandon --reason); on-disk field is outcome:")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "bypass active-children hierarchy check (#130)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	cmd.MarkFlagsMutuallyExclusive("outcome", "reason")
	return cmd
}

// runGoalComplete is the pure logic for `rufio goal complete`. Mirrors
// runDecline's validation-then-state-transition shape: cheap-and-syntactic
// first (outcome trim), then filesystem-touching (root + identity + state
// load), and only then the actual move. Authorisation precedes the move so
// an unauthorised caller never produces a write side effect.
func runGoalComplete(cwd, goalID, outcomeRaw string, force bool, opts output.RenderOpts) error {
	outcome := strings.TrimSpace(outcomeRaw)
	if outcome == "" {
		return &rufioerr.InvalidContentError{Field: "outcome"}
	}

	// v1.0.5: --server routes through the remote MCP goal_complete tool.
	// Identity (author check that gates the move) comes from the bearer
	// token; the server enforces D17.8 authz against it. The --force
	// flag is local-only (the server always enforces #130 active-child
	// safety today — a future MCP tool option could expose it).
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"goal_id": goalID,
			"outcome": outcome,
		})
		return remoteCallAndRender("goal", "goal_complete", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	me, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	// R30: LoadAnyStateAs gates the suffix-resolution candidate set with
	// the resolved agent so other-author scope:agent goals don't surface
	// via the disambiguation error (#147).
	loaded, err := goal.LoadAnyStateAs(root, goalID, me)
	if err != nil {
		return err
	}
	// R30: LoadAnyStateAs may have resolved a 6-char suffix to a
	// canonical id. Propagate the canonical form through the rest of the
	// handler so downstream MoveToCompleted / ActiveChildren / error
	// surfaces path-derive from the real on-disk filename. Before R30 only
	// the LOAD path was suffix-aware (PR #172) — the MOVE path retained
	// the original short id, so the user saw "no such goal: <short>"
	// after a successful read.
	goalID = loaded.ID
	// D17.13: state transitions are one-way. Anything not currently in
	// active (already completed/abandoned) surfaces as NoSuchGoalError —
	// the brief is unambiguous: complete is only valid on active.
	if loaded.State != goal.StateActive {
		return &rufioerr.NoSuchGoalError{ID: goalID}
	}
	// D17.8: only the goal's original author may complete/abandon.
	if loaded.Author != me {
		return &rufioerr.GoalAuthError{ID: goalID, Author: loaded.Author}
	}
	// #130: refuse to complete a parent while it has active children.
	// --force bypasses (rare legitimate case: parent is unblocking, the
	// remaining children stand independently). Check runs AFTER auth so
	// an unauthorised caller can't probe the hierarchy via the error
	// shape (they get GoalAuthError, not GoalActiveChildrenError).
	if !force {
		kids, err := goal.ActiveChildren(root, goalID)
		if err != nil {
			return err
		}
		if len(kids) > 0 {
			return &rufioerr.GoalActiveChildrenError{
				ID:       goalID,
				Op:       "complete",
				Children: kids,
			}
		}
	}

	ts := versioning.NowISO()
	if err := goal.MoveToCompleted(root, goalID, me, outcome, ts); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "goal-complete",
			"_version": "1",
			"id":       goalID,
			"by":       me,
			"outcome":  outcome,
			"ts":       ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteOut("completed: id="+goalID+" outcome="+outcome, opts)
	return nil
}
