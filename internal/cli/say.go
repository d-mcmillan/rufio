package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewSayCmd returns the `rufio say --channel=<ch-id> --content=<text>`
// Cobra command. Writes a single @say record to
// live/channels/active/<ch-id>/messages/<msg-id>.gdl per D16.2. Only
// current members of the channel may say (D16.3); closed channels reject
// further writes (D16.6 — surface as NoSuchChannel since they're gone
// for write purposes).
func NewSayCmd() *cobra.Command {
	var channelFlag, contentFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "say",
		Short: "Write a message to a channel",
		Long:  withIdentityEnvHelp("Write a message to a channel."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runSay(cwd, channelFlag, contentFlag, opts)
			}
			if err != nil {
				HandleError("say", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&channelFlag, "channel", "", "channel id (ch-...) to write to (required)")
	cmd.Flags().StringVar(&contentFlag, "content", "", "free-text message body (required)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runSay(cwd, channelRaw, contentRaw string, opts output.RenderOpts) error {
	// Validate BEFORE touching the filesystem. Mirrors --reason validation
	// in decline.go: empty/whitespace-only inputs surface as
	// InvalidContentError (exit 2) without consulting project root or
	// identity.
	chID := strings.TrimSpace(channelRaw)
	if chID == "" {
		return &rufioerr.InvalidContentError{Field: "channel"}
	}
	content := strings.TrimSpace(contentRaw)
	if content == "" {
		return &rufioerr.InvalidContentError{Field: "content"}
	}

	// v1.0.5: --server routes through the remote MCP say tool.
	// Identity comes from the bearer token; the server enforces
	// D16.3 membership against the resolved agent.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"channel": chID,
			"content": content,
		})
		return remoteCallAndRender("say", "say", args, opts)
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
	// D16.6: closed channels are gone for write purposes. Surface the
	// same as truly-missing channels so callers can't distinguish
	// "never existed" from "closed" — both are equally unwritable.
	if meta.Closed {
		return &rufioerr.NoSuchChannelError{ID: chID}
	}
	// D16.3: membership check. Authorisation precedes the message write
	// so an unauthorised agent never produces a write side effect.
	if !meta.IsCurrentMember(me) {
		return &rufioerr.NotChannelMemberError{ID: chID, Agent: me}
	}

	msgID, err := channels.GenerateMessageID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	record := channels.BuildSayRecord(msgID, chID, me, content, ts)
	if err := channels.WriteMessage(root, chID, msgID, record); err != nil {
		return err
	}

	if opts.JSON {
		// Issue #107: _type is "channel-message" to match the on-disk
		// record Type emitted by channels.BuildSayRecord (aligned with
		// recall.AllTypes). The CLI verb name (`rufio say`) is
		// unchanged; only the structured-output _type token shifts.
		// MCP's sayOut struct mirrors this for CLI/MCP fidelity.
		payload := map[string]interface{}{
			"_type":    "channel-message",
			"_version": "1",
			"id":       msgID,
			"channel":  chID,
			"by":       me,
			"content":  content,
			"ts":       ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`. Pre-H3d
	// this was "said: ..." (past-tense form).
	output.WriteOut("say: id="+msgID+" channel="+chID, opts)
	return nil
}
