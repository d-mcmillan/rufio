package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewSummonCmd returns the `rufio summon <agent-id> --topic=<topic>
// --intent=<text>` Cobra command. Writes
// live/summons/pending/<summon-id>.gdl with an @summon record. Hardcoded
// 24h TTL per D15.2 (config in v1.1). The opener identity comes from the
// usual env > .rufio/identity.local.gdl chain.
func NewSummonCmd() *cobra.Command {
	var topicFlag, intentFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "summon <agent-id>",
		Short: "Open a private channel by summoning another agent",
		Long:  withIdentityEnvHelp("Open a private channel by summoning another agent."),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runSummon(cwd, args[0], topicFlag, intentFlag, opts)
			}
			if err != nil {
				HandleError("summon", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&topicFlag, "topic", "", "channel topic — singular; the conversation subject for THIS channel (required). Distinct from attend/think/observe/reason `--topics` which are plural record-labels")
	cmd.Flags().StringVar(&intentFlag, "intent", "", "free-text reason for the summon (required)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runSummon(cwd, to, topic, intent string, opts output.RenderOpts) error {
	// Validate BEFORE touching the filesystem (design §4.D).
	if err := summon.ValidateTopic(topic); err != nil {
		return err
	}
	if err := summon.ValidateIntent(intent); err != nil {
		return err
	}

	// v1.0.5: --server routes through the remote MCP summon tool.
	// Identity (from) comes from the bearer token.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"to":     to,
			"topic":  topic,
			"intent": intent,
		})
		return remoteCallAndRender("summon", "summon", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	from, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	id, err := summon.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	ttl := summon.DefaultTTL

	rec := summon.BuildSummonRecord(id, from, to, topic, intent, ts, ttl)
	if err := summon.WritePending(root, id, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "summon",
			"_version": "1",
			"id":       id,
			"from":     from,
			"to":       to,
			"topic":    topic,
			"intent":   intent,
			"ts":       ts,
			"ttl":      ttl,
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`. Pre-H3d this
	// was "summoned: ..." (past-tense form). The unified rule is the
	// literal CLI verb so grep is predictable across surfaces.
	output.WriteOut("summon: id="+id+" to="+to+" topic="+topic, opts)
	return nil
}
