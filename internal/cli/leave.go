package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewLeaveCmd returns the `rufio leave <channel-id>` Cobra command.
// Appends a @channel-leave record to
// live/channels/active/<ch-id>/meta.gdl per D16.4. Per D16.14, BOTH
// the opener AND the target may leave — only close is opener-only.
// Closed channels surface as NoSuchChannel per D16.6: they are gone
// for write purposes regardless of whether the caller was a former
// member.
//
// Idempotent at the lib layer (channels.AppendLeave): a second leave
// by the same agent does NOT append a duplicate record.
func NewLeaveCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "leave <channel-id>",
		Short: "Leave a channel (audit trail preserved)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runLeave(cwd, args[0], opts)
			}
			if err != nil {
				HandleError("leave", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runLeave(cwd, chID string, opts output.RenderOpts) error {
	// v1.0.5: --server routes through the remote MCP leave tool.
	// Identity (the agent leaving) comes from the bearer token.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"channel": chID,
		})
		return remoteCallAndRender("leave", "leave", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	me, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	meta, err := channels.LoadMeta(root, chID)
	if err != nil {
		return err
	}
	// D16.6: closed channels are gone for write purposes. Surface as
	// NoSuchChannelError so callers can't distinguish "never existed"
	// from "already closed" — both are equally unwritable.
	if meta.Closed {
		return &rufioerr.NoSuchChannelError{ID: chID}
	}
	// D16.14: BOTH opener and target may leave. Anyone else trying to
	// leave is a third party who was never a member — reject with
	// NotChannelMemberError. We intentionally do NOT consult IsCurrentMember
	// here so that a previously-left agent who runs `rufio leave` again
	// flows into the idempotent AppendLeave path rather than getting a
	// hard error (matches D16.4: "second leave by same agent is a no-op").
	if me != meta.Opener && me != meta.Target {
		return &rufioerr.NotChannelMemberError{ID: chID, Agent: me}
	}

	ts := versioning.NowISO()
	if err := channels.AppendLeave(root, chID, me, ts); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "channel-leave",
			"_version": "1",
			"channel":  chID,
			"by":       me,
			"ts":       ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteOut("left: channel="+chID, opts)
	return nil
}
