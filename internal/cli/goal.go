// Package cli — `rufio goal`.
//
// Goal is a one-author-owned coordination primitive (D17). The parent
// command both:
//
//   - Writes a new active goal when invoked with `--statement=<text>`.
//   - Hosts the `complete` and `abandon` subcommands (added in later
//     tasks within this PR).
//
// The Cobra-parent-with-its-own-RunE shape is deliberate: spec §189 locks
// `rufio goal --statement=<text>` as the write entrypoint, NOT
// `rufio goal create`. Subcommands sit under the same parent without
// shadowing the write path because complete/abandon take a positional
// goal-id arg and the parent uses `cobra.NoArgs` — there's no ambiguity.
//
// On-disk shape: a single-record file at live/goals/active/<goal-id>.gdl
// containing one @goal record. The goal-id format mirrors thought/summon
// (<unix-millis>-<rand6>) so generic id-shaped parsers handle goals
// uniformly. See internal/lib/goal/goal.go.
package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// goalShortIDSuffix matches the 6-char [a-z0-9] suffix shape — what
// `goals list` and the TUI surface as the abbreviated form of a goal
// id. Used by --parent's R30 pre-validate resolver hop to detect
// "user pasted the short form" before the strict canonical-shape
// regex in thought.ValidateParent runs.
var goalShortIDSuffix = regexp.MustCompile(`^[a-z0-9]{6}$`)

// shortIDLooksLikeSuffix is the thin predicate wrapper used by
// runGoalWrite. Mirrors retract.LooksLikeShortID — kept local to avoid
// the cli → retract package coupling for a one-line regex check.
func shortIDLooksLikeSuffix(s string) bool {
	return goalShortIDSuffix.MatchString(s)
}

// NewGoalCmd returns the `rufio goal` Cobra command. The parent has both
// its own write-side RunE (--statement=<text>) AND will host the
// complete/abandon subcommands (registered in Tasks 5+6).
func NewGoalCmd() *cobra.Command {
	var statementFlag, byFlag, parentFlag, scopeFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "goal",
		Short: "Declare a coordination goal (or operate on existing goals via complete/abandon)",
		Long:  withIdentityEnvHelp("Declare a coordination goal (or operate on existing goals via complete/abandon)."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runGoalWrite(cwd, statementFlag, byFlag, parentFlag, scopeFlag, opts)
			}
			if err != nil {
				HandleError("goal", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&statementFlag, "statement", "", "goal statement (required when not using a subcommand)")
	cmd.Flags().StringVar(&byFlag, "by", "", "deadline (free-text in v1; e.g. 'EOW', '2026-06-01', '2 weeks')")
	cmd.Flags().StringVar(&parentFlag, "parent", "", "parent goal id (optional)")
	// H3a (#125): default changed from "agent" → "fleet" so goal matches
	// the unified write-verb rule (broadcast default, --scope=agent
	// opt-in for private). The pflag default "fleet" auto-renders in
	// --help via `(default "fleet")`; NB: do NOT add "; default fleet"
	// to the description — pflag would print it twice (#157).
	cmd.Flags().StringVar(&scopeFlag, "scope", "fleet", "visibility scope (agent|deployment|fleet)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	// Subcommands.
	cmd.AddCommand(newGoalCompleteCmd())
	cmd.AddCommand(newGoalAbandonCmd())
	return cmd
}

// runGoalWrite validates inputs, resolves identity + root, then writes
// the @goal record to live/goals/active/<id>.gdl. Validation order
// (design §4.D): cheap-and-syntactic first, then filesystem-touching.
func runGoalWrite(cwd, statement, by, parentID, scope string, opts output.RenderOpts) error {
	if err := goal.ValidateStatement(statement); err != nil {
		return err
	}
	// H3a (#125): default empty scope to fleet. pflag's StringVar default
	// fills "fleet" for direct CLI use; this defensive default ALSO
	// handles in-process callers (tests, SDK) that invoke runGoalWrite
	// with an explicit "". Validate post-default so a typo like "team"
	// still errors at write-time.
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "fleet"
	}
	if err := thought.ValidateScope(scope); err != nil {
		return err
	}

	// v1.0.5: --server routes through the remote MCP goal tool.
	// Identity (author) comes from the bearer token. We do NOT
	// run short-id parent resolution locally in remote mode — the
	// server holds the canonical substrate.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"statement": strings.TrimSpace(statement),
			"by":        by,
			"parent":    parentID,
			"scope":     scope,
		})
		return remoteCallAndRender("goal", "goal", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	author, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	// R30: --parent accepts the 6-char [a-z0-9]{6} suffix that
	// `goals list` / TUI render in text mode. Resolve BEFORE the
	// canonical-shape regex check (thought.ValidateParent) so the
	// regex stays strict — no widening of the wire-level contract —
	// but the user can paste back what they read on screen. Empty
	// passes through unchanged.
	//
	// Resolution surfaces:
	//   - canonical full id → no I/O, pass through to ValidateParent.
	//   - 6-char suffix → resolveSuffix walks live/goals/{active,
	//     completed,abandoned}/*-<suffix>.gdl, filtered by the privacy
	//     floor (#147) against the resolved author.
	//   - any other shape → pass through to ValidateParent for the
	//     existing InvalidParentError surface (exit 2).
	if parentID != "" && shortIDLooksLikeSuffix(parentID) {
		canonical, err := goal.ResolveSuffixAs(root, parentID, author)
		if err != nil {
			return err
		}
		parentID = canonical
	}
	if err := thought.ValidateParent(parentID); err != nil {
		return err
	}

	// #133: --parent must reference a goal that ACTUALLY EXISTS in
	// live/goals/{active,completed,abandoned}/. Before this check a
	// format-valid-but-absent id was silently accepted and a dangling
	// child reference was written. The contract now mirrors what
	// `reason --decision` enforces via reason.ValidateDecisionTarget:
	// pure shape regex first (ValidateParent above, exit 2 on bad
	// shape) → existence next (NoSuchGoalError, exit 1 on miss).
	//
	// This block also subsumes the #130 cross-author warning that
	// previously did its own load. Consolidating into a single
	// LoadAnyStateAs call (a) avoids the double-disk-walk and (b)
	// ensures the missing-parent rejection takes precedence over the
	// warning path — a missing parent never reaches the warning code.
	//
	// Privacy floor (#147): an other-author scope:agent parent must
	// NOT be observable by a non-author. LoadAnyStateAs's suffix path
	// already filters via privacy.IsVisible, but the canonical-id path
	// (full <millis>-<rand6>) reads the file directly. After the load
	// we re-check IsVisible and map a hit on a hidden record to the
	// same *NoSuchGoalError surface as a real miss — so existence
	// cannot be probed by id-guessing.
	if parentID != "" {
		parentGoal, loadErr := goal.LoadAnyStateAs(root, parentID, author)
		if loadErr != nil {
			return loadErr
		}
		// Privacy floor: pretend the file isn't there if the caller
		// isn't allowed to see it. Same error shape as a real miss.
		if !privacy.IsVisible(parentGoal, author) {
			return &rufioerr.NoSuchGoalError{ID: parentID}
		}
		// #130 cross-author advisory warning (collaborative attach is
		// allowed — emit so the relationship isn't established silently).
		// --quiet suppresses, mirroring the attend-overwrite warning (#108).
		if !opts.Quiet && parentGoal.Author != "" && parentGoal.Author != author {
			fmt.Fprintf(os.Stderr,
				"rufio goal: warning — attaching child to goal authored by %q (not you); ensure the parent author expects this\n",
				parentGoal.Author,
			)
		}
	}

	id, err := goal.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	statement = strings.TrimSpace(statement)
	rec := goal.BuildGoalRecord(id, author, statement, by, parentID, scope, ts)
	if err := goal.WriteActive(root, id, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":     "goal",
			"_version":  "1",
			"id":        id,
			"author":    author,
			"statement": statement,
			"scope":     scope,
			"ts":        ts,
		}
		// `by` and `parent` are always present in the JSON payload —
		// nil when absent so consumers don't need a key-present check
		// (matches think's parent contract per D5.12).
		if by != "" {
			payload["by"] = by
		} else {
			payload["by"] = nil
		}
		if parentID != "" {
			payload["parent"] = parentID
		} else {
			payload["parent"] = nil
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteOut("goal: id="+id+" scope="+scope, opts)
	return nil
}
