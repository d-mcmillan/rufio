package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewRetractCmd returns the `rufio retract <thought-id> --reason=<text>`
// Cobra command. Writes live/retracted/<thought-id>.gdl with an @retract
// record. Only the author of the thought can retract.
func NewRetractCmd() *cobra.Command {
	var reasonFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "retract <thought-id>",
		Short: "Retract one of the current agent's thoughts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runRetract(cwd, args[0], reasonFlag, opts)
			}
			if err != nil {
				HandleError("retract", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reasonFlag, "reason", "", "free-text reason for retraction (required)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runRetract(cwd, targetID, reasonRaw string, opts output.RenderOpts) error {
	reason := strings.TrimSpace(reasonRaw)
	if reason == "" {
		return &rufioerr.InvalidContentError{Field: "reason"}
	}

	// v1.0.5: --server routes through the remote MCP retract tool.
	// The server resolves identity from the bearer token and enforces
	// the author-only authz check. We do NOT resolve short-id locally
	// in remote mode — the server holds the canonical substrate.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"thought_id": targetID,
			"reason":     reason,
		})
		return remoteCallAndRender("retract", "retract", args, opts)
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
	// Resolve cascades the privacy floor — but since retract is
	// author-only (RetractAuthorError below catches the mismatch), the
	// privacy filter is a defence-in-depth here, not the primary gate.
	targetID, err = retract.Resolve(root, targetID, agent)
	if err != nil {
		return err
	}

	author, err := retract.Lookup(root, targetID)
	if err != nil {
		return err
	}
	if author != agent {
		return &rufioerr.RetractAuthorError{ID: targetID, Author: author}
	}

	ts := versioning.NowISO()
	rec := retract.BuildRecord(targetID, reason, agent, ts)
	if err := retract.Write(root, targetID, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "retract",
			"_version": "1",
			"target":   targetID,
			"reason":   reason,
			"by":       agent,
			"ts":       ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("retract: target="+targetID+" by="+agent, opts)
	return nil
}
