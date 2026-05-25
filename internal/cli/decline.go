package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewDeclineCmd returns the `rufio decline <summon-id> --reason=<text>`
// Cobra command. Moves live/summons/pending/<id>.gdl to
// live/summons/declined/<id>.gdl and appends an @decline record per
// D15.5. Only the target of the summon may decline (D15.9); accept and
// decline are mutually exclusive transitions out of pending (D15.10).
func NewDeclineCmd() *cobra.Command {
	var reasonFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "decline <summon-id>",
		Short: "Decline a pending summon addressed to the current agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runDecline(cwd, args[0], reasonFlag, opts)
			}
			if err != nil {
				HandleError("decline", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reasonFlag, "reason", "", "free-text reason for declining (required)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runDecline(cwd, summonID, reasonRaw string, opts output.RenderOpts) error {
	reason := strings.TrimSpace(reasonRaw)
	if reason == "" {
		return &rufioerr.InvalidContentError{Field: "reason"}
	}

	// v1.0.5: --server routes through the remote MCP decline tool.
	// Identity (the "me" check that gates the decline) comes from
	// the bearer token; the server enforces D15.9 authz against it.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"summon_id": summonID,
			"reason":    reason,
		})
		return remoteCallAndRender("decline", "decline", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	me, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	loaded, err := summon.LoadAnyState(root, summonID)
	if err != nil {
		return err
	}
	// D15.10: accept/decline are only valid against pending summons. Any
	// other state (accepted/declined/expired) surfaces as NoSuchSummon —
	// the brief is unambiguous: "only valid on pending."
	if loaded.State != summon.StatePending {
		return &rufioerr.NoSuchSummonError{ID: summonID}
	}
	// D15.9: only the summon's target may respond. Authorisation precedes
	// the state move so an unauthorised agent never produces a write side
	// effect.
	if loaded.To != me {
		return &rufioerr.SummonAuthError{ID: summonID, Target: loaded.To}
	}

	ts := versioning.NowISO()
	if err := summon.MoveToDeclined(root, summonID, me, reason, ts); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":     "decline",
			"_version":  "1",
			"summon-id": summonID,
			"by":        me,
			"reason":    reason,
			"ts":        ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("decline: summon-id="+summonID+" reason="+reason, opts)
	return nil
}
