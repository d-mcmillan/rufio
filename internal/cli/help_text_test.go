// Package cli — tests for `--help` text quality of public verbs (#156, #157).
//
// These tests are help-text-only contract checks. They build each verb's
// Cobra command via its public constructor, render `--help` to a buffer,
// and assert specific properties of the rendered output.
//
// Why this exists:
//
//   - #156: `rufio mcp --help` shipped without enumerating the 19 exposed
//     MCP tools. Cold integrators wiring rufio into Claude Desktop / Cursor
//     have no in-binary way to know what tools they get. The fix surfaces
//     the tool roster in the Long: description.
//
//   - #157: `rufio goal --help` shows `--scope` with its default rendered
//     TWICE: once as a hardcoded `(default agent)` baked into the
//     description string, and again as pflag's auto-generated `(default
//     "agent")`. The fix removes the hardcoded redundancy. H3a (#125)
//     later flipped the pflag default agent → fleet for write-verb
//     consistency; the duplicate-render invariant is unchanged.
//
//   - Audit guard: every other verb that takes --scope (think/observe/
//     attend/reason/recall/listen/stream/goals) must NOT have the same
//     duplicate-default rendering. The cluster of #125-introduced
//     `; default fleet` strings is legal (pflag default is empty so no
//     auto-render fires) but the test pins the invariant against future
//     drift.
package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// renderHelp builds the cobra command via its constructor, sets SetArgs
// to ["--help"], and captures the rendered help via SetOut. Returns the
// rendered string. Fails the test on any error.
func renderHelp(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	// cobra returns nil on --help (it's not an error). RunE is never
	// invoked because Cobra short-circuits on the help flag.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute(--help): %v", err)
	}
	return buf.String()
}

// TestMcpHelp_ListsAllTools (#156) asserts `rufio mcp --help` enumerates
// every one of the 22 MCP tools the server exposes. The list is sourced
// from internal/mcp/server.go's register* calls. If a tool is added to the
// server, this test (and the Long: text) must be updated in lockstep.
//
// v1.2.0 bumped the roster from 19 to 20 by adding `open` — the read-dual
// of `attend`. v1.0.3 bumped 20 → 21 by adding `quickstart` — the cold-
// start card. v1.0.4 bumped 21 → 22 by adding `serve_status` — the hosted-
// server health probe. The symmetry contract requires every CLI verb to
// have a matching MCP tool.
func TestMcpHelp_ListsAllTools(t *testing.T) {
	tools := []string{
		"attend", "think", "observe", "reason", "retract",
		"confirm", "refute", "recall",
		"summon", "accept", "decline", "say", "leave", "close",
		"goal", "goals_list", "goal_complete", "goal_abandon",
		"listen", "open", "quickstart", "serve_status",
	}
	if len(tools) != 22 {
		t.Fatalf("tool roster has %d entries, expected 22 (#156 spec + v1.2.0 open + v1.0.3 quickstart + v1.0.4 serve_status)", len(tools))
	}
	help := renderHelp(t, NewMcpCmd("test-version"))
	for _, tool := range tools {
		// Word-boundary match so "goal" doesn't accidentally satisfy
		// the assertion when only "goal_complete" is present.
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(tool) + `\b`)
		if !re.MatchString(help) {
			t.Errorf("rufio mcp --help does not list tool %q; help text:\n%s", tool, help)
		}
	}
}

// TestGoalHelp_ScopeDefaultRenderedOnce (#157) asserts that the `--scope`
// line in `rufio goal --help` contains exactly ONE "default" rendering,
// not two. Pre-fix the line is:
//
//	--scope string   visibility scope (agent|deployment|fleet); default agent (default "agent")
//
// The hardcoded `; default agent` plus pflag's auto-rendered `(default
// "agent")` collide. The fix removes the hardcoded portion.
//
// We count occurrences of the word `default` on the line (case-insensitive,
// word-boundary) — the buggy state has 2, the fixed state has 1 (pflag's
// auto-rendered form).
func TestGoalHelp_ScopeDefaultRenderedOnce(t *testing.T) {
	help := renderHelp(t, NewGoalCmd())
	line := findFlagLine(help, "--scope")
	if line == "" {
		t.Fatalf("rufio goal --help has no --scope line; help:\n%s", help)
	}
	count := countDefaultMentions(line)
	if count != 1 {
		t.Errorf("rufio goal --help: --scope line has %d 'default' mentions, want 1\nline: %q", count, line)
	}
	// Pflag's auto-rendered form must remain — that's the canonical one.
	// H3a (#125): default changed agent → fleet so goal matches the unified
	// write-verb rule.
	if !strings.Contains(line, `(default "fleet")`) {
		t.Errorf("rufio goal --help: --scope line lost pflag's auto-rendered default; line: %q", line)
	}
}

// TestAllVerbsHelp_NoDuplicateScopeDefault asserts that every verb taking
// --scope renders its default at most once. This catches both:
//
//   - The #157 anti-pattern (hardcoded default in description string AND a
//     non-empty pflag default value).
//   - Future drift if someone adds another verb with both signals.
//
// "Renders at most once" means: the word "default" appears ≤1 times on
// the --scope line. attend and reason use `; default fleet` in their
// description but their pflag default is empty (no auto-render) — so the
// count is 1, which is allowed. The bug is count=2.
//
// Verbs without a --scope flag are skipped (e.g. `goals` parent command
// may not expose one on its own --help, only on subcommands).
func TestAllVerbsHelp_NoDuplicateScopeDefault(t *testing.T) {
	cases := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"think", NewThinkCmd()},
		{"observe", NewObserveCmd()},
		{"attend", NewAttendCmd()},
		{"reason", NewReasonCmd()},
		{"recall", NewRecallCmd()},
		{"listen", NewListenCmd()},
		{"stream", NewStreamCmd()},
		{"goal", NewGoalCmd()},
		{"goals", NewGoalsCmd()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			help := renderHelp(t, tc.cmd)
			line := findFlagLine(help, "--scope")
			if line == "" {
				t.Skipf("verb %s has no --scope flag in --help output", tc.name)
			}
			count := countDefaultMentions(line)
			if count > 1 {
				t.Errorf("verb %s --help: --scope line has %d 'default' mentions, want ≤1\nline: %q", tc.name, count, line)
			}
		})
	}
}

// countDefaultMentions returns how many times the word "default"
// (case-insensitive, word-boundary) appears in `line`. Used to detect
// the #157 duplicate-default-rendering bug: pre-fix the goal --scope
// line says "default agent (default \"agent\")" — count=2; post-fix
// it says only "(default \"agent\")" — count=1.
func countDefaultMentions(line string) int {
	re := regexp.MustCompile(`(?i)\bdefault\b`)
	return len(re.FindAllString(line, -1))
}

// findFlagLine returns the line in `help` that begins (after leading
// whitespace) with `flag` (e.g. "--scope"). Returns empty string if no
// such line is found. Match is on the FULL flag long-form so "--scope"
// doesn't match "--scope-foo".
func findFlagLine(help, flag string) string {
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		// Cobra renders flag lines like:
		//   "--scope string   visibility scope ..."
		// or with a short-form prefix:
		//   "-s, --scope string   ..."
		if !strings.Contains(trimmed, flag+" ") && !strings.HasSuffix(trimmed, flag) {
			continue
		}
		// Confirm it's actually a flag-help line by checking that the
		// flag appears as a whole token (preceded by start or comma+
		// space, followed by space or end).
		idx := strings.Index(trimmed, flag)
		if idx == -1 {
			continue
		}
		// After the flag, the next char must be space or end-of-line.
		after := idx + len(flag)
		if after < len(trimmed) {
			next := trimmed[after]
			if next != ' ' && next != '=' {
				continue
			}
		}
		return line
	}
	return ""
}
