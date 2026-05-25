package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// These tests pin the behaviour that makes docs/v1-spec.md §"How agents
// discover Rufio" (line ~281) TRUE: `rufio init` must emit a RUFIO.md
// agent-onboarding primer at the project root, and idempotently fold the
// same primer into any harness context files it detects (CLAUDE.md,
// .cursorrules, AGENTS.md). Before this PR the spec/demo claimed this but
// init only wrote rufio.gdl + the dir tree.

// loadBearingFragments are the substrate-onboarding facts a cold external
// harness MUST be able to learn from RUFIO.md alone. If any of these
// regress the primer stops being "genuinely sufficient for a cold harness
// to coordinate" (the acceptance bar in the spec section being made true).
var loadBearingFragments = []string{
	// (a) shared substrate, filesystem is the medium, no SDK.
	"shared",
	"filesystem",
	// (b) the verb set — every coordination verb must be named.
	"attend",
	"think",
	"observe",
	"reason",
	"confirm",
	"refute",
	"recall",
	"summon",
	"accept",
	"say",
	"goal",
	// (c) etiquette + quorum, quoted correctly from autopromote.go.
	"RUFIO_AGENT_ID",
	"3 distinct",
	"0.85",
	"learned/",
	// confirm/refute do NOT overwrite — surface the conflict.
	"overwrit",
	"conflict",
	// (c2) recall→id capture is binary-true (post-#46): plain recall
	// prints a labelled `id=<id>` field and `--json` exposes a
	// top-level `id` key. The plain `id=<id>` pin stays.
	//
	// RECONCILED (#78 fix 4): this list previously pinned the literal
	// `--json | jq -r '.id'` (quoted `.id`). That literal appeared in
	// the primer ONLY inside the FACTUALLY BROKEN multi-match recall
	// recipe `recall "<subject>" --types=thought --json | jq -r '.id'`
	// — `recall --json` is JSONL (one object per line, not an array),
	// so a bare quoted `.id` over a normal multi-thought subject
	// yields N ids with no disambiguation. #78 removes that recipe and
	// emphasises the reliable create-time capture
	// (`rufio think … --json | jq -r .id`, unquoted). The pin is
	// re-anchored to that still-true, multi-match-safe literal so the
	// recall→id capability stays guarded without re-encoding the bug.
	"id=<id>",
	"--json | jq -r .id",
	// scope vocabulary.
	"agent|deployment|fleet",
	// (d) the pointer to deeper docs.
	//
	// RECONCILED (#78 fix 2): this list previously pinned
	// "docs/primitives.md" as the SOLE deeper-docs pointer, encoding
	// the OLD primer framing that called it "the complete protocol
	// reference (all 13 verbs…)". That framing was a FACTUAL ERROR
	// caught in headless dogfood — `rufio --help` lists 40+ commands,
	// not 13, and the primer cited only that one file. The pin was
	// briefly replaced with `docs/cli-reference.md`.
	//
	// RECONCILED AGAIN (#122 fix 1): the cold-start vet caught the
	// `docs/cli-reference.md` and `docs/primitives.md` references too —
	// those files exist in the rufio REPO but `rufio init` does NOT
	// scaffold a `docs/` directory in the SUBSTRATE it creates. From the
	// moment a cold agent reads the primer in their freshly-initialised
	// project, both links are dead. The pin is now narrowed to the
	// references that are guaranteed-present everywhere: the binary's
	// own help (always current; generated from the command itself). The
	// no-stale-docs-refs guarantee is pinned negatively in
	// TestPrimer_NoStaleDocsRefs.
	"rufio <verb> --help",
	"rufio --help",
}

// preservedFragments are load-bearing strings that were hard-won in
// PR #45/#48/#52 and MUST survive any future primer edit byte-for-byte
// (or only additively extended). A regression on any of these silently
// breaks the cold-agent dogfood gate, so they get their own guard
// independent of the broader loadBearingFragments list.
var preservedFragments = []string{
	// Quorum: engine-true sentence with the autopromote constant
	// interpolated (NOT hardcoded). MinDistinctConfirmers=3,
	// MinConfidence=0.85 → these exact phrasings prove the
	// itoa/ftoa interpolation still feeds the primer.
	"≥3 distinct agents each running `rufio confirm <thought-id>`",
	"≥0.85 confidence",
	"emitting the `rufio think` does NOT\ncount as a confirm",
	"deduplicated",
	// thought-id capture (PR #52) — the recall→id / --json .id /
	// jq -r .id guidance must stay intact.
	//
	// RECONCILED (#78 fix 4): this list previously pinned the literal
	// recipe `--types=thought --json | jq -r '.id'`. That recipe was
	// FACTUALLY BROKEN: `recall --json` is JSONL (one object per line,
	// not an array) and the normal multi-agent case returns N thoughts
	// for a subject, so a bare `jq -r '.id'` yields N ids with no
	// disambiguation. The superseded pin is removed; the still-valid,
	// reliable capture paths (the labelled `id=<id>` field and the
	// create-time `rufio think … --json | jq -r .id`) stay pinned, and
	// the corrected multi-match-safe recall recipe is asserted
	// positively in TestRufioInit_PrimerCorrectionsFixed below — net
	// coverage is strengthened.
	"`recall`",
	"id=<id>",
	"jq -r .id",
	// Etiquette: no-overwrite / confirm-refute / scope.
	"There is no overwrite verb",
	"Disagreement is data.",
	"prefer the narrowest",
}

// newGuidanceFragments are the surgical additions this PR teaches
// (#135/#130/#128 primer pass). These pin the NEW guidance so a
// regression that drops the type-selection table, the reason→lineage
// rule, the decision-quorum clarification, the continuous-participation
// loop, or the action-mapping note fails loudly.
var newGuidanceFragments = []string{
	// (1) verb/type SELECTION — de-emphasise focus, name when to use
	// hypothesis/decision/question.
	"Which `--type` when",
	"`hypothesis`",
	"a claim/estimate to be verified",
	"`decision`",
	"a ratified choice/synthesis",
	"`question`",
	"asking the fleet",
	"`focus` is NOT the catch-all",
	"brief status / orientation only",
	// (2) reason → lineage drill-down.
	"lineage",
	"decision with no `reason` chain shows",
	"an EMPTY lineage",
	"rufio reason --content=... --decision=<that-decision-id>",
	// (3) quorum applies to a --type=decision too.
	"The same confirm → quorum → auto-promote",
	"applies to a `--type=decision`",
	"`confirm` the decision id",
	// (4) continuous participation — autonomous loop, first-class.
	"### Continuous participation (the autonomous loop)",
	"--catch-up",
	"do NOT stop and wait for a human",
	"~6 quiescent exchanges",
	// RECONCILED (#122 fix 4): the blanket "do NOT start `rufio dev`
	// yourself" rule was wrong for the first-mover case the cold-start
	// vet exposed (a fresh `rufio init` on an empty dir leaves NO
	// daemon running; the first agent SHOULD start one). The
	// corrected primer keeps the "one daemon per project"
	// invariant (now enforced by the #133 lock-guard) and adds the
	// first-mover branch. The no-blanket-prohibition guarantee is
	// pinned negatively in TestPrimer_DevDaemonGuidanceCoversBothCases.
	"one daemon per project",
	"rufio fleet",
	// (5) action representation — cognition substrate, no exec verb.
	"Rufio records cognition/coordination, not action execution",
	"no action/exec verb",
	"`attend --intent` (doing now)",
	"`observe` (did / durable result)",
}

func readPrimer(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "RUFIO.md"))
	if err != nil {
		t.Fatalf("read RUFIO.md: %v", err)
	}
	return string(b)
}

func TestRufioInit_WritesRufioMDPrimer(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	for _, frag := range loadBearingFragments {
		if !strings.Contains(primer, frag) {
			t.Errorf("RUFIO.md missing load-bearing fragment %q\n--- primer ---\n%s", frag, primer)
		}
	}
}

// TestRufioInit_PrimerPreservesHardWonSections guards the sections that
// were hard-won in PR #45/#48/#52 — chiefly the ENGINE-TRUE quorum line
// (constants interpolated from autopromote, never hardcoded), the
// thought-id capture guidance, and the etiquette rules. A future edit
// that silently weakens any of these fails here.
func TestRufioInit_PrimerPreservesHardWonSections(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	for _, frag := range preservedFragments {
		if !strings.Contains(primer, frag) {
			t.Errorf("RUFIO.md DROPPED preserved (hard-won) fragment %q\n--- primer ---\n%s", frag, primer)
		}
	}
}

// TestRufioInit_PrimerTeachesNewGuidance pins the surgical #135/#130/#128
// primer additions: verb/type selection (de-emphasising focus), the
// reason→lineage rule, the decision-quorum clarification, the
// continuous-participation autonomous loop, and the action-mapping note.
func TestRufioInit_PrimerTeachesNewGuidance(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	for _, frag := range newGuidanceFragments {
		if !strings.Contains(primer, frag) {
			t.Errorf("RUFIO.md missing NEW guidance fragment %q\n--- primer ---\n%s", frag, primer)
		}
	}
}

// engineTrueQuorumSubstrings are the byte-exact, autopromote-interpolated
// quorum sentences that PR #135/#45/#48/#52 made engine-true. They MUST
// survive the #78 correction pass byte-for-byte (the constants are
// rendered via itoa(autopromote.MinDistinctConfirmers) /
// ftoa(autopromote.MinConfidence); MinDistinctConfirmers=3,
// MinConfidence=0.85). This guard is intentionally independent of the
// broader preservedFragments list so a #78 edit cannot silently weaken
// the engine-truth contract.
var engineTrueQuorumSubstrings = []string{
	"quorum = **≥3 distinct agents each running `rufio confirm <thought-id>`**",
	"at **≥0.85 confidence**",
	"emitting the `rufio think` does NOT\ncount as a confirm",
	"Each\ndistinct `--as`/`RUFIO_AGENT_ID` is counted once, deduplicated",
	"the substrate AUTO-PROMOTES the thought to a\ndurable observation under `learned/`",
	"The same confirm → quorum → auto-promote",
	"applies to a `--type=decision`",
}

// continuousParticipationSubstrings pin the #135 autonomous-loop section
// verbatim — its heading and the load-bearing loop invariants. The #78
// pass must not touch this section's substance.
//
// RECONCILED (#122 fixes 3 + 4):
//   - The `rufio listen --as=$RUFIO_AGENT_ID --catch-up &` pin encoded
//     the redundant-`--as` idiom the cold-start vet caught (`rufio
//     listen --as` defaults to the current identity when
//     RUFIO_AGENT_ID is set, so passing it explicitly is noise). The
//     corrected form (`rufio listen --catch-up &`) is the new pin; the
//     no-redundant-`--as` guarantee is pinned negatively in
//     TestPrimer_NoRedundantListenAs.
//   - The "do NOT start `rufio dev` yourself" pin encoded the blanket
//     prohibition that was false for the first-mover case (see the
//     newGuidanceFragments comment above). The "one daemon per
//     project" invariant — still true, now lock-guard-enforced — stays.
var continuousParticipationSubstrings = []string{
	"### Continuous participation (the autonomous loop)",
	"rufio listen --catch-up &",
	"do NOT stop and wait for a human",
	"~6 quiescent exchanges",
	"one daemon per project",
}

// TestRufioInit_PrimerPreserveGuard_EngineTrueAndContinuousLoop pins the
// #78-PRESERVE contract: the engine-true quorum sentences (constants
// interpolated from autopromote, never hardcoded) and the
// continuous-participation autonomous-loop section survive the
// correctness pass byte-for-byte. A future edit that silently drops or
// reworords any of these fails here.
func TestRufioInit_PrimerPreserveGuard_EngineTrueAndContinuousLoop(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)
	for _, frag := range engineTrueQuorumSubstrings {
		if !strings.Contains(primer, frag) {
			t.Errorf("RUFIO.md DROPPED engine-true quorum substring %q (preserve-guard #78)\n--- primer ---\n%s", frag, primer)
		}
	}
	for _, frag := range continuousParticipationSubstrings {
		if !strings.Contains(primer, frag) {
			t.Errorf("RUFIO.md DROPPED continuous-participation substring %q (preserve-guard #78)\n--- primer ---\n%s", frag, primer)
		}
	}
}

// TestRufioInit_PrimerCorrectionsFixed asserts the four FACTUAL
// corrections in #78 (the doc-half of #77). Written RED-first against
// the primer at 0642433 (every assertion below fails on the pre-fix
// primer); GREEN once primer.go is corrected.
func TestRufioInit_PrimerCorrectionsFixed(t *testing.T) {
	root := initProject(t)
	primer := readPrimer(t, root)

	// (1) reason→decision: the primer must instruct attaching reason to
	// the DECISION's id (post think --type=decision, capture ITS id,
	// then reason --decision=<that DECISION id>), aligned with the #77
	// sibling code that makes `reason --decision` REJECT a hypothesis.
	// It must also make explicit that --decision targets a decision,
	// never a hypothesis, and must NOT instruct attaching reason to the
	// agent's hypothesis.
	if !strings.Contains(primer, "NEVER a\n  hypothesis") {
		t.Errorf("primer must state `reason --decision` targets a decision, NEVER a hypothesis")
	}
	if strings.Contains(primer, "--decision=<thought-id>") {
		t.Errorf("primer still uses the ambiguous `--decision=<thought-id>` framing; it must say `--decision=<decision-id>` (a DECISION, never a hypothesis)")
	}
	// The reason verb signature must constrain --decision to a decision id.
	if !strings.Contains(primer, "--decision=<decision-id>") {
		t.Errorf("primer reason guidance must use `--decision=<decision-id>` (decision-targeted)")
	}
	// No "attach reason to your hypothesis"-type wording.
	for _, bad := range []string{
		"reason --decision=<that thought",
		"--decision=<hypothesis",
		"attach reason to your hypothesis",
		"reason to your hypothesis",
	} {
		if strings.Contains(primer, bad) {
			t.Errorf("primer contains orphaning reason-on-hypothesis wording %q (now errors per #77)", bad)
		}
	}

	// (2) dead protocol-reference pointer + wrong verb count: the primer
	// must NOT claim a hard "13 verbs" count and must NOT frame any
	// single file as THE complete protocol reference; it must point at
	// the real canonical references.
	//
	// RECONCILED (#122 fix 1): the "must point at the real
	// docs/cli-reference.md" requirement was itself a bug — `rufio
	// init` does NOT scaffold a `docs/` directory in the substrate, so
	// that link is dead from the moment the primer is read. The
	// requirement is reversed in TestPrimer_NoStaleDocsRefs (the primer
	// must NOT cite docs/*.md) and the canonical-references positive
	// pins are the binary's own help — `rufio --help` and
	// `rufio <verb> --help` — which are guaranteed-present everywhere.
	if strings.Contains(primer, "all 13 verbs") || strings.Contains(primer, "13 verbs") {
		t.Errorf("primer still claims a hard \"13 verbs\" count (rufio --help lists 40+ commands)")
	}
	if strings.Contains(primer, "the complete protocol reference") {
		t.Errorf("primer still frames a single doc as THE complete protocol reference")
	}
	if !strings.Contains(primer, "rufio <verb> --help") {
		t.Errorf("primer must point at `rufio <verb> --help` for any verb")
	}
	if !strings.Contains(primer, "rufio --help") {
		t.Errorf("primer must point at the full command list via `rufio --help`")
	}

	// (3) entity-id format: the namespace:local colon format and a
	// concrete example must be documented where entities/subjects are
	// introduced.
	if !strings.Contains(primer, "[a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+") {
		t.Errorf("primer must document the entity/subject id regex")
	}
	if !strings.Contains(primer, "namespace:local") {
		t.Errorf("primer must explain the mandatory namespace:local colon segment")
	}
	if !strings.Contains(primer, "customer:5821") {
		t.Errorf("primer must give a concrete entity-id example (e.g. customer:5821)")
	}

	// (4) TID/id-capture recipe: the broken single-`.id` jq on a
	// multi-match recall must be gone; the corrected recipe must be
	// multi-match safe.
	if strings.Contains(primer, "recall \"<subject>\" --types=thought --json | jq -r '.id'") {
		t.Errorf("primer still ships the BROKEN multi-match recall recipe (bare jq -r '.id' over JSONL yields N ids)")
	}
	// The reliable create-time capture path stays.
	if !strings.Contains(primer, "rufio think") || !strings.Contains(primer, "jq -r .id") {
		t.Errorf("primer must keep the reliable create-time capture: rufio think … --json | jq -r .id")
	}
	// A multi-match-safe peer-thought pattern must be present (last
	// match via jq -rs '.[-1].id', an explicit filter, or the labelled
	// id= lift) — assert the slurp/last-match form the corrected recipe
	// uses so a regression to bare `.id` fails.
	if !regexp.MustCompile(`jq -rs '\.\[-1\]\.id'`).MatchString(primer) &&
		!strings.Contains(primer, "select(") {
		t.Errorf("primer must give a multi-match-safe recall→id pattern (jq -rs '.[-1].id' or an explicit select() filter)")
	}
}

// TestVerbCmds_EntitySubjectFlagHelpStatesIDFormat asserts the
// --entities/--subject flag help of attend/observe/think now states the
// entity-id format (additive, behaviour-unchanged) so an agent reading
// `rufio <verb> --help` learns the namespace:local rule without having
// to trigger an exit-2.
func TestVerbCmds_EntitySubjectFlagHelpStatesIDFormat(t *testing.T) {
	cases := []struct {
		cmd  string
		flag string
	}{
		{"attend", "entities"},
		{"observe", "subject"},
		{"think", "subject"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.cmd, func(t *testing.T) {
			workdir := mkProject(t)
			r := testutil.RunCLI(t, []string{c.cmd, "--help"}, workdir, nil)
			if r.Code != 0 {
				t.Fatalf("%s --help: exit %d, stderr: %s", c.cmd, r.Code, r.Stderr)
			}
			help := r.Stdout + r.Stderr
			if !strings.Contains(help, "--"+c.flag) {
				t.Fatalf("%s --help missing the --%s flag entirely:\n%s", c.cmd, c.flag, help)
			}
			// The corrected flag help must surface the colon-segment rule
			// and a concrete example.
			if !strings.Contains(help, "namespace:local") {
				t.Errorf("%s --%s help must state the namespace:local id format:\n%s", c.cmd, c.flag, help)
			}
			if !strings.Contains(help, "customer:5821") {
				t.Errorf("%s --%s help must give a concrete example (customer:5821):\n%s", c.cmd, c.flag, help)
			}
		})
	}
}

func TestRufioInit_NoHarnessFiles_OnlyWritesRufioMD(t *testing.T) {
	root := initProject(t)
	for _, f := range []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Errorf("init created %q but it did not pre-exist; it must only touch harness files that already exist", f)
		}
	}
}

const rufioBeginMarker = "<!-- rufio:begin -->"
const rufioEndMarker = "<!-- rufio:end -->"

// markedBlockCount counts how many begin-markers appear — used to assert
// idempotent re-init never duplicates the block.
func markedBlockCount(s string) int {
	return strings.Count(s, rufioBeginMarker)
}

func TestRufioInit_AppendsIdempotentBlockToHarnessFiles(t *testing.T) {
	for _, harness := range []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"} {
		harness := harness
		t.Run(harness, func(t *testing.T) {
			workdir := mkProject(t)
			userContent := "# My project rules\n\nDo not delete prod.\n"
			hp := filepath.Join(workdir, harness)
			if err := os.WriteFile(hp, []byte(userContent), 0o644); err != nil {
				t.Fatalf("seed harness file: %v", err)
			}

			if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
				t.Fatalf("init: exit %d, stderr: %s", r.Code, r.Stderr)
			}

			got, err := os.ReadFile(hp)
			if err != nil {
				t.Fatalf("read harness file: %v", err)
			}
			gs := string(got)
			if !strings.Contains(gs, userContent) {
				t.Errorf("user content was clobbered in %s:\n%s", harness, gs)
			}
			if markedBlockCount(gs) != 1 {
				t.Errorf("%s: want exactly 1 rufio block, got %d", harness, markedBlockCount(gs))
			}
			if !strings.Contains(gs, rufioEndMarker) {
				t.Errorf("%s: missing end marker", harness)
			}
			// RECONCILED (#122 fix 1): the `docs/cli-reference.md`
			// fragment pin was itself a bug — that file is not
			// scaffolded by `rufio init` (see loadBearingFragments
			// note). Replaced with `rufio --help`, which IS guaranteed
			// to be present everywhere a primer is read.
			for _, frag := range []string{"attend", "RUFIO_AGENT_ID", "0.85", "rufio --help"} {
				if !strings.Contains(gs, frag) {
					t.Errorf("%s rufio block missing %q", harness, frag)
				}
			}
		})
	}
}

// TestRufioInit_ReinitRefreshesPrimerAndRefoldsIdempotently pins the
// #128 contract.
//
// SUPERSEDED CONTRACT (deliberately reversed by #128): the previous
// version of this test asserted re-init exits NON-ZERO
// (AlreadyInitialisedError) *before* writePrimerArtifacts runs, so a
// harness file added AFTER the first init could never be folded. That
// behaviour is the bug #128 fixes — a primer-version bump or a newly
// added CLAUDE.md/AGENTS.md must be re-foldable. The old "expected
// non-zero exit on re-init" / "RUFIO.md mutated by re-init is an error"
// assertions encoded that superseded contract and are replaced below
// with the new contract:
//
//   - re-init SUCCEEDS (exit 0) on an already-initialised project,
//   - it re-emits RUFIO.md (deterministic — byte-identical when the
//     primer is unchanged),
//   - it idempotently FOLDS a CLAUDE.md added *after* the first init
//     (exactly one marked block, user content outside markers intact),
//   - it does NOT rewrite rufio.gdl or clobber given/learned/live data.
//
// The still-valid invariant (rufio.gdl + data untouched on re-init) is
// preserved here — only the hard-fail behaviour was superseded.
func TestRufioInit_ReinitRefreshesPrimerAndRefoldsIdempotently(t *testing.T) {
	workdir := mkProject(t)

	first := testutil.RunCLI(t, []string{"init"}, workdir, nil)
	if first.Code != 0 {
		t.Fatalf("first init: %s", first.Stderr)
	}
	primerAfter1 := readPrimer(t, workdir)
	gdlAfter1, err := os.ReadFile(filepath.Join(workdir, "rufio.gdl"))
	if err != nil {
		t.Fatalf("read rufio.gdl: %v", err)
	}
	// Drop a data file into the live tree to prove re-init never touches it.
	sentinel := filepath.Join(workdir, "live", "sentinel.gdl")
	if err := os.WriteFile(sentinel, []byte("@marker|keep:1\n"), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	// A CLAUDE.md added AFTER the first init — under the SUPERSEDED
	// contract this could NEVER be folded (re-init hard-failed before
	// writePrimerArtifacts). The #128 contract folds it on re-init.
	harness := filepath.Join(workdir, "CLAUDE.md")
	userContent := "# rules\nbe careful\n"
	if err := os.WriteFile(harness, []byte(userContent), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	second := testutil.RunCLI(t, []string{"init"}, workdir, nil)
	if second.Code != 0 {
		t.Fatalf("expected re-init to SUCCEED (refresh primer), got exit %d, stderr: %s", second.Code, second.Stderr)
	}

	// RUFIO.md re-emitted, deterministic (primer unchanged → identical).
	primerAfter2 := readPrimer(t, workdir)
	if primerAfter1 != primerAfter2 {
		t.Errorf("re-init produced non-deterministic RUFIO.md\n--- before ---\n%s\n--- after ---\n%s", primerAfter1, primerAfter2)
	}

	// The post-first-init CLAUDE.md is now folded — exactly one block,
	// user content outside the markers preserved verbatim.
	claudeAfter2, err := os.ReadFile(harness)
	if err != nil {
		t.Fatalf("read CLAUDE.md after re-init: %v", err)
	}
	cs := string(claudeAfter2)
	if markedBlockCount(cs) != 1 {
		t.Errorf("re-init must fold the newly-added CLAUDE.md exactly once, got %d blocks:\n%s", markedBlockCount(cs), cs)
	}
	if !strings.Contains(cs, rufioEndMarker) {
		t.Errorf("folded CLAUDE.md missing end marker:\n%s", cs)
	}
	if !strings.Contains(cs, userContent) {
		t.Errorf("re-init clobbered user content outside markers:\n%s", cs)
	}
	if !strings.Contains(cs, "RUFIO_AGENT_ID") {
		t.Errorf("folded block missing primer content:\n%s", cs)
	}

	// rufio.gdl + live data untouched (the still-valid invariant).
	gdlAfter2, _ := os.ReadFile(filepath.Join(workdir, "rufio.gdl"))
	if string(gdlAfter1) != string(gdlAfter2) {
		t.Errorf("re-init rewrote rufio.gdl\n--- before ---\n%s\n--- after ---\n%s", gdlAfter1, gdlAfter2)
	}
	sb, err := os.ReadFile(sentinel)
	if err != nil || string(sb) != "@marker|keep:1\n" {
		t.Errorf("re-init clobbered live data sentinel: err=%v content=%q", err, string(sb))
	}

	// A SECOND re-init must remain idempotent (still one block).
	third := testutil.RunCLI(t, []string{"init"}, workdir, nil)
	if third.Code != 0 {
		t.Fatalf("third init (idempotent refresh): exit %d, stderr: %s", third.Code, third.Stderr)
	}
	claudeAfter3, _ := os.ReadFile(harness)
	if markedBlockCount(string(claudeAfter3)) != 1 {
		t.Errorf("repeated re-init duplicated the marked block: %d", markedBlockCount(string(claudeAfter3)))
	}
}

// TestRufioInit_ReinitJSONIndicatesRefresh pins that the JSON summary
// distinguishes a refresh from a fresh init (#128 requirement).
func TestRufioInit_ReinitJSONIndicatesRefresh(t *testing.T) {
	workdir := mkProject(t)
	if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
		t.Fatalf("first init: %s", r.Stderr)
	}
	r := testutil.RunCLI(t, []string{"init", "--quiet", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("re-init --json: exit %d, stderr: %s", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "refresh") {
		t.Errorf("re-init JSON summary should indicate a refresh; got: %q", r.Stdout)
	}
}

// TestRufioInit_StalePrimerBlockReplacedInPlace asserts the idempotent
// marker semantics: if a marked block already exists (e.g. from an older
// rufio version) it is replaced in place, never duplicated, and user
// content outside the markers is preserved verbatim.
func TestRufioInit_StalePrimerBlockReplacedInPlace(t *testing.T) {
	workdir := mkProject(t)
	harness := filepath.Join(workdir, "AGENTS.md")
	stale := "# top\nkeep me\n\n" + rufioBeginMarker + "\nOLD STALE PRIMER\n" + rufioEndMarker + "\n\n# bottom\nkeep me too\n"
	if err := os.WriteFile(harness, []byte(stale), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if r := testutil.RunCLI(t, []string{"init"}, workdir, nil); r.Code != 0 {
		t.Fatalf("init: %s", r.Stderr)
	}

	got, _ := os.ReadFile(harness)
	gs := string(got)
	if markedBlockCount(gs) != 1 {
		t.Errorf("want exactly 1 block after replace, got %d:\n%s", markedBlockCount(gs), gs)
	}
	if strings.Contains(gs, "OLD STALE PRIMER") {
		t.Errorf("stale block not replaced:\n%s", gs)
	}
	if !strings.Contains(gs, "# top\nkeep me\n") || !strings.Contains(gs, "# bottom\nkeep me too\n") {
		t.Errorf("user content around stale block not preserved:\n%s", gs)
	}
	if !strings.Contains(gs, "RUFIO_AGENT_ID") {
		t.Errorf("replacement block missing fresh primer content:\n%s", gs)
	}
}
