// Package errors defines the typed-error hierarchy Rufio uses to drive exit
// codes. Every error returned from public APIs implements RufioError; the
// dispatcher (in internal/cli) maps RufioError.ExitCode() to the process
// exit code.
//
// Mirrors src/lib/errors.ts in the TypeScript reference. Every typed error
// from week 1 is preserved here, including UsageError (exit code 2) and
// EmptyRefsError, which both surfaced through the week-1 review loops.
package errors

import (
	"fmt"
	"strings"
)

// RufioError is implemented by every typed error in Rufio. Callers can
// inspect ExitCode() to map to the process exit code without switching on
// concrete types.
type RufioError interface {
	error
	ExitCode() int
}

// NotInProjectError fires when no rufio.gdl is found walking up from cwd.
type NotInProjectError struct{ Cwd string }

func (e *NotInProjectError) Error() string {
	return fmt.Sprintf("not inside a Rufio project (no rufio.gdl found from %s)", e.Cwd)
}
func (e *NotInProjectError) ExitCode() int { return 1 }

// PathOutsideRootError fires when a content path resolves outside the
// project root (after symlink resolution).
type PathOutsideRootError struct{ Path string }

func (e *PathOutsideRootError) Error() string {
	return fmt.Sprintf("path '%s' is outside the project root", e.Path)
}
func (e *PathOutsideRootError) ExitCode() int { return 2 }

// IneligiblePathError fires when a path targets a forbidden tree
// (.rufio/, internal/, .git/) or the project root itself.
type IneligiblePathError struct{ Path, Reason string }

func (e *IneligiblePathError) Error() string {
	return fmt.Sprintf("path '%s' cannot be versioned: %s", e.Path, e.Reason)
}
func (e *IneligiblePathError) ExitCode() int { return 2 }

// NoSuchVersionError fires when a path has no @ref matching the selector.
type NoSuchVersionError struct{ Path, Version string }

func (e *NoSuchVersionError) Error() string {
	return fmt.Sprintf("no version '%s' for path '%s'", e.Version, e.Path)
}
func (e *NoSuchVersionError) ExitCode() int { return 1 }

// AlreadyInitialisedError fires when init runs in a project that already has
// a rufio.gdl.
type AlreadyInitialisedError struct{ Root string }

func (e *AlreadyInitialisedError) Error() string {
	return fmt.Sprintf("Rufio project already initialised at %s (rufio.gdl exists)", e.Root)
}
func (e *AlreadyInitialisedError) ExitCode() int { return 1 }

// EmptyRefsError fires when LatestRef is called on an empty ref list. Added
// per the week-1 Phase 2 review (M2): every internal failure path should
// carry an exit code, not throw a bare error.
type EmptyRefsError struct{}

func (e *EmptyRefsError) Error() string { return "cannot find latest ref in empty list" }
func (e *EmptyRefsError) ExitCode() int { return 1 }

// UsageError is what the user sees when they type something wrong: unknown
// flag, missing argument, malformed selector. Conventional Unix exit code 2.
//
// Added per the week-1 branch-level review (M1): bare Error throws from
// parseArgs were exiting 1 because the catch fell through to "non-RufioError
// → 1". Throwing UsageError keeps the contract clean — UsageError IS a
// RufioError so the dispatcher's `errors.As` lookup picks it up and the
// exit-code-2 invariant holds across every command.
type UsageError struct{ Message string }

func (e *UsageError) Error() string { return e.Message }
func (e *UsageError) ExitCode() int { return 2 }

// NoIdentityError fires when neither RUFIO_AGENT_ID nor
// .rufio/identity.local.gdl resolves to a valid agent id.
type NoIdentityError struct{}

func (e *NoIdentityError) Error() string {
	return "no identity set; run `rufio identity --as=<id>` or set RUFIO_AGENT_ID"
}
func (e *NoIdentityError) ExitCode() int { return 1 }

// InvalidIdentityError fires when an agent id fails the regex
// [a-z0-9][a-z0-9-]{0,63}. Rejected at the env-var read AND the
// `identity --as=` write.
type InvalidIdentityError struct{ ID string }

func (e *InvalidIdentityError) Error() string {
	return fmt.Sprintf("invalid agent id %q: must match [a-z0-9][a-z0-9-]{0,63}", e.ID)
}
func (e *InvalidIdentityError) ExitCode() int { return 2 }

// InvalidContentError fires when a required text field (--intent for
// attend, --content for think, --statement for observe) is empty after
// trimming. Exit 2 (usage shape).
type InvalidContentError struct{ Field string }

func (e *InvalidContentError) Error() string {
	return fmt.Sprintf("--%s must not be empty", e.Field)
}
func (e *InvalidContentError) ExitCode() int { return 2 }

// InvalidEntitiesError fires when --entities is missing, empty, or
// contains a token that fails the entity-id regex
// [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+. Exit 2.
type InvalidEntitiesError struct{ Token string }

func (e *InvalidEntitiesError) Error() string {
	if e.Token == "" {
		return "--entities must include at least one entity id"
	}
	return fmt.Sprintf("invalid entity id %q: must match [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+", e.Token)
}
func (e *InvalidEntitiesError) ExitCode() int { return 2 }

// InvalidTopicsError fires when --topics contains a token that fails the
// topic regex [a-z0-9][a-z0-9_.-]*. Exit 2.
type InvalidTopicsError struct{ Token string }

func (e *InvalidTopicsError) Error() string {
	if e.Token == "" {
		return "--topics must contain non-empty tokens (saw empty entry)"
	}
	return fmt.Sprintf("invalid topic %q: must match [a-z0-9][a-z0-9_.-]*", e.Token)
}
func (e *InvalidTopicsError) ExitCode() int { return 2 }

// InvalidTypeError fires when a --type value (for think) is not in the
// allowed enum. Exit 2.
type InvalidTypeError struct {
	Value   string
	Allowed []string
}

func (e *InvalidTypeError) Error() string {
	return fmt.Sprintf("invalid --type %q: must be one of %v", e.Value, e.Allowed)
}
func (e *InvalidTypeError) ExitCode() int { return 2 }

// InvalidSubjectError fires when --subject fails the entity-id regex
// [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+. Exit 2.
type InvalidSubjectError struct{ Subject string }

func (e *InvalidSubjectError) Error() string {
	if e.Subject == "" {
		return "--subject must not be empty"
	}
	return fmt.Sprintf("invalid --subject %q: must match [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+", e.Subject)
}
func (e *InvalidSubjectError) ExitCode() int { return 2 }

// InvalidParentError fires when --parent fails the thought-id regex
// [0-9]+-[a-z0-9]{6}. Exit 2.
type InvalidParentError struct{ ID string }

func (e *InvalidParentError) Error() string {
	return fmt.Sprintf("invalid --parent %q: must match <unix-millis>-<rand6>", e.ID)
}
func (e *InvalidParentError) ExitCode() int { return 2 }

// InvalidScopeError fires when --scope is not in the allowed enum
// (agent | deployment | fleet). Exit 2.
type InvalidScopeError struct {
	Value   string
	Allowed []string
}

func (e *InvalidScopeError) Error() string {
	return fmt.Sprintf("invalid --scope %q: must be one of %v", e.Value, e.Allowed)
}
func (e *InvalidScopeError) ExitCode() int { return 2 }

// InvalidTTLError fires when --ttl fails to parse as a positive integer
// (seconds), or is ≤ 0 when provided. Exit 2.
type InvalidTTLError struct{ Raw string }

func (e *InvalidTTLError) Error() string {
	return fmt.Sprintf("invalid --ttl %q: must be a positive integer (seconds)", e.Raw)
}
func (e *InvalidTTLError) ExitCode() int { return 2 }

// InvalidPredicateError fires when --predicate fails the regex
// [a-z][a-z0-9_-]*. Exit 2.
type InvalidPredicateError struct{ Predicate string }

func (e *InvalidPredicateError) Error() string {
	if e.Predicate == "" {
		return "--predicate must not be empty"
	}
	return fmt.Sprintf("invalid --predicate %q: must match [a-z][a-z0-9_-]*", e.Predicate)
}
func (e *InvalidPredicateError) ExitCode() int { return 2 }

// InvalidConfidenceError fires when --confidence fails to parse as a
// float in the inclusive range [0, 1]. Exit 2.
type InvalidConfidenceError struct{ Raw string }

func (e *InvalidConfidenceError) Error() string {
	return fmt.Sprintf("invalid --confidence %q: must be a number in [0,1]", e.Raw)
}
func (e *InvalidConfidenceError) ExitCode() int { return 2 }

// InvalidDecisionError fires when --decision fails the thought-id regex
// [0-9]+-[a-z0-9]{6}. Exit 2.
type InvalidDecisionError struct{ ID string }

func (e *InvalidDecisionError) Error() string {
	return fmt.Sprintf("invalid --decision %q: must match <unix-millis>-<rand6>", e.ID)
}
func (e *InvalidDecisionError) ExitCode() int { return 2 }

// NoSuchThoughtError fires when retract/confirm/refute references a
// record-id that doesn't exist in any agent's live/outbox/ NOR under
// learned/. Exit 1. (#150: retract now also walks learned/ for
// observations; the error message names both roots so the agent can
// tell their id was looked up everywhere, not just outbox.)
type NoSuchThoughtError struct{ ID string }

func (e *NoSuchThoughtError) Error() string {
	return fmt.Sprintf("no such record: id %q not found under live/outbox/ or learned/", e.ID)
}
func (e *NoSuchThoughtError) ExitCode() int { return 1 }

// AmbiguousIDError fires when a short-form (6-char suffix) thought-id
// resolves to MULTIPLE canonical thought-ids on disk. R29a's resolver
// surfaces every candidate with disambiguation context (author + type
// + subject) so the agent can pick by eye without a second query —
// silent first-match would write to the wrong record. Exit 1.
//
// Short is the original suffix the caller passed. Candidates carries
// the full canonical ids plus the context fields rendered into the
// message body. Order is filesystem-walk order (stable: outbox sorted
// then learned).
type AmbiguousIDError struct {
	Short      string
	Candidates []AmbiguousCandidate
}

// AmbiguousCandidate is one row in AmbiguousIDError.Candidates. Fields
// mirror what `thoughts list` would show so a cold agent can map the
// candidate to a record they already saw on stdout.
type AmbiguousCandidate struct {
	ID      string // canonical <unix-millis>-<rand6>
	Author  string
	Type    string // thought type (decision|hypothesis|focus|observation|…)
	Subject string
}

func (e *AmbiguousIDError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "short id %q is ambiguous; candidates:\n", e.Short)
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "  - %s (%s, %s", c.ID, c.Author, c.Type)
		if c.Subject != "" {
			fmt.Fprintf(&b, ", subject:%s", c.Subject)
		}
		b.WriteString(")\n")
	}
	b.WriteString("use the full id.")
	return b.String()
}
func (e *AmbiguousIDError) ExitCode() int { return 1 }

// RetractAuthorError fires when an agent tries to retract a thought
// authored by a different agent. Exit 1.
type RetractAuthorError struct {
	ID     string
	Author string
}

func (e *RetractAuthorError) Error() string {
	return fmt.Sprintf("cannot retract thought %q: authored by %q (only the author can retract)", e.ID, e.Author)
}
func (e *RetractAuthorError) ExitCode() int { return 1 }

// PrivateRecordAuthzError fires when a non-author tries to write a
// social-validation record (confirm/refute) against a scope:agent
// thought. Exit 1.
//
// Verb is "confirm" or "refute" so the rendered message is precise.
// ID and Author identify the target. The message is deliberately
// explicit about WHY the rejection happened (scope:agent + author
// mismatch) so a cold agent can self-correct from one line of stderr.
type PrivateRecordAuthzError struct {
	Verb   string
	ID     string
	Author string
}

func (e *PrivateRecordAuthzError) Error() string {
	return fmt.Sprintf("cannot %s thought %q: scope:agent record authored by %q (only the author can %s their own private record)",
		e.Verb, e.ID, e.Author, e.Verb)
}
func (e *PrivateRecordAuthzError) ExitCode() int { return 1 }

// InvalidTypesError fires when --types contains an unknown type. Exit 2.
type InvalidTypesError struct{ Value string }

func (e *InvalidTypesError) Error() string {
	return fmt.Sprintf("invalid --types %q: must be from given|learned|thought|observation|reason|summon|channel-message|goal", e.Value)
}
func (e *InvalidTypesError) ExitCode() int { return 2 }

// ThoughtTypeAsRecordTypeError fires when --types= receives a token
// that is a thought-SUBTYPE (decision|hypothesis|focus|question) rather
// than a record type. P3/R31 fix: instead of the generic
// InvalidTypesError dump, redirect the agent to the corrected shape
// (--types=thought --thought-types=<subtype>). Exit 2.
type ThoughtTypeAsRecordTypeError struct{ Value string }

func (e *ThoughtTypeAsRecordTypeError) Error() string {
	return fmt.Sprintf("%q is a thought-type, not a record-type. Use --types=thought --thought-types=%s.", e.Value, e.Value)
}
func (e *ThoughtTypeAsRecordTypeError) ExitCode() int { return 2 }

// InvalidThoughtTypesError fires when --thought-types contains an
// unknown thought-subtype. The allowed enum mirrors
// thought.allowedTypes (decision|hypothesis|observation|focus|question).
// Exit 2.
type InvalidThoughtTypesError struct{ Value string }

func (e *InvalidThoughtTypesError) Error() string {
	return fmt.Sprintf("invalid --thought-types %q: must be from decision|hypothesis|observation|focus|question", e.Value)
}
func (e *InvalidThoughtTypesError) ExitCode() int { return 2 }

// InvalidDurationError fires when --since fails Go duration parse. Exit 2.
type InvalidDurationError struct{ Raw string }

func (e *InvalidDurationError) Error() string {
	return fmt.Sprintf("invalid --since %q: must be a Go duration (e.g., 10m, 2h, 24h)", e.Raw)
}
func (e *InvalidDurationError) ExitCode() int { return 2 }

// InvalidTimestampError fires when --as-of or --by fails RFC3339 parse. Exit 2.
type InvalidTimestampError struct{ Raw string }

func (e *InvalidTimestampError) Error() string {
	return fmt.Sprintf("invalid timestamp %q: must be RFC3339 (e.g., 2026-05-12T12:00:00Z)", e.Raw)
}
func (e *InvalidTimestampError) ExitCode() int { return 2 }

// InvalidStageTransitionError fires when approve/promote requests a
// stage transition that's not allowed (e.g., approve from staged, or
// promote from already-live). Exit 2.
type InvalidStageTransitionError struct {
	Path, From, To string
}

func (e *InvalidStageTransitionError) Error() string {
	return fmt.Sprintf("cannot transition %q from stage %q to %q", e.Path, e.From, e.To)
}
func (e *InvalidStageTransitionError) ExitCode() int { return 2 }

// AlreadyExpiredError is returned by ttlsweep.Move when the destination
// live/expired/<agent>/<id>.gdl already exists (e.g., outbox + inbox
// copies both expire in the same sweep tick). Per D14.6, callers
// log-and-skip; the audit trail keeps the first-moved copy.
type AlreadyExpiredError struct {
	Agent string
	ID    string
}

func (e *AlreadyExpiredError) Error() string {
	return fmt.Sprintf("expired/%s/%s.gdl already exists", e.Agent, e.ID)
}

func (e *AlreadyExpiredError) ExitCode() int { return 1 }

// NoSuchSummonError fires when accept/decline targets a summon-id that
// either doesn't exist anywhere, or exists but is no longer in pending
// (already accepted/declined/expired). Per design §4.A line 351 + D15.10.
// Exit 1.
type NoSuchSummonError struct{ ID string }

func (e *NoSuchSummonError) Error() string {
	return fmt.Sprintf("no such summon %q (not found in live/summons/pending/)", e.ID)
}
func (e *NoSuchSummonError) ExitCode() int { return 1 }

// SummonAuthError fires when an agent other than the summon's target
// attempts to accept or decline it. Per D15.9. Exit 1.
type SummonAuthError struct {
	ID     string
	Target string
}

func (e *SummonAuthError) Error() string {
	return fmt.Sprintf("cannot accept/decline summon %q: only %q may respond", e.ID, e.Target)
}
func (e *SummonAuthError) ExitCode() int { return 1 }

// InvalidTopicError fires when --topic is empty or fails the topic regex
// [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)*. Exit 2 — usage shape, matching the
// existing InvalidSubjectError/InvalidPredicateError pattern. (Plan D15.16
// said "all exit 1"; that's wrong for a usage-shaped validation error per
// the design §4.A roster.)
type InvalidTopicError struct{ Topic string }

func (e *InvalidTopicError) Error() string {
	if e.Topic == "" {
		return "--topic must not be empty"
	}
	return fmt.Sprintf("invalid --topic %q: must match [a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)*", e.Topic)
}
func (e *InvalidTopicError) ExitCode() int { return 2 }

// NoSuchChannelError fires when say/leave/close targets a ch-id that has
// no live/channels/active/<ch-id>/meta.gdl (and isn't in closed/ either
// for the read-side LoadMeta path). Per D16.12. Exit 1.
type NoSuchChannelError struct{ ID string }

func (e *NoSuchChannelError) Error() string {
	return fmt.Sprintf("no such channel: %s", e.ID)
}
func (e *NoSuchChannelError) ExitCode() int { return 1 }

// NotChannelMemberError fires when an agent tries to say/leave on a
// channel they're not a current member of (never joined, or already
// left). Per D16.3 + D16.12. Exit 1.
type NotChannelMemberError struct {
	ID    string
	Agent string
}

func (e *NotChannelMemberError) Error() string {
	return fmt.Sprintf("agent %s is not a current member of channel %s", e.Agent, e.ID)
}
func (e *NotChannelMemberError) ExitCode() int { return 1 }

// NotChannelOpenerError fires when a non-opener tries to close a
// channel. Per D16.5 + D16.12. Exit 1.
type NotChannelOpenerError struct {
	ID     string
	Agent  string
	Opener string
}

func (e *NotChannelOpenerError) Error() string {
	return fmt.Sprintf("agent %s cannot close channel %s: only opener %s may close", e.Agent, e.ID, e.Opener)
}
func (e *NotChannelOpenerError) ExitCode() int { return 1 }

// ChannelShowNotAuthorizedError fires when `rufio channel show <ch-id>`
// is called by an agent who is neither a current nor past member of
// the channel. The read API is gated on ever-membership (opener or
// target) per #142 — a third party gets the same hard-stop they would
// from say/leave, but the message is read-shaped ("not authorized")
// rather than write-shaped ("not a current member").
type ChannelShowNotAuthorizedError struct {
	ID    string
	Agent string
}

func (e *ChannelShowNotAuthorizedError) Error() string {
	return fmt.Sprintf("not authorized; you are not a member of channel %s", e.ID)
}
func (e *ChannelShowNotAuthorizedError) ExitCode() int { return 1 }

// NoSuchGoalError fires when complete/abandon targets a goal-id that
// either doesn't exist anywhere, or exists but is no longer in active
// (already completed/abandoned). Per D17.13. Exit 1.
type NoSuchGoalError struct{ ID string }

func (e *NoSuchGoalError) Error() string { return fmt.Sprintf("no such goal: %s", e.ID) }
func (e *NoSuchGoalError) ExitCode() int { return 1 }

// GoalAuthError fires when an agent other than the goal's original
// @goal.author attempts to complete or abandon it. Per D17.8. Exit 1.
type GoalAuthError struct{ ID, Author string }

func (e *GoalAuthError) Error() string {
	return fmt.Sprintf("agent cannot complete/abandon goal %s: only author %s may do so", e.ID, e.Author)
}
func (e *GoalAuthError) ExitCode() int { return 1 }

// GoalActiveChildrenError fires when `goal complete` / `goal abandon`
// would silently orphan still-active children (#130). Surfaces the IDs
// so the user can act on them without grepping; mentions --force as the
// escape hatch. Exit 1 (state-shape error, not usage).
type GoalActiveChildrenError struct {
	ID       string
	Op       string // "complete" or "abandon" — flows into the message verbatim.
	Children []string
}

func (e *GoalActiveChildrenError) Error() string {
	noun := "child"
	if len(e.Children) != 1 {
		noun = "children"
	}
	return fmt.Sprintf(
		"goal %s has %d active %s: [%s]; complete or abandon them first, or pass --force to override",
		e.ID, len(e.Children), noun, strings.Join(e.Children, ", "),
	)
}
func (e *GoalActiveChildrenError) ExitCode() int { return 1 }

// InvalidStatementError fires when --statement is empty after TrimSpace.
// Exit 2 (usage shape) — matches InvalidContentError pattern. Per D17.13.
type InvalidStatementError struct{}

func (e *InvalidStatementError) Error() string { return "--statement must not be empty" }
func (e *InvalidStatementError) ExitCode() int { return 2 }

// NoSuchDecisionError fires when `rufio lineage <id>` cannot locate the
// decision in either live/outbox/*/<id>.gdl or live/expired/*/<id>.gdl.
// Per design §4.A line 348. Exit 1.
type NoSuchDecisionError struct{ ID string }

func (e *NoSuchDecisionError) Error() string { return fmt.Sprintf("no such decision: %s", e.ID) }
func (e *NoSuchDecisionError) ExitCode() int { return 1 }

// NotADecisionError fires when `rufio lineage <id>` resolves to a
// @thought record whose type field is not "decision" (e.g., the id
// names a hypothesis or observation). Per design §4.A line 349. Exit 1.
//
// Cold-vet R34 (smaller-model friction, GH #177) showed the error
// without the hint sent agents into the wrong verb. Now self-teaches
// the cognition-state-machine: elevate a hypothesis to a decision via
// `think --type=decision`, or chain refinements via `think --parent=`.
type NotADecisionError struct{ ID, Type string }

func (e *NotADecisionError) Error() string {
	return fmt.Sprintf(
		"thought %s is type %q, not 'decision'. "+
			"To chain reasoning, elevate to a decision first: "+
			"`rufio think --type=decision --content=\"...\" --subject=...`. "+
			"Or refine the hypothesis itself with `think --parent=%s`.",
		e.ID, e.Type, e.ID,
	)
}
func (e *NotADecisionError) ExitCode() int { return 1 }

// NoAttentionError fires when `rufio attention <agent>` targets an
// agent who has no live/attention/<agent>.gdl file (never attended, or
// the file was removed). Per D20.2 + D20.5 + design §4.A roster. Exit 1.
type NoAttentionError struct{ Agent string }

func (e *NoAttentionError) Error() string {
	return fmt.Sprintf("no attention record for agent %s", e.Agent)
}
func (e *NoAttentionError) ExitCode() int { return 1 }

// InvalidPersonaError fires when `rufio swarm spawn --persona=<text>`
// receives an empty value or one that doesn't match the agent-id regex
// [a-z][a-z0-9-]*. Personas become id prefixes (`<persona>-<seq>`) so
// they share the identity grammar. Exit 2 — usage shape. Per D21.2 +
// D21.8.
type InvalidPersonaError struct{ Persona string }

func (e *InvalidPersonaError) Error() string {
	if e.Persona == "" {
		return "--persona must not be empty"
	}
	return fmt.Sprintf("--persona %q: must match [a-z][a-z0-9-]*", e.Persona)
}
func (e *InvalidPersonaError) ExitCode() int { return 2 }

// InvalidCountError fires when `rufio swarm spawn --count=<n>` is
// outside the inclusive range 1..50. The cap is pragmatic — keeps demo
// runs sane and bounds the on-disk write. Exit 2. Per D21.3 + D21.8.
type InvalidCountError struct{ Count int }

func (e *InvalidCountError) Error() string {
	return fmt.Sprintf("--count %d: must be between 1 and 50", e.Count)
}
func (e *InvalidCountError) ExitCode() int { return 2 }

// DemoStateError fires when `rufio demo` cannot proceed because the
// live tree is non-empty (without --reset) or because the daemon pid
// file fails to appear within the spawn deadline. Both are
// pre-flight/orchestration failures the operator must resolve before
// the showcase can run. Exit 2 — usage shape (the operator needs to
// add --reset, or stop the existing daemon and retry). Per D24.11.
type DemoStateError struct{ Reason string }

func (e *DemoStateError) Error() string {
	return fmt.Sprintf("rufio demo: %s", e.Reason)
}
func (e *DemoStateError) ExitCode() int { return 2 }
