package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewAcceptCmd returns the `rufio accept <summon-id>` Cobra command.
// Moves live/summons/pending/<id>.gdl to live/summons/accepted/<id>.gdl
// with an appended @accept record (D15.4) AND creates the channel meta
// file at live/channels/active/<ch-id>/meta.gdl (D15.7). Only the
// summon's target may accept (D15.9); already-handled summons surface
// as NoSuchSummon (D15.10).
func NewAcceptCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "accept <summon-id>",
		Short: "Accept a pending summon and open the channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runAccept(cwd, args[0], opts)
			}
			if err != nil {
				HandleError("accept", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runAccept(cwd, summonID string, opts output.RenderOpts) error {
	// v1.0.5: --server routes through the remote MCP accept tool.
	// Identity (the "me" check that gates the accept) comes from
	// the bearer token; the server enforces D15.9 authz against it.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"summon_id": summonID,
		})
		return remoteCallAndRender("accept", "accept", args, opts)
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
	// D15.10: accept is only valid against pending. Any other state
	// (accepted/declined/expired) surfaces as NoSuchSummon — idempotency
	// is "the second caller can't tell what happened first."
	if loaded.State != summon.StatePending {
		return &rufioerr.NoSuchSummonError{ID: summonID}
	}
	// D15.9: only the summon's target may respond. Auth precedes any
	// write so an unauthorised caller cannot produce side effects.
	if loaded.To != me {
		return &rufioerr.SummonAuthError{ID: summonID, Target: loaded.To}
	}

	chID, err := channels.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()

	// Order of writes (D15.6): channel meta FIRST, then state move.
	//
	// Rationale: if MoveToAccepted fails (race lost, or filesystem
	// error), the channel meta we just wrote becomes an ORPHAN — no
	// @accept record references the ch-id, and the summon stays in
	// whatever state the racing caller left it. Per D15.6 / D15.10 we
	// accept this leak: ch-ids are freshly minted, unreferenced, and
	// won't be picked up by future close/leave commands. The inverse
	// order (state move first) would create the opposite hazard: a
	// summon claiming a channel that never got materialised, which
	// downstream `say` callers would treat as a hard error.
	metaRec := channels.BuildMetaRecord(chID, loaded.From, loaded.To, loaded.Topic, loaded.Intent, ts)
	if err := channels.WriteMeta(root, chID, metaRec); err != nil {
		return err
	}

	if err := summon.MoveToAccepted(root, summonID, me, chID, ts); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":     "accept",
			"_version":  "1",
			"summon-id": summonID,
			"channel":   chID,
			"by":        me,
			"ts":        ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("accept: summon-id="+summonID+" channel="+chID, opts)
	return nil
}
