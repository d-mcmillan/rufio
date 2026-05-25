package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewAttendCmd returns the `rufio attend` Cobra command. Writes the current
// agent's attention to live/attention/<me>.gdl (overwrite, current-state).
//
//	rufio attend --intent=<text> --entities=<csv> [--topics=<csv>] [--scope=<...>]
//
// Required identity (env > .rufio/identity.local.gdl). Exit 1 if neither set.
//
// --scope is part of the #125 verb-pattern-consistency fix. Every other
// write verb (think/observe/reason/goal) accepts --scope; before #125
// attend rejected it with "unknown flag", which silently let agents who
// learned the pattern from a peer verb write attentions with the wrong
// (or absent) scope without realising. Default is "fleet" — attention is
// a broadcast primitive, so the safe default matches its intent.
func NewAttendCmd() *cobra.Command {
	var intentFlag, entitiesFlag, topicsFlag, scopeFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "attend",
		Short: "Declare what the current agent is attending to",
		Long:  withIdentityEnvHelp("Declare what the current agent is attending to."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runAttend(cwd, attendArgs{
					Intent: intentFlag, Entities: entitiesFlag,
					Topics: topicsFlag, Scope: scopeFlag,
				}, opts)
			}
			if err != nil {
				HandleError("attend", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&intentFlag, "intent", "", "free-text description of current intent (required)")
	cmd.Flags().StringVar(&entitiesFlag, "entities", "", "comma-separated entity ids in `namespace:local` form (regex: [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+; e.g. topic:freewill, customer:5821) (required, ≥1)")
	cmd.Flags().StringVar(&topicsFlag, "topics", "", "comma-separated topic tokens labelling this attention (plural; record-side labels — distinct from `summon --topic` which names a channel)")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "visibility scope (agent|deployment|fleet); default fleet")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter (no effect on --json or written record)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// attendArgs collects the runAttend inputs. Moved off positional
// parameters in #125 so the new --scope flag composes the same way the
// other verbs (reason, goal, …) compose their flag bundles.
type attendArgs struct {
	Intent, Entities, Topics, Scope string
}

func runAttend(cwd string, a attendArgs, opts output.RenderOpts) error {
	// Validate BEFORE touching the filesystem (design §4.D).
	intent := strings.TrimSpace(a.Intent)
	if err := attention.ValidateIntent(intent); err != nil {
		return err
	}
	entities := splitCSVTrim(a.Entities)
	if err := attention.ValidateEntities(entities); err != nil {
		return err
	}
	topics := splitCSVTrim(a.Topics)
	if err := attention.ValidateTopics(topics); err != nil {
		return err
	}
	// #125: default to "fleet" when the flag is omitted (attention is a
	// broadcast primitive). Explicit non-empty values must validate
	// against the canonical enum so a typo (e.g. --scope=team) errors
	// at write-time, not silently lands as a malformed record.
	scope := strings.TrimSpace(a.Scope)
	if scope == "" {
		scope = "fleet"
	}
	if err := thought.ValidateScope(scope); err != nil {
		return err
	}

	// v1.0.4: when --server is set, dispatch through the remote MCP
	// tool with the matching name. Identity comes from the server's
	// token resolution, NOT RUFIO_AGENT_ID.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"intent":   intent,
			"entities": entities,
			"topics":   topics,
			"scope":    scope,
		})
		return remoteCallAndRender("attend", "attend", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	agent, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	// Issue #108: warn on stderr when overwriting an existing attention
	// record. Catches the cold-agent foot-gun where an inherited identity
	// silently stomps another agent's record. Best-effort: NoAttentionError
	// is the "no prior record" signal (silent), any other read/parse error
	// is swallowed so a corrupted file doesn't add a second noisy stderr
	// line on top of the write-path failure.
	if !opts.Quiet {
		prior, loadErr := attention.LoadOne(root, agent)
		var none *rufioerr.NoAttentionError
		switch {
		case loadErr == nil:
			fmt.Fprintf(os.Stderr,
				"attend: previous attention record from %s (%q) — overwriting\n",
				prior.TS, prior.Intent)
		case errors.As(loadErr, &none):
			// No prior record — silent, fall through.
		default:
			// Read/parse error — swallow; the write below will likely fail
			// and surface a clearer error.
		}
	}

	ts := versioning.NowISO()
	rec := attention.BuildRecord(agent, intent, scope, entities, topics, ts)
	if err := attention.Write(root, agent, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":    "attend-set",
			"_version": "1",
			"agent":    agent,
			"intent":   intent,
			"scope":    scope,
			"entities": entities,
			"ts":       ts,
		}
		// Always render topics as a (possibly empty) array — never null,
		// never absent — so JSON consumers can iterate without nil-checks.
		if topics == nil {
			payload["topics"] = []string{}
		} else {
			payload["topics"] = topics
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`. Pre-H3d this
	// was "attention set: ...", which forced cold agents to learn a
	// different prefix for every verb.
	output.WriteOut("attend: agent="+agent+" intent="+intent+" scope="+scope, opts)
	return nil
}

// splitCSVTrim splits a CSV flag value on commas and trims whitespace per
// token. Empty input → nil. Empty tokens (from trailing/double commas)
// survive as "" entries so Validate* can reject them with a clear message.
func splitCSVTrim(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}
