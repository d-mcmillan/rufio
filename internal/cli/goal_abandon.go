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

// newGoalAbandonCmd returns the `rufio goal abandon <goal-id>
// --reason=<text>` Cobra subcommand. Moves live/goals/active/<id>.gdl to
// live/goals/abandoned/<id>.gdl and appends the @goal-abandon audit
// record (D17.7/D17.10). State transitions are one-way (D17.13) — a goal
// that is no longer in active surfaces as *NoSuchGoalError, including
// the already-completed and already-abandoned cases, so the caller can't
// distinguish "never existed" from "already handled" by exit code.
//
// Lowercase / package-internal: only NewGoalCmd() references it. The
// HandleError prefix is "goal" (parent name) to preserve the
// single-prefix invariant — users see `rufio goal: <err>` regardless
// of whether they ran the parent or this subcommand.
// R32 vocab-mirror: --outcome is accepted as a paired-verb alias for
// --reason (symmetric with goal complete's --reason alias). On-disk record
// SHAPE is unchanged: the prose lands as `reason:` on the @goal-abandon
// audit record. Mutual-exclusion via Cobra MarkFlagsMutuallyExclusive.
func newGoalAbandonCmd() *cobra.Command {
	var reasonFlag, outcomeAliasFlag string
	var jsonFlag, quietFlag, noColorFlag, forceFlag bool
	cmd := &cobra.Command{
		Use:   "abandon <goal-id>",
		Short: "Abandon an active goal (author-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			// R32 alias resolution: collapse --outcome → --reason.
			reason := reasonFlag
			if reason == "" {
				reason = outcomeAliasFlag
			}
			cwd, err := os.Getwd()
			if err == nil {
				err = runGoalAbandon(cwd, args[0], reason, forceFlag, opts)
			}
			if err != nil {
				HandleError("goal", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reasonFlag, "reason", "", "reason description (required)")
	cmd.Flags().StringVar(&outcomeAliasFlag, "outcome", "", "alias for --reason (paired-verb mirror with goal complete --outcome); on-disk field is reason:")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "bypass active-children hierarchy check (#130)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	cmd.MarkFlagsMutuallyExclusive("reason", "outcome")
	return cmd
}

// runGoalAbandon is the pure logic for `rufio goal abandon`. Mirrors
// runGoalComplete's validation-then-state-transition shape: cheap-and-
// syntactic first (reason trim), then filesystem-touching (root +
// identity + state load), and only then the actual move. Authorisation
// precedes the move so an unauthorised caller never produces a write
// side effect.
func runGoalAbandon(cwd, goalID, reasonRaw string, force bool, opts output.RenderOpts) error {
	reason := strings.TrimSpace(reasonRaw)
	if reason == "" {
		return &rufioerr.InvalidContentError{Field: "reason"}
	}

	// v1.0.5: --server routes through the remote MCP goal_abandon tool.
	// Identity (author check that gates the move) comes from the bearer
	// token; the server enforces D17.8 authz against it.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"goal_id": goalID,
			"reason":  reason,
		})
		return remoteCallAndRender("goal", "goal_abandon", args, opts)
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
	// via the disambiguation error (#147). Same posture as goal complete.
	loaded, err := goal.LoadAnyStateAs(root, goalID, me)
	if err != nil {
		return err
	}
	// R30: propagate the canonical id resolved by LoadAnyStateAs through
	// the rest of the handler. See runGoalComplete for the rationale —
	// MoveToAbandoned and ActiveChildren path-derive from goalID so a
	// short suffix here makes the downstream filesystem operations miss.
	goalID = loaded.ID
	// D17.13: state transitions are one-way. Anything not currently in
	// active (already completed/abandoned) surfaces as NoSuchGoalError —
	// the brief is unambiguous: abandon is only valid on active.
	if loaded.State != goal.StateActive {
		return &rufioerr.NoSuchGoalError{ID: goalID}
	}
	// D17.8: only the goal's original author may complete/abandon.
	if loaded.Author != me {
		return &rufioerr.GoalAuthError{ID: goalID, Author: loaded.Author}
	}
	// #130: refuse to abandon a parent while it has active children.
	// --force bypasses (rare: deprioritising a whole sub-tree where the
	// children should be cleaned up by the caller in a follow-up). Check
	// runs AFTER auth so an unauthorised caller can't probe the hierarchy.
	if !force {
		kids, err := goal.ActiveChildren(root, goalID)
		if err != nil {
			return err
		}
		if len(kids) > 0 {
			return &rufioerr.GoalActiveChildrenError{
				ID:       goalID,
				Op:       "abandon",
				Children: kids,
			}
		}
	}

	ts := versioning.NowISO()
	if err := goal.MoveToAbandoned(root, goalID, me, reason, ts); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "goal-abandon",
			"_version": "1",
			"id":       goalID,
			"by":       me,
			"reason":   reason,
			"ts":       ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteOut("abandoned: id="+goalID+" reason="+reason, opts)
	return nil
}
