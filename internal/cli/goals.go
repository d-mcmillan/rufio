// Package cli — `rufio goals list`.
//
// `goals` is a read-only parent command: it inspects goals across the
// whole project, with no write-side of its own (unlike `goal`, which both
// writes new goals and hosts complete/abandon). The shape mirrors
// `summons` (parent + `list` subcommand) so users get a consistent
// inspection pattern.
//
// Filters:
//   - `--scope=<agent|deployment|fleet>` validated against the same enum
//     `thought.ValidateScope` uses; empty means no filter.
//   - `--state=<active|completed|abandoned>` validated against
//     `goal.State*`; empty means all states.
//
// Output mirrors `summons list`:
//   - Columnar: one tab-separated line per goal with the truncated
//     statement quoted (intentTruncateLen, shared with summons).
//   - JSONL: one object per goal with locked `_type=goal`, `_version=1`
//     and the audit-derived fields (outcome / reason / *_by / *_at)
//     populated when state != active.
package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// NewGoalsCmd returns the `rufio goals` parent Cobra command.
//
// H3c (#125): bare `rufio goals` aliases to `goals list`. See
// NewThoughtsCmd for the cluster rationale.
func NewGoalsCmd() *cobra.Command {
	listCmd := newGoalsListCmd()
	cmd := &cobra.Command{
		Use:   "goals",
		Short: "Inspect coordination goals across the project",
		RunE:  listCmd.RunE,
	}
	cmd.Flags().AddFlagSet(listCmd.Flags())
	cmd.AddCommand(listCmd)
	return cmd
}

// newGoalsListCmd returns the `rufio goals list` subcommand.
//
//	rufio goals list [--scope=<scope>] [--state=<state>] [--json]
func newGoalsListCmd() *cobra.Command {
	var scopeFlag, stateFlag, parentFlag string
	var jsonFlag, quietFlag, noColorFlag, treeFlag bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List goals (active, completed, abandoned)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runGoalsList(cwd, scopeFlag, stateFlag, parentFlag, treeFlag, opts)
			}
			if err != nil {
				HandleError("goals", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "filter to goals with this scope (agent|deployment|fleet); empty = no filter")
	cmd.Flags().StringVar(&stateFlag, "state", "", "filter to goals in this state (active|completed|abandoned); empty = all")
	cmd.Flags().StringVar(&parentFlag, "parent", "", "filter to direct children of <id> (#131)")
	cmd.Flags().BoolVar(&treeFlag, "tree", false, "render nested goals indented under their parent (#131)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runGoalsList is the pure logic for `rufio goals list`. Validation
// order (design §4.D): cheap-and-syntactic first (scope/state enum
// checks), then filesystem-touching (FindProjectRoot + ReadAll).
//
// Identity is resolved best-effort: when set (env or file), the
// privacy filter (#147) hides scope:agent goals authored by other
// agents — closing the leak that R10 surfaced in vet round 2026-05-20.
// When identity is unset, the predicate's anonymous-firehose path
// keeps the pre-#147 contract (goals list returns every goal). This
// mirrors the stream.Match opt-in semantic shipped in #139.
func runGoalsList(cwd, scopeFilter, stateFilter, parentFilter string, tree bool, opts output.RenderOpts) error {
	// Validate filters BEFORE touching the filesystem so the failure
	// envelope is the same whether or not the project is initialised.
	if scopeFilter != "" {
		if err := thought.ValidateScope(scopeFilter); err != nil {
			return err
		}
	}
	if stateFilter != "" {
		switch goal.State(stateFilter) {
		case goal.StateActive, goal.StateCompleted, goal.StateAbandoned:
			// ok
		default:
			return &rufioerr.UsageError{
				Message: fmt.Sprintf("invalid --state %q: must be one of [active completed abandoned]", stateFilter),
			}
		}
	}

	// v1.0.5: --server routes through the remote MCP goals_list tool.
	// The server runs the same scope/state filters and the privacy floor
	// against the bearer-token agent. --parent and --tree filters are
	// applied locally to the server's response since the MCP tool's
	// surface is scope/state only (matches the goals_list tool shape).
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"scope": scopeFilter,
			"state": stateFilter,
		})
		return remoteCallAndRender("goals", "goals_list", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// Identity is best-effort. An unidentified caller gets the firehose
	// path via privacy.IsVisible (anonymous returns true unconditionally).
	currentAgent, _, _ := identity.Resolve(root)

	all, err := goal.ReadAll(root)
	if err != nil {
		return err
	}

	// Filter pass — preserves the ReadAll ordering (state precedence,
	// then ts descending within each state). Privacy filter (#147)
	// applies BEFORE scope/state filters so the leak gate is the same
	// regardless of the user-facing query.
	//
	// #131: --parent=<id> narrows to direct children of <id>. Applies
	// AFTER privacy so a hidden parent can't be probed by trying its id.
	matched := make([]goal.Goal, 0, len(all))
	for _, g := range all {
		if !privacy.IsVisible(g, currentAgent) {
			continue
		}
		if scopeFilter != "" && g.Scope != scopeFilter {
			continue
		}
		if stateFilter != "" && string(g.State) != stateFilter {
			continue
		}
		if parentFilter != "" && g.Parent != parentFilter {
			continue
		}
		matched = append(matched, g)
	}

	if opts.JSON {
		return renderGoalsJSON(matched, opts)
	}
	if tree {
		renderGoalsTree(matched, opts)
		return nil
	}
	renderGoalsColumnar(matched, opts)
	return nil
}

// renderGoalsColumnar prints one tab-separated line per goal. Empty
// input produces zero output (no header) — same convention as
// summons list.
//
// Line shape (H1b):
//
//	<state>\t<reltime>\t<short-id>\tauthor:<author>\tscope:<scope>[\tparent:<id>]\tstatement:"<truncated>"
//
// parent:<id> is emitted ONLY when g.Parent != "" (#131) — parentless
// goals keep their pre-#131 columns so the common case stays compact.
//
// H1a: state is rendered via BoldState (active→green, completed→cyan,
// abandoned→red bold on a colour tty); short-id via Cyan; reltime via Dim.
func renderGoalsColumnar(rows []goal.Goal, opts output.RenderOpts) {
	now := time.Now()
	for _, g := range rows {
		line := formatGoalRow(g, now, opts)
		// WriteData (not WriteOut): rows are primary output and must
		// survive --quiet. Matches summons list.
		output.WriteData(line, opts)
	}
}

// formatGoalRow renders a single columnar goal line. Pulled out so the
// tree renderer can reuse the column shape (just prepended with indent)
// — keeping the column contract single-sourced.
//
// The colour wrappers degrade to no-op when ShouldUseColor returns
// false, so non-tty consumers see exactly the same TAB-separated text
// they did before H1.
func formatGoalRow(g goal.Goal, now time.Time, opts output.RenderOpts) string {
	state := output.BoldState(string(g.State), opts)
	reltime := output.Dim(output.RenderRelTime(g.TS, now), opts)
	id := output.Cyan(output.FormatID(g.ID), opts)
	if g.Parent != "" {
		return fmt.Sprintf(
			"%s\t%s\t%s\tauthor:%s\tscope:%s\tparent:%s\tstatement:%s",
			state, reltime, id, g.Author, g.Scope, output.FormatID(g.Parent), quoteAndTruncate(g.Statement),
		)
	}
	return fmt.Sprintf(
		"%s\t%s\t%s\tauthor:%s\tscope:%s\tstatement:%s",
		state, reltime, id, g.Author, g.Scope, quoteAndTruncate(g.Statement),
	)
}

// renderGoalsTree prints goals as a nested hierarchy, indenting children
// under their parent. Roots are any goals whose Parent is "" OR whose
// Parent isn't present in the visible set (a hidden / out-of-scope
// parent shouldn't strand its visible children — they surface as roots
// so they don't disappear from the view).
//
// Traversal order: roots in their ReadAll order (state precedence, then
// ts desc within state); descendants in DFS via the sorted children
// adjacency. Indent is 2 spaces per depth level — matches the
// conventional indent in other rufio columnar diagrams.
func renderGoalsTree(rows []goal.Goal, opts output.RenderOpts) {
	// Build adjacency: parentID -> []Goal (children, in input order).
	visible := make(map[string]struct{}, len(rows))
	for _, g := range rows {
		visible[g.ID] = struct{}{}
	}
	children := make(map[string][]goal.Goal, len(rows))
	var roots []goal.Goal
	for _, g := range rows {
		if g.Parent == "" {
			roots = append(roots, g)
			continue
		}
		if _, ok := visible[g.Parent]; !ok {
			// Parent not in the visible set — treat as a root so the
			// goal still shows up in the tree.
			roots = append(roots, g)
			continue
		}
		children[g.Parent] = append(children[g.Parent], g)
	}
	now := time.Now()
	var walk func(g goal.Goal, depth int)
	walk = func(g goal.Goal, depth int) {
		indent := strings.Repeat("  ", depth)
		output.WriteData(indent+formatGoalRow(g, now, opts), opts)
		for _, c := range children[g.ID] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
}

// renderGoalsJSON emits one JSONL object per matched goal. The JSON
// shape (locked + conditional audit keys) is single-sourced in
// goal.RenderJSON so the CLI and the MCP goals_list tool never drift;
// see internal/lib/goal/goal.go RenderJSON. opts is unused (the
// "--json wins over --quiet" rule means JSONL always writes to stdout,
// exactly as output.WriteJSONL did).
func renderGoalsJSON(rows []goal.Goal, opts output.RenderOpts) error {
	_ = opts
	return goal.RenderJSON(os.Stdout, rows)
}
