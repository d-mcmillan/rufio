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

// NewCloseCmd returns the `rufio close <channel-id>` Cobra command.
// Appends a @channel-close record to active/<ch-id>/meta.gdl AND renames
// active/<ch-id>/ → closed/<ch-id>/ atomically under channel-<ch-id>.lock
// per D16.5. Only the opener may close (D16.5). Closed channels surface as
// NoSuchChannel on subsequent close attempts per D16.6 — once gone, gone.
//
// The exported constructor is NewCloseCmd rather than NewClose to mirror
// every other command in this package; the inner function is runClose to
// avoid colliding with Go's built-in close() (which is a keyword in many
// contexts but a usable identifier here — we'd rather not test the
// boundary).
func NewCloseCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "close <channel-id>",
		Short: "Close a channel (opener only, archives to closed/)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runClose(cwd, args[0], opts)
			}
			if err != nil {
				HandleError("close", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runClose(cwd, chID string, opts output.RenderOpts) error {
	// v1.0.5: --server routes through the remote MCP close tool.
	// Identity (the opener check that gates close) comes from the
	// bearer token; the server enforces D16.5 authz against it.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"channel": chID,
		})
		return remoteCallAndRender("close", "close", args, opts)
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
	// D16.6: an already-closed channel is treated as not-found from the
	// close caller's perspective. Surface the same as a channel that
	// never existed — once gone, gone.
	if meta.Closed {
		return &rufioerr.NoSuchChannelError{ID: chID}
	}
	// D16.5: only the opener may close. Authorisation precedes the
	// append-and-rename so a rejected attempt has zero side effect.
	if me != meta.Opener {
		return &rufioerr.NotChannelOpenerError{ID: chID, Agent: me, Opener: meta.Opener}
	}

	ts := versioning.NowISO()
	if err := channels.AppendClose(root, chID, me, ts); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "channel-close",
			"_version": "1",
			"channel":  chID,
			"by":       me,
			"ts":       ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteOut("closed: channel="+chID, opts)
	return nil
}
