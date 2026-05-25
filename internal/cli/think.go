package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewThinkCmd returns the `rufio think` Cobra command. Writes a single
// @thought record (and a sibling @context-bundle for --type=decision) to
// live/outbox/<me>/<id>.gdl. Append-only; each invocation produces a new
// <unix-millis>-<rand6> thought-id.
//
//	rufio think --type=<...> --subject=<entity> --content=<text>
//	            --scope=<...> [--ttl=<seconds>] [--parent=<thought-id>]
//	            [--topics=<csv>]
func NewThinkCmd() *cobra.Command {
	var typeFlag, subjectFlag, contentFlag, scopeFlag string
	var ttlFlag, parentFlag, topicsFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "think",
		Short: "Write a thought (ambient broadcast) to live/outbox/",
		Long:  withIdentityEnvHelp("Write a thought (ambient broadcast) to live/outbox/."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runThink(cwd, thinkArgs{
					Type: typeFlag, Subject: subjectFlag, Content: contentFlag,
					Scope: scopeFlag, TTL: ttlFlag, Parent: parentFlag, Topics: topicsFlag,
				}, opts)
			}
			if err != nil {
				HandleError("think", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typeFlag, "type", "", "thought type (hypothesis|observation|decision|question|focus) (required)")
	cmd.Flags().StringVar(&subjectFlag, "subject", "", "entity id this thought is about, namespace:local e.g. customer:5821 (required)")
	cmd.Flags().StringVar(&contentFlag, "content", "", "free-text content of the thought (required)")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "visibility scope (agent|deployment|fleet); default fleet")
	// --ttl is semantically an integer seconds count (the existing
	// parser is strconv.Atoi); render it as `--ttl int` in --help
	// so cold agents don't pass `5m` or `1h` (issue #123). Parsing
	// + sign-check stay in thought.ParseTTL so the error contract
	// ("invalid --ttl" for 0/negative/non-integer) is preserved.
	cmd.Flags().Var(&stringValueWithType{raw: &ttlFlag, typeName: "int"}, "ttl", "expiry in seconds (optional; default never expires)")
	cmd.Flags().StringVar(&parentFlag, "parent", "", "parent thought id (optional)")
	cmd.Flags().StringVar(&topicsFlag, "topics", "", "comma-separated topic tokens labelling this thought (plural; record-side labels — distinct from `summon --topic` which names a channel)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

type thinkArgs struct {
	Type, Subject, Content, Scope string
	TTL, Parent, Topics           string
}

func runThink(cwd string, a thinkArgs, opts output.RenderOpts) error {
	// Validate BEFORE touching the filesystem (design §4.D).
	content := strings.TrimSpace(a.Content)
	if err := thought.ValidateContent(content); err != nil {
		return err
	}
	if err := thought.ValidateType(a.Type); err != nil {
		return err
	}
	if err := thought.ValidateSubject(a.Subject); err != nil {
		return err
	}
	// H3a (#125): default empty --scope to fleet. Pre-H3a think rejected
	// an empty --scope; the unified rule is "Write verbs default to
	// --scope=fleet; pass --scope=agent for private." A typo
	// (--scope=team) still errors at write time so a non-empty value
	// must validate against the canonical enum.
	scope := strings.TrimSpace(a.Scope)
	if scope == "" {
		scope = "fleet"
	}
	if err := thought.ValidateScope(scope); err != nil {
		return err
	}
	a.Scope = scope
	ttl, err := thought.ParseTTL(a.TTL)
	if err != nil {
		return err
	}
	if err := thought.ValidateParent(a.Parent); err != nil {
		return err
	}
	topics := splitCSVTrim(a.Topics)
	if err := attention.ValidateTopics(topics); err != nil {
		return err
	}

	// v1.0.4: --server dispatches through the remote MCP think tool.
	// Identity comes from the bearer token's resolution, not local
	// identity.Resolve. ttl_seconds maps the CLI's --ttl flag onto the
	// MCP tool's named field; subject/scope/parent/topics passthrough.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"type":    a.Type,
			"subject": a.Subject,
			"content": content,
			"scope":   scope,
			"topics":  topics,
			"parent":  a.Parent,
			"ttl":     a.TTL,
		})
		return remoteCallAndRender("think", "think", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	agent, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	id, err := thought.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()

	thoughtRec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: agent, Type: a.Type, Subject: a.Subject,
		Content: content, Scope: a.Scope, Topics: topics,
		TS: ts, TTL: ttl, Parent: a.Parent,
	})

	records := []gdl.Record{thoughtRec}
	var bundleRefs []string
	if a.Type == "decision" {
		bundleRefs, err = thought.CollectGivenLearnedSHAs(root)
		if err != nil {
			return err
		}
		records = append(records, thought.BuildContextBundle(id, bundleRefs))
	}

	if err := thought.Write(root, agent, id, records); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":       "think",
			"_version":    "1",
			"id":          id,
			"author":      agent,
			"type":        a.Type,
			"subject":     a.Subject,
			"content":     content,
			"scope":       a.Scope,
			"ts":          ts,
			"ttl":         ttl,
			"bundle_refs": bundleRefsOrEmpty(bundleRefs),
		}
		if topics == nil {
			payload["topics"] = []string{}
		} else {
			payload["topics"] = topics
		}
		if a.Parent != "" {
			payload["parent"] = a.Parent
		} else {
			payload["parent"] = nil
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("think: id="+id+" type="+a.Type+" subject="+a.Subject+" scope="+a.Scope, opts)
	return nil
}

// bundleRefsOrEmpty normalises nil → []string{} for JSON output so the
// `bundle_refs` field is always an array (never null).
func bundleRefsOrEmpty(refs []string) []string {
	if refs == nil {
		return []string{}
	}
	return refs
}
