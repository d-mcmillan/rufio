package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// NewRecallCmd returns the `rufio recall` Cobra command. Scans the
// corpus across given/learned/live namespaces and renders matching
// records.
//
//	rufio recall [<query>] [--scope=...] [--types=...] [--thought-types=...]
//	             [--topics=...] [--since=...] [--as-of=...]
//	             [--include-expired] [--json]
//
// --types= selects RECORD types (thought, observation, reason, summon,
// confirm, refute, retract, goal, channel-message, given, learned).
// --thought-types= (P3/R31) filters WITHIN --types=thought by
// thought-subtype (decision|hypothesis|focus|question|observation).
// Passing a thought-subtype to --types= directly returns a helpful
// redirect error pointing to the corrected shape.
//
// --topics=<csv> (#180) ANY-matches against the on-disk `topics:` field
// of @thought / @observation records — symmetric with the write verbs
// (attend/think/observe) which all accept --topics= to tag records.
// Records without a topics: field are excluded when --topics= is set.
func NewRecallCmd() *cobra.Command {
	var scopeFlag, typesFlag, thoughtTypesFlag, topicsFlag, sinceFlag, asOfFlag string
	var includeExpiredFlag, jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "recall [query]",
		Short: "Scan corpus across given/learned/live and render matching records",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			cwd, err := os.Getwd()
			if err == nil {
				err = runRecall(cwd, recallArgs{
					Query: query, Scope: scopeFlag, Types: typesFlag,
					ThoughtTypes: thoughtTypesFlag,
					Topics:       topicsFlag,
					Since:        sinceFlag, AsOf: asOfFlag,
					IncludeExpired: includeExpiredFlag,
				}, opts)
			}
			if err != nil {
				HandleError("recall", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "scope filter (agent|deployment|fleet)")
	cmd.Flags().StringVar(&typesFlag, "types", "", "CSV of record types to include (default: all). Pass thought-subtypes via --thought-types instead")
	cmd.Flags().StringVar(&thoughtTypesFlag, "thought-types", "", "CSV of thought-subtypes to filter within --types=thought (decision|hypothesis|focus|question|observation)")
	cmd.Flags().StringVar(&topicsFlag, "topics", "", "CSV of topic tokens; ANY-match against the record's topics: field (mirror of write verbs' --topics; records without topics are excluded when set)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "include only records younger than the Go duration")
	cmd.Flags().StringVar(&asOfFlag, "as-of", "", "RFC3339 timestamp; exclude records newer than this")
	cmd.Flags().BoolVar(&includeExpiredFlag, "include-expired", false, "also surface retracted and TTL-expired records (default: hide both)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

type recallArgs struct {
	Query, Scope, Types, Since, AsOf string
	// ThoughtTypes (P3/R31) — CSV of thought-subtypes (decision|hypothesis|
	// focus|question|observation) used to filter WITHIN --types=thought.
	// Distinct from --types= (which selects record-types). Empty means
	// "no thought-subtype filter".
	ThoughtTypes string
	// Topics (#180) — CSV of topic tokens; ANY-match against the
	// record's on-disk `topics:` field. Mirrors the write verbs'
	// --topics= (attend/think/observe). Empty means "no topic filter".
	Topics         string
	IncludeExpired bool
}

func runRecall(cwd string, a recallArgs, opts output.RenderOpts) error {
	// Parse + validate flags BEFORE touching the FS.
	types, err := recall.ValidateTypes(a.Types)
	if err != nil {
		return err
	}
	// P3/R31: validate --thought-types (separate enum: decision|
	// hypothesis|focus|question|observation). Empty/whitespace → nil
	// (no filter). Errors before any FS read so a typo is caught
	// fast.
	thoughtTypes, err := recall.ValidateThoughtTypes(a.ThoughtTypes)
	if err != nil {
		return err
	}
	if a.Scope != "" {
		if err := thought.ValidateScope(a.Scope); err != nil {
			return err
		}
	}
	// #180: parse --topics= CSV via the same splitCSVTrim helper the
	// write verbs use, so the read-side parsing is byte-for-byte
	// symmetric. Empty/whitespace → nil → no filter (regression-safe).
	topics := splitCSVTrim(a.Topics)
	since, err := recall.ParseSince(a.Since)
	if err != nil {
		return err
	}
	asof, err := recall.ParseAsOf(a.AsOf)
	if err != nil {
		return err
	}

	// v1.0.4: --server routes recall through the remote MCP tool. The
	// server's privacy.IsVisible is the floor regardless of what the
	// client thinks "scope" should mean — identity comes from the
	// bearer token, not RUFIO_AGENT_ID.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"query":           a.Query,
			"scope":           a.Scope,
			"types":           a.Types,
			"since":           a.Since,
			"as_of":           a.AsOf,
			"include_expired": a.IncludeExpired,
		})
		return remoteCallAndRender("recall", "recall", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	// Identity resolution is best-effort — recall works for an
	// unidentified user (CurrentAgent="" → scope=agent filter excludes
	// all agent-scoped records, which is the safe default).
	currentAgent, _, _ := identity.Resolve(root)

	// Always include retraction markers from scan: Filter uses the
	// Retracted flag to gate visibility via IncludeExpired. Without this,
	// default recall could not hide retracted records.
	records, err := recall.Scan(root, true)
	if err != nil {
		return err
	}

	filtered := recall.Filter(records, recall.FilterParams{
		Types: types, ThoughtTypes: thoughtTypes,
		Topics: topics,
		Scope:  a.Scope, Since: since, AsOf: asof,
		IncludeExpired: a.IncludeExpired, CurrentAgent: currentAgent,
	})
	matched := recall.Match(filtered, a.Query)

	if opts.JSON {
		// Pass root so the path field renders root-relative
		// (security audit H2 + Bonus).
		return recall.RenderJSON(os.Stdout, root, matched)
	}
	return recall.RenderColumnar(os.Stdout, matched)
}
