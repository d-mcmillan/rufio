// Package cli — tests for the H3 verb-consistency pass.
//
// Three sub-fixes covered:
//
//	H3a — --scope default unification across WRITE verbs. The R24 cold-
//	start vet surfaced that --scope behaviour differed per verb: some
//	defaulted fleet (attend, reason), some required-explicit (think,
//	observe), and one defaulted "agent" (goal). The chosen unification
//	is fleet for all write verbs — broadcast is the substrate's purpose;
//	private is a deliberate opt-in. Read verbs that filter on scope
//	(recall/listen/stream) keep their "no filter" empty default — that
//	IS the unified rule (no implicit narrowing).
//
//	H3c — bare noun-verb invocations alias to `list`. Today `rufio
//	thoughts`, `rufio summons`, `rufio goals`, `rufio channels` (with no
//	subcommand) print help + exit 1 (Cobra default), while `rufio fleet`
//	runs. Inconsistent. The fix wires the parent's RunE to call the list
//	handler so the bare invocation does the obvious thing. --help still
//	works (RunE only fires when no help-flag short-circuit occurs).
//
//	H3d — echo verbosity normalization. Today success-echo shapes vary
//	("attention set: ...", "thought set: ...", "summoned: ...", "said:
//	...", etc). The house style is `<verb>: <key>=<val> ...` where
//	`<verb>` is the literal CLI verb (attend, think, observe, reason,
//	goal, summon, say, accept, decline, retract, confirm, refute). The
//	key=val payload remains verb-specific but follows the lead-with-id
//	convention where applicable.
//
// H3b is help-text-only (--topic vs --topics semantics documented per
// verb); covered by smoke in TestVerbHelp_TopicSemantics_Documented.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// ---------------------------------------------------------------------
// H3a — --scope default unification.
// ---------------------------------------------------------------------

// TestThink_DefaultScopeFleet asserts a missing --scope now defaults to
// fleet (previously required-explicit). The written @thought record
// carries scope:fleet.
func TestThink_DefaultScopeFleet(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runThink(root, thinkArgs{
		Type:    "hypothesis",
		Subject: "customer:5821",
		Content: "auth flake under load",
		// Scope intentionally omitted.
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runThink: unexpected error %v", err)
	}
	if got := readThoughtScope(t, root, "alice"); got != "fleet" {
		t.Errorf("thought scope (default) = %q, want %q", got, "fleet")
	}
}

// TestObserve_DefaultScopeFleet — symmetric to TestThink_DefaultScopeFleet.
func TestObserve_DefaultScopeFleet(t *testing.T) {
	root := scopeTestProject(t, "alice")
	err := runObserve(root, observeArgs{
		Subject:   "customer:5821",
		Predicate: "uses",
		Object:    "policy:refund-2",
		// Scope intentionally omitted.
	}, output.RenderOpts{Quiet: true})
	if err != nil {
		t.Fatalf("runObserve: unexpected error %v", err)
	}
	if got := readObservationScope(t, root, "customer:5821"); got != "fleet" {
		t.Errorf("observation scope (default) = %q, want %q", got, "fleet")
	}
}

// TestGoal_DefaultScopeFleet asserts goal's default --scope is now fleet
// (was "agent" pre-#125 cluster H3a). Goals are coordination primitives —
// fleet broadcast matches the rest of the write surface.
func TestGoal_DefaultScopeFleet(t *testing.T) {
	root := scopeTestProject(t, "alice")
	if err := runGoalWrite(root, "ship v1", "", "", "", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalWrite: %v", err)
	}
	all, err := goal.ReadAll(root)
	if err != nil {
		t.Fatalf("goal.ReadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 goal, got %d", len(all))
	}
	if got := all[0].Scope; got != "fleet" {
		t.Errorf("goal scope (default) = %q, want %q", got, "fleet")
	}
}

// TestAllScopeWriteVerbs_DefaultMatchesPrimerGuidance is a table-driven
// table that pins the H3a contract: every write verb defaults --scope to
// fleet when the flag is omitted. The primer guidance is "Write verbs
// default to --scope=fleet; pass --scope=agent for private."
//
// attend + reason were already fleet pre-H3a (scope_consistency_test.go);
// think + observe + goal change as part of H3a. The shared assertion
// here guards against future verbs slipping back to a different default.
func TestAllScopeWriteVerbs_DefaultMatchesPrimerGuidance(t *testing.T) {
	cases := []struct {
		name string
		run  func(root string) string // returns the on-disk scope value
	}{
		{
			name: "attend",
			run: func(root string) string {
				_ = runAttend(root, attendArgs{
					Intent: "x", Entities: "customer:1",
				}, output.RenderOpts{Quiet: true})
				return readAttentionScope(t, root, "alice")
			},
		},
		{
			name: "think",
			run: func(root string) string {
				_ = runThink(root, thinkArgs{
					Type: "hypothesis", Subject: "customer:1", Content: "x",
				}, output.RenderOpts{Quiet: true})
				return readThoughtScope(t, root, "alice")
			},
		},
		{
			name: "observe",
			run: func(root string) string {
				_ = runObserve(root, observeArgs{
					Subject: "customer:1", Predicate: "is", Object: "x",
				}, output.RenderOpts{Quiet: true})
				return readObservationScope(t, root, "customer:1")
			},
		},
		{
			name: "reason",
			run: func(root string) string {
				_ = runReason(root, reasonArgs{Content: "x"},
					output.RenderOpts{Quiet: true})
				return reasonScopeOf(t, root, "alice")
			},
		},
		{
			name: "goal",
			run: func(root string) string {
				_ = runGoalWrite(root, "ship", "", "", "", output.RenderOpts{Quiet: true})
				all, _ := goal.ReadAll(root)
				if len(all) == 0 {
					return ""
				}
				return all[0].Scope
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			root := scopeTestProject(t, "alice")
			if got := tc.run(root); got != "fleet" {
				t.Errorf("%s default scope = %q, want %q (H3a primer guidance)",
					tc.name, got, "fleet")
			}
		})
	}
}

// readThoughtScope scans live/outbox/<author>/*.gdl and returns the
// scope of the first @thought record. Used by TestThink_DefaultScopeFleet.
func readThoughtScope(t *testing.T, root, author string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "live", "outbox", author, "*.gdl"))
	if err != nil {
		t.Fatalf("glob outbox: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no outbox files under live/outbox/%s/", author)
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse outbox: %v", err)
	}
	for _, r := range records {
		if r.Type == "thought" {
			return r.Get("scope")
		}
	}
	t.Fatalf("no @thought record in %s", matches[0])
	return ""
}

// readObservationScope walks learned/<subject-path>/*.gdlm for the given
// subject and returns the scope of the first @observation. Mirrors
// observation.Write's on-disk layout (namespace:local → namespace/local).
func readObservationScope(t *testing.T, root, subject string) string {
	t.Helper()
	parts := strings.SplitN(subject, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("subject %q must be namespace:local", subject)
	}
	matches, err := filepath.Glob(filepath.Join(root, "learned", parts[0], parts[1], "*.gdlm"))
	if err != nil {
		t.Fatalf("glob learned: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no observation files under learned/%s/%s/", parts[0], parts[1])
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read learned: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse learned: %v", err)
	}
	for _, r := range records {
		if r.Type == "observation" {
			return r.Get("scope")
		}
	}
	t.Fatalf("no @observation record in %s", matches[0])
	return ""
}

// ---------------------------------------------------------------------
// H3b — --topic vs --topics semantics documented.
// ---------------------------------------------------------------------

// TestVerbHelp_TopicSemantics_Documented asserts each verb's --topic /
// --topics flag carries a description that disambiguates its semantic
// (Option B from the H3b spec).
//
//   - attend --topics: plural CSV — topics the agent is attending to.
//   - summon --topic: singular — conversation topic for opening a channel.
//   - think --topics: plural CSV — topic labels for this thought.
//   - observe --topics: plural CSV — topic labels for this observation.
//   - reason --topics: plural CSV — topic labels for this reasoning step.
//
// The test renders --help for each verb and checks the flag line carries
// the disambiguating word (plural "topic tokens" / "topics" for --topics;
// the word "channel" for summon's --topic, since channel is the
// distinguishing semantic).
func TestVerbHelp_TopicSemantics_Documented(t *testing.T) {
	cases := []struct {
		name      string
		cmd       *cobra.Command
		flag      string
		wantInDoc string // substring required in the flag's description
	}{
		{"attend", NewAttendCmd(), "--topics", "topic tokens"},
		{"think", NewThinkCmd(), "--topics", "topic tokens"},
		{"observe", NewObserveCmd(), "--topics", "topic tokens"},
		{"reason", NewReasonCmd(), "--topics", "topic tokens"},
		{"summon", NewSummonCmd(), "--topic", "channel"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			help := renderHelp(t, tc.cmd)
			line := findFlagLine(help, tc.flag)
			if line == "" {
				t.Fatalf("%s --help has no %s line; help:\n%s", tc.name, tc.flag, help)
			}
			if !strings.Contains(line, tc.wantInDoc) {
				t.Errorf("%s --help: %s line missing disambiguating substring %q\nline: %q",
					tc.name, tc.flag, tc.wantInDoc, line)
			}
		})
	}
}

// ---------------------------------------------------------------------
// H3c — bare noun-verb invocations alias to `list`.
// ---------------------------------------------------------------------

// TestThoughts_BareInvocation_RunsListBehavior asserts `rufio thoughts`
// (no subcommand) acts like `rufio thoughts list`: exit 0, no error,
// content emitted to stdout matches `thoughts list`. Previously the bare
// invocation printed help + exit 1 (Cobra default).
func TestThoughts_BareInvocation_RunsListBehavior(t *testing.T) {
	root := scopeTestProject(t, "alice")
	// Seed one thought so the listing is non-empty (the assertion is on
	// shape — empty stdout would still pass an "exit 0" check, but a
	// listing that actually walks the substrate is the load-bearing
	// behaviour the bare-verb is supposed to provide).
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "customer:1", Content: "test",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think: %v", err)
	}

	bare := captureStdout(t, func() {
		cwd, _ := os.Getwd()
		_ = os.Chdir(root)
		defer os.Chdir(cwd)
		cmd := NewThoughtsCmd()
		cmd.SetArgs(nil) // bare invocation
		_ = cmd.Execute()
	})

	list := captureStdout(t, func() {
		cwd, _ := os.Getwd()
		_ = os.Chdir(root)
		defer os.Chdir(cwd)
		cmd := NewThoughtsCmd()
		cmd.SetArgs([]string{"list"})
		_ = cmd.Execute()
	})

	if bare == "" || list == "" {
		t.Fatalf("bare or list stdout is empty\nbare=%q\nlist=%q", bare, list)
	}
	// Listings are non-deterministic on ts only if seeded multiple times;
	// here we seeded exactly one thought, so the two should match.
	if bare != list {
		t.Errorf("bare invocation differs from `list`:\nbare=%q\nlist=%q", bare, list)
	}
}

// TestSummons_BareInvocation_RunsListBehavior — symmetric assertion for
// `rufio summons`. The signal that distinguishes "ran list" from "Cobra
// printed help": when the parent has no RunE, Cobra prints help to the
// command's SetOut sink. We redirect SetOut into a buffer; if the buffer
// contains "Usage:" the parent fell through to help instead of running
// the list handler.
func TestSummons_BareInvocation_RunsListBehavior(t *testing.T) {
	root := scopeTestProject(t, "alice")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewSummonsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(nil) // bare invocation
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare `rufio summons` returned error: %v", err)
	}
	if strings.Contains(buf.String(), "Usage:") {
		t.Errorf("bare `rufio summons` printed Cobra help instead of running list:\n%s", buf.String())
	}
}

// TestGoals_BareInvocation_RunsListBehavior — symmetric to summons.
func TestGoals_BareInvocation_RunsListBehavior(t *testing.T) {
	root := scopeTestProject(t, "alice")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewGoalsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare `rufio goals` returned error: %v", err)
	}
	if strings.Contains(buf.String(), "Usage:") {
		t.Errorf("bare `rufio goals` printed Cobra help instead of running list:\n%s", buf.String())
	}
}

// TestChannels_BareInvocation_RunsListBehavior — symmetric. The channels
// parent gets bare-list wiring; the channel-show shape (`rufio channel
// show <id>`) is owned by H3's L worker and is untouched here.
func TestChannels_BareInvocation_RunsListBehavior(t *testing.T) {
	root := scopeTestProject(t, "alice")

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	cmd := NewChannelsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare `rufio channels` returned error: %v", err)
	}
	if strings.Contains(buf.String(), "Usage:") {
		t.Errorf("bare `rufio channels` printed Cobra help instead of running list:\n%s", buf.String())
	}
}

// TestThoughts_HelpFlagStillWorks is the regression guard: --help on the
// thoughts parent still prints help (RunE is short-circuited by Cobra's
// help-flag machinery).
func TestThoughts_HelpFlagStillWorks(t *testing.T) {
	help := renderHelp(t, NewThoughtsCmd())
	// Cobra's help output for a parent-with-subcommands carries "Usage:" +
	// the subcommand list. Both must be present.
	if !strings.Contains(help, "Usage:") {
		t.Errorf("thoughts --help missing 'Usage:' section:\n%s", help)
	}
	if !strings.Contains(help, "list") {
		t.Errorf("thoughts --help missing 'list' subcommand entry:\n%s", help)
	}
}

// ---------------------------------------------------------------------
// H3d — echo verbosity normalization.
// ---------------------------------------------------------------------

// TestEchoShape_AcrossAllWriteVerbs_FollowsHouseStyle asserts every write
// verb's success-echo line begins with `<verb>: ` where `<verb>` is the
// literal CLI verb. Today: `attention set:`, `thought set:`, `observation
// set:`, `reason set:`, `summoned:`, `said:`, `accepted:`, `declined:`,
// `retracted:`, `confirmed:`, `refuted:` — eleven different shapes. The
// house style is one: `<verb>: <kvs>`.
//
// We don't pin the kvs payload here (it remains verb-specific per H3d) —
// just the leading `<verb>: ` token so a cold agent can grep predictably.
func TestEchoShape_AcrossAllWriteVerbs_FollowsHouseStyle(t *testing.T) {
	// Each case runs the verb's pure-logic handler and captures stdout,
	// then asserts the leading token. Setup steals from scope_consistency
	// (scopeTestProject + identity-pinned) and where the verb operates on
	// an existing target we seed a minimal in-process @thought / @summon.
	root := scopeTestProject(t, "alice")

	// Seed a thought for retract/confirm/refute. The handlers expect the
	// target to exist on disk; we use runThink so the on-disk shape is
	// indistinguishable from a real `rufio think` invocation.
	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "customer:1", Content: "seed",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think: %v", err)
	}
	// Recover the seeded thought id from the file we just wrote.
	seedID := readFirstThoughtID(t, root, "alice")

	cases := []struct {
		name       string
		wantPrefix string
		run        func() error
	}{
		{
			name: "attend", wantPrefix: "attend: ",
			run: func() error {
				return runAttend(root, attendArgs{
					Intent: "x", Entities: "customer:1",
				}, output.RenderOpts{})
			},
		},
		{
			name: "think", wantPrefix: "think: ",
			run: func() error {
				return runThink(root, thinkArgs{
					Type: "hypothesis", Subject: "customer:1", Content: "x",
				}, output.RenderOpts{})
			},
		},
		{
			name: "observe", wantPrefix: "observe: ",
			run: func() error {
				return runObserve(root, observeArgs{
					Subject: "customer:1", Predicate: "is", Object: "x",
				}, output.RenderOpts{})
			},
		},
		{
			name: "reason", wantPrefix: "reason: ",
			run: func() error {
				return runReason(root, reasonArgs{Content: "x"},
					output.RenderOpts{})
			},
		},
		{
			name: "goal", wantPrefix: "goal: ",
			run: func() error {
				return runGoalWrite(root, "ship", "", "", "", output.RenderOpts{})
			},
		},
		{
			name: "retract", wantPrefix: "retract: ",
			run: func() error {
				return runRetract(root, seedID, "no longer relevant", output.RenderOpts{})
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := tc.run(); err != nil {
					t.Fatalf("%s run: %v", tc.name, err)
				}
			})
			if !strings.HasPrefix(strings.TrimSpace(out), tc.wantPrefix) {
				t.Errorf("%s echo missing house-style prefix %q; got %q",
					tc.name, tc.wantPrefix, out)
			}
		})
	}
}

// readFirstThoughtID returns the id of the first @thought record found
// under live/outbox/<author>/*.gdl. Used by the H3d table-test to recover
// an id for the retract round-trip.
func readFirstThoughtID(t *testing.T, root, author string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "live", "outbox", author, "*.gdl"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("glob outbox: matches=%v err=%v", matches, err)
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse outbox: %v", err)
	}
	for _, r := range records {
		if r.Type == "thought" {
			return r.Get("id")
		}
	}
	t.Fatalf("no @thought in %s", matches[0])
	return ""
}
