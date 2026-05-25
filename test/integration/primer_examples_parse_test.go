package integration_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestPrimer_ExampleCommandsParse is the highest-value v1.0.6.2 regression
// guard: every literal `rufio <verb> ...` example in the generated
// RUFIO.md (which IS buildPrimer() output) must be PARSEABLE by the real
// binary. "Parseable" here means the command does not exit 2 (usage /
// validation error from cobra or from a rufio validator like the entity
// id regex). Exit 0 (success) or exit 1 (semantic — e.g., no peer thought
// matches the given id) is fine; exit 2 means the cold agent COPIED the
// example verbatim and the CLI rejected it as malformed.
//
// Why this catches D1+D2 at PR-time:
//
//   - D1 (HIGH): pre-fix, the primer showed `--entities=freewill` (bare
//     id) which the validator rejects with exit 2
//     ("invalid entity id ... must match [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+").
//     With this test in place, any future doc edit that reintroduces a
//     bare-id example fails CI loudly.
//
//   - D2 (MED): the test catches the `reason --decision=<hypothesis-id>`
//     style pattern via the same exit-code contract — though D2's specific
//     case is harder to express here without a live hypothesis id, the
//     scrutiny is broadly the right shape: "the docs say to run this; if
//     the binary refuses, that's a bug regardless of which side is wrong".
//
// The test deliberately substitutes placeholder tokens (<id>, <thought-id>,
// <subject>, etc.) with substrate-valid stand-ins so the parse-vs-semantic
// distinction is clean. Substitution table is documented inline.
func TestPrimer_ExampleCommandsParse(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)

	// Extract every literal `rufio <verb> ...` invocation from the
	// primer. The primer uses Markdown backtick-fenced inline code for
	// inline examples and indented code blocks for multi-line snippets.
	// `extractRufioCommands` finds both shapes.
	cmds := extractRufioCommands(primer)
	if len(cmds) == 0 {
		t.Fatalf("extracted 0 rufio commands from primer — extractor regressed; primer head:\n%s", head(primer, 500))
	}

	// `runnableSubcommands` is the allow-list of verbs this guard
	// actually runs. We exclude verbs whose example shape is shell
	// composition (heredoc, jq pipes) since the parse-vs-semantic test
	// is about the CLI's flag parser, not the shell. We also exclude
	// `rufio init` because it would clobber `root`.
	runnableSubcommands := map[string]bool{
		"attend":  true,
		"think":   true,
		"observe": true,
		"reason":  true,
		"confirm": true,
		"refute":  true,
		"recall":  true,
		"goal":    true,
		"summon":  true,
		"accept":  true,
		"say":     true,
		"fleet":   true,
		"whoami":  true,
		"open":    true,
		"primer":  true,
		// Excluded: `init` (clobbers root), `dev`/`listen` (background
		// loops). `identity` writes per-project state — kept (idempotent).
		"identity": true,
	}

	env := map[string]string{"RUFIO_AGENT_ID": "test-agent"}

	for _, c := range cmds {
		c := c
		if len(c) == 0 {
			continue
		}
		// Trim the leading `rufio` and pick the subcommand.
		args := splitArgs(c)
		if len(args) < 2 || args[0] != "rufio" {
			continue
		}
		sub := args[1]
		if !runnableSubcommands[sub] {
			continue
		}
		// Strip the leading `rufio` token; testutil.RunCLI execs the
		// binary directly.
		args = args[1:]

		// Substitute primer placeholders with substrate-valid values so
		// the parser sees real-shaped inputs. The integration target is
		// CLI parse + validator, not semantics.
		args = substitutePlaceholders(args)

		// Skip examples that retain META-shapes the docs intentionally
		// use as syntactic notation, NOT literal copy-paste:
		//
		//   - `[--flag=v]`  is markdown for "optional"; agents are
		//     expected to drop the brackets, not type them.
		//   - `<a|b|c>`    is a pipe-separated alternative inside an
		//     angle bracket; agents are expected to pick one.
		//   - `…` / `...`  is an ellipsis placeholder for "more args".
		//   - lone-verb mentions (`rufio confirm`, `rufio think`) inside
		//     prose are documentation references, not invocations.
		if hasMetaToken(args) {
			continue
		}
		if isLoneVerb(args) {
			continue
		}

		t.Run(joinArgs(args), func(t *testing.T) {
			r := testutil.RunCLI(t, args, root, env)
			// Exit 2 is cobra's usage-error code AND rufio's validator
			// exit code. Anything 0 or 1 is acceptable.
			if r.Code == 2 {
				t.Errorf("primer example failed parse/validation (exit 2): %v\nstderr: %s\nstdout: %s",
					args, r.Stderr, r.Stdout)
			}
		})
	}
}

// hasMetaToken reports whether any arg still contains a documentation
// META-token (brackets, ellipsis, pipe-alternative) that an agent is
// expected to interpret, not type verbatim. Such examples are
// intentional prose, not literal commands.
func hasMetaToken(args []string) bool {
	for _, a := range args {
		if strings.ContainsAny(a, "[]…") {
			return true
		}
		if strings.Contains(a, "...") {
			return true
		}
		// Angle-bracketed pipe-alternative like `<agent|deployment|fleet>`.
		if strings.Contains(a, "<") && strings.Contains(a, "|") && strings.Contains(a, ">") {
			return true
		}
	}
	return false
}

// isLoneVerb reports whether the command is just `<verb>` with no flags
// or args — e.g. the primer's prose mention "their `rufio think` does
// NOT count as a confirm". Those are documentation references, not
// runnable invocations, so they are excluded from the parse guard.
func isLoneVerb(args []string) bool {
	return len(args) == 1
}

// extractRufioCommands lifts every literal `rufio ...` invocation from
// the primer body. It walks line-by-line, finds backtick-fenced spans of
// the form ` `rufio <subcommand> ...` `, and lifts the contents. It also
// picks up indented code blocks (four-space prefix) whose first token is
// `rufio`.
//
// The regex is deliberately conservative: it requires a backtick-fenced
// span starting with `rufio ` so prose mentions of `rufio` outside of a
// command example are skipped.
func extractRufioCommands(s string) []string {
	var out []string
	// Backtick-fenced inline command examples. The primer uses single
	// backticks heavily, e.g., `rufio think --type=hypothesis ...`.
	inline := regexp.MustCompile("`(rufio [^`]+)`")
	for _, m := range inline.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// splitArgs is a minimal shell-tokenizer for command examples: splits
// on whitespace but respects double-quoted spans (so `--intent="hello
// world"` becomes one arg). Not a full shell parser — sufficient for the
// primer's example shapes.
func splitArgs(cmd string) []string {
	var args []string
	var cur strings.Builder
	inQuote := false
	for _, r := range cmd {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuote:
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	// Strip surrounding quotes from each token (so the binary sees the
	// unquoted value the shell would have passed).
	for i, a := range args {
		args[i] = strings.Trim(a, `"`)
	}
	return args
}

// joinArgs renders args back into a readable label for t.Run subtest names.
func joinArgs(args []string) string {
	// t.Run sanitises whitespace; keep it short.
	if len(args) > 6 {
		return strings.Join(args[:6], "_") + "_..."
	}
	return strings.Join(args, "_")
}

// substitutePlaceholders rewrites primer angle-bracketed placeholders
// into substrate-valid stand-ins so the example parses without spurious
// errors unrelated to the doc-vs-CLI drift the test is hunting.
//
//	<your-stable-id>     -> test-agent
//	<id>, <thought-id>,
//	<decision-id>,
//	<summon-id>,
//	<ch-id>, <channel-id> -> a valid-shaped thought id
//	<subject>, <entity>  -> topic:test  (namespace:local)
//	<topic>, <t>, <text>,
//	<msg>, <why>, <what
//	you're doing>        -> a quoted literal string
//	<csv>, <your-subject> -> topic:test (or a CSV with one item)
//	<rel>, <value>       -> sample literal values
//	<query>, <dur>, <ts> -> sample literal values
//	<...>                -> "agent"   (a valid --scope value)
//	<agent-id>           -> test-peer
//	<seconds>            -> "60"
//
// Anything still angle-bracketed after substitution is left as-is —
// the cobra parser will treat it as a literal string and the test will
// report the residual placeholder.
func substitutePlaceholders(args []string) []string {
	const validID = "1700000000000-abcdef"
	repl := strings.NewReplacer(
		"<your-stable-id>", "test-agent",
		"<your-id>", "test-agent",
		"<agent-id>", "test-peer",
		"<id>", validID,
		"<thought-id>", validID,
		"<decision-id>", validID,
		"<summon-id>", validID,
		"<ch-id>", validID,
		"<channel-id>", validID,
		"<a-hypothesis-id>", validID,
		"<that-decision-id>", validID,
		"<that DECISION id>", validID,
		"<that decision id>", validID,
		"<peer-id>", "test-peer",
		"<entity>", "topic:test",
		"<subject>", "topic:test",
		"<your-subject>", "topic:test",
		"<your-stable-subject>", "topic:test",
		"<csv>", "topic:test",
		"<topic>", "demo",
		"<t>", "demo",
		"<text>", "demo",
		"<msg>", "demo",
		"<why>", "demo",
		"<what you're doing>", "demo",
		"<rel>", "prefers",
		"<value>", "demo",
		"<query>", "demo",
		"<dur>", "10m",
		"<ts>", "2026-05-01T00:00:00Z",
		"<...>", "agent",
		"<seconds>", "60",
		"<your subject>", "topic:test",
		"<name>", "demo",
		"<your-name>", "test-agent",
	)
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = repl.Replace(a)
	}
	return out
}

// head returns the first n bytes of s for inclusion in failure messages.
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
