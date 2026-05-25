// live_write.go — G-interact: the lib-backed substrate WRITE path.
//
// SCOPE (G-interact, damon-approved 2026-05-16): make the v8 substrate
// console WRITE to the substrate. The whole v8 read-only stack (G1 chat /
// G2 mesh / G3 tabs+lineage) stays intact; this adds the composer-driven
// write side ONLY. (Was preview-only behind RUFIO_TUI_PREVIEW with the
// legacy tui.Model as the default; the G4 cutover, 2026-05-17, made v8
// the unconditional `rufio tui` and deleted the gate + legacy Model.)
//
// LOAD-BEARING PRINCIPLE (do not violate): Rufio is substrate-agnostic;
// the operator is just an agent driving the CLI. The TUI writes by
// calling the SAME internal substrate libs the `rufio` CLI commands use —
// it does NOT shell out to the `rufio` binary and does NOT embed any
// model/agent SDK. Each emit* below mirrors, line-for-line, the
// filesystem-touching tail of the corresponding internal/cli command's
// runX function (validate → resolve identity → build record → lib Write),
// MINUS the cobra wiring + the output.RenderOpts terminal chatter (the
// TUI renders results in-pane via the existing watcher fold, never to
// stdout). The exact CLI entrypoints reproduced here, file:line:
//
//   - emitThought  ⇐ cli/think.go:67  runThink   → thought.Write
//     (thought.ValidateContent/Type/Subject/Scope,
//     thought.GenerateID, versioning.NowISO,
//     thought.BuildThoughtRecord, thought.Write)
//   - emitConfirm  ⇐ cli/confirm.go:46 runConfirm → confirm.Append
//     (retract.Lookup, confirm.BuildConfirm, confirm.Append)
//   - emitRefute   ⇐ cli/refute.go:50  runRefute  → confirm.Append
//     (retract.Lookup, confirm.BuildRefute, confirm.Append)
//   - emitGoal     ⇐ cli/goal.go:74    runGoalWrite → goal.WriteActive
//   - emitObserve  ⇐ cli/observe.go:63 runObserve → observation.Write
//   - emitAttend   ⇐ cli/attend.go:50  runAttend  → attention.Write
//   - emitSummon   ⇐ cli/summon.go:47  runSummon  → summon.WritePending
//   - emitSay      ⇐ cli/say.go:50     runSay     → channels.WriteMessage
//   - emitDirected ⇐ the @agent transport: reuse-or-open a channel
//     EXACTLY as the CLI primitives allow (say into an
//     existing channel where `me` is a current member,
//     else summon — see emitDirected's doc for why a
//     unilateral channel-create is impossible by design).
//
// IDENTITY: every record is authored as the resolved operator `me` (the
// identity already on App from G1 — identity.Resolve, default
// operatorFallbackID "operator"). `me` is threaded in as a param (NOT
// re-resolved) so the TUI writes as exactly the identity it reads/renders
// as — the thin-client contract. ts is versioning.NowISO() (the SAME
// stamp the CLI uses; it is wall-clock, but tests assert the RECORD and
// the subsequent RENDER, never a golden-pinned ts — the determinism
// contract is preserved because the composer-buffer goldens pin a fixed
// buffer state, not a written record).
//
// Every emit* returns an error on bad input (the lib Validate* / a
// resolution failure) — the caller (app.go's Enter routing) renders it
// in-pane per the existing convention and NEVER crashes / NEVER bubbles
// an exit code (the read-only console posture extended to writes).
package tui

import (
	"strings"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/observation"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// ── free-text broadcast defaults (damon-approved 2026-05-16) ──────────
//
// Plain composer text → a broadcast operator @thought. The approved
// context-aware defaults:
//
//   - opThoughtType = "focus": the operator steering/focusing the fleet.
//     thought.ValidateType (thought.go:44) accepts exactly
//     hypothesis|observation|decision|question|focus; `focus` is the
//     closest Rufio thought type to "the operator is directing the
//     fleet's attention" (a decision needs a quorum/lineage; a
//     hypothesis/observation/question are agent-cognition kinds). So a
//     bare operator broadcast is a `focus` thought — documented WHY here
//     per the approved scope.
//   - opThoughtScope = "fleet": the broadcast is fleet-wide (every agent
//     sees it), the widest thought.ValidateScope (thought.go:67) value —
//     an operator steering message is for the whole fleet.
const (
	opThoughtType  = "focus"
	opThoughtScope = "fleet"
)

// opSubjectFallback is the documented general fallback subject for a
// free-text broadcast when NEITHER the selected row NOR the most-recent
// thread row resolves a subject (the approved "sane constant"). It MUST
// satisfy thought.ValidateSubject's entity-id regex
// `[a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+` (thought.go:55) — a bare token
// like `general` is REJECTED by the same validator the CLI uses, so the
// fallback is the entity form `fleet:general`: an un-targeted fleet-wide
// operator steering note. Self-documenting in the rendered row and the
// SAME shape an agent's subject takes (customer:5821), so it reads
// consistently. (Resolved scope ambiguity: the approved scope said "a
// sane constant, e.g. general"; `general` alone fails the shared
// validator, so the entity-form `fleet:general` is the honest realisation
// of that intent.)
const opSubjectFallback = "fleet:general"

// emitThought writes a broadcast operator @thought, reproducing
// cli/think.go:67 runThink's filesystem tail (validate → GenerateID →
// NowISO → BuildThoughtRecord → thought.Write) MINUS cobra/output. The
// type/scope are the approved context-aware defaults (opThoughtType /
// opThoughtScope); subject is resolved by the caller (the focused
// entity — see resolveBroadcastSubject) and passed in. Authored as `me`.
// content is trimmed + validated by the SAME thought.ValidateContent the
// CLI uses, so an empty/over-long buffer surfaces the SAME error class
// (rendered in-pane, never a crash).
func emitThought(root, me, subject, content string) error {
	content = strings.TrimSpace(content)
	if err := thought.ValidateContent(content); err != nil {
		return err
	}
	if err := thought.ValidateType(opThoughtType); err != nil {
		return err
	}
	if err := thought.ValidateSubject(subject); err != nil {
		return err
	}
	if err := thought.ValidateScope(opThoughtScope); err != nil {
		return err
	}
	id, err := thought.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: me, Type: opThoughtType, Subject: subject,
		Content: content, Scope: opThoughtScope, TS: ts,
	})
	// type != decision → no @context-bundle (cli/think.go:117 only bundles
	// decisions; a focus thought is a single-record write).
	return thought.Write(root, me, id, []gdl.Record{rec})
}

// emitConfirm writes an @confirm by `me` against targetID, reproducing
// cli/confirm.go:46 runConfirm's tail (retract.Lookup verifies the target
// exists — anyone may confirm, no author match — then confirm.BuildConfirm
// + confirm.Append). evidence is optional (the CLI's --evidence). The
// in-pane demo centerpiece: confirm the selected DECISION row → the
// watcher fold + the immediate post-write reload re-read the tally and
// the quorum dots advance (2/3 → 3/3 → auto-promote crossing).
func emitConfirm(root, me, targetID, evidence string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return &rufioerr.InvalidContentError{Field: "thought-id"}
	}
	if _, err := retract.Lookup(root, targetID); err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := confirm.BuildConfirm(targetID, me, strings.TrimSpace(evidence), ts)
	return confirm.Append(root, targetID, rec)
}

// emitRefute writes an @refute by `me` against targetID, reproducing
// cli/refute.go:50 runRefute's tail (reason required + trimmed →
// retract.Lookup → confirm.BuildRefute → confirm.Append, the SAME
// per-thought tally file confirms append to). reason is required (the
// CLI's --reason); an empty reason surfaces the SAME InvalidContentError.
func emitRefute(root, me, targetID, reason, evidence string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return &rufioerr.InvalidContentError{Field: "thought-id"}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return &rufioerr.InvalidContentError{Field: "reason"}
	}
	if _, err := retract.Lookup(root, targetID); err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := confirm.BuildRefute(targetID, me, reason, strings.TrimSpace(evidence), ts)
	return confirm.Append(root, targetID, rec)
}

// emitGoal writes an active @goal by `me`, reproducing cli/goal.go:74
// runGoalWrite's tail (goal.ValidateStatement + thought.ValidateScope →
// goal.GenerateID → goal.BuildGoalRecord → goal.WriteActive). scope
// defaults to "agent" (the CLI's flag default, goal.go:61) when the
// caller passes ""; `by`/parent are optional.
func emitGoal(root, me, statement, by, scope string) error {
	statement = strings.TrimSpace(statement)
	if err := goal.ValidateStatement(statement); err != nil {
		return err
	}
	if scope == "" {
		scope = "agent" // cli/goal.go:61 flag default
	}
	if err := thought.ValidateScope(scope); err != nil {
		return err
	}
	id, err := goal.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := goal.BuildGoalRecord(id, me, statement, by, "", scope, ts)
	return goal.WriteActive(root, id, rec)
}

// emitObserve writes an @observation by `me`, reproducing cli/observe.go:63
// runObserve's tail (ValidateSubject/Predicate/Object/Scope →
// thought.GenerateID → observation.BuildObservationRecord →
// observation.Write). confidence defaults to 1.0 (observation.ParseConfidence
// of "" — observation.go:50). scope defaults to "fleet" so a TUI observe
// is fleet-visible like a broadcast (documented sane default; the CLI
// requires --scope but the in-pane minimal-parse form supplies it).
func emitObserve(root, me, subject, predicate, object, scope string) error {
	if err := thought.ValidateSubject(subject); err != nil {
		return err
	}
	if err := observation.ValidatePredicate(predicate); err != nil {
		return err
	}
	object = strings.TrimSpace(object)
	if err := observation.ValidateObject(object); err != nil {
		return err
	}
	if scope == "" {
		scope = opThoughtScope // fleet — a TUI observe is fleet-visible
	}
	if err := thought.ValidateScope(scope); err != nil {
		return err
	}
	id, err := thought.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := observation.BuildObservationRecord(observation.ObservationInput{
		ID: id, Author: me, Subject: subject, Predicate: predicate,
		Object: object, Scope: scope, Confidence: 1.0, TS: ts,
	})
	return observation.Write(root, subject, id, rec)
}

// emitAttend overwrites `me`'s attention, reproducing cli/attend.go:50
// runAttend's tail (ValidateIntent/Entities/Topics → attention.BuildRecord
// → attention.Write — current-state overwrite). entities is ≥1 required
// (attention.ValidateEntities). Scope defaults to "fleet" — the CLI's
// runAttend default (#125), matching attention's broadcast intent.
func emitAttend(root, me, intent string, entities []string) error {
	intent = strings.TrimSpace(intent)
	if err := attention.ValidateIntent(intent); err != nil {
		return err
	}
	if err := attention.ValidateEntities(entities); err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := attention.BuildRecord(me, intent, "fleet", entities, nil, ts)
	return attention.Write(root, me, rec)
}

// emitSummon writes a pending @summon from `me` to `to`, reproducing
// cli/summon.go:47 runSummon's tail (summon.ValidateTopic/Intent →
// summon.GenerateID → summon.BuildSummonRecord (DefaultTTL) →
// summon.WritePending).
func emitSummon(root, me, to, topic, intent string) error {
	if err := summon.ValidateTopic(topic); err != nil {
		return err
	}
	if err := summon.ValidateIntent(intent); err != nil {
		return err
	}
	id, err := summon.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := summon.BuildSummonRecord(id, me, to, topic, intent, ts, summon.DefaultTTL)
	return summon.WritePending(root, id, rec)
}

// emitSay writes an @say by `me` into channel chID, reproducing
// cli/say.go:50 runSay's tail (trim+validate channel/content →
// channels.LoadMeta → closed/membership checks → channels.GenerateMessageID
// → channels.BuildSayRecord → channels.WriteMessage). The closed +
// IsCurrentMember gates are reproduced VERBATIM so a TUI say is exactly
// as authorised as a CLI say (an unauthorised say produces NO side
// effect and surfaces the SAME error in-pane).
func emitSay(root, me, chID, content string) error {
	chID = strings.TrimSpace(chID)
	if chID == "" {
		return &rufioerr.InvalidContentError{Field: "channel"}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return &rufioerr.InvalidContentError{Field: "content"}
	}
	meta, err := channels.LoadMeta(root, chID)
	if err != nil {
		return err
	}
	if meta.Closed {
		return &rufioerr.NoSuchChannelError{ID: chID}
	}
	if !meta.IsCurrentMember(me) {
		return &rufioerr.NotChannelMemberError{ID: chID, Agent: me}
	}
	msgID, err := channels.GenerateMessageID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	rec := channels.BuildSayRecord(msgID, chID, me, content, ts)
	return channels.WriteMessage(root, chID, msgID, rec)
}

// emitDirected is the `@agent <text>` transport. It opens-or-reuses a
// channel EXACTLY as the CLI primitives allow:
//
//   - If an ACTIVE channel already exists where `me` is a current member
//     and the OTHER party is `agent` → `say` into it (emitSay). This is
//     the "reuse a channel" path (cli/say.go behaviour).
//   - Else → `summon` the agent (emitSummon). This is the ONLY
//     operator-initiated channel-open primitive: a channel becomes
//     writable only after the target ACCEPTS the summon (cli/accept.go
//     writes the @channel meta with both parties as members — D15.7). The
//     operator CANNOT unilaterally create a channel it can `say` into
//     (channels.WriteMeta is reached only via `rufio accept`, by the
//     summon's target); attempting to fabricate one would NOT match CLI
//     behaviour and would bypass the summon→accept handshake. So
//     "summon-then-say, matching CLI behaviour" (the approved scope)
//     resolves to: summon now; the say becomes possible once the agent
//     accepts (and a future `@agent` re-send then takes the reuse path).
//     This is the honest substrate semantics, documented per the scope's
//     "if a channel must exist first, summon-then-say" clause.
//
// The topic/intent for the summon are derived from the message text (the
// operator's directed line IS the intent; the topic is a stable token so
// the channel is greppable) — minimal, no extra prompting (tier-3 admin
// is explicitly out of scope). Returns a human action note + any error;
// the caller renders the note in-pane.
func emitDirected(root, me, agent, text string) (string, error) {
	agent = strings.TrimSpace(agent)
	text = strings.TrimSpace(text)
	if agent == "" {
		return "", &rufioerr.InvalidContentError{Field: "agent"}
	}
	if text == "" {
		return "", &rufioerr.InvalidContentError{Field: "content"}
	}
	if chID := findOpenChannelWith(root, me, agent); chID != "" {
		if err := emitSay(root, me, chID, text); err != nil {
			return "", err
		}
		return "said → @" + agent + " (" + chID + ")", nil
	}
	// No reusable channel — summon (the only operator-initiated open).
	// topic: a stable directed-message token (greppable); intent: the
	// operator's line (summon.ValidateIntent requires non-empty — the
	// trimmed text satisfies it).
	if err := emitSummon(root, me, agent, "operator-direct", text); err != nil {
		return "", err
	}
	return "summoned @" + agent + " — awaiting accept to open channel", nil
}

// findOpenChannelWith returns the id of an ACTIVE channel where `me` is a
// current member and the other party is `agent`, or "" if none. It reuses
// the EXISTING InitialWalkPanes channel enumeration (the SAME canonical
// active-only disk read G3's loadTabs / the old tui.go use, via
// projectChannels) so the reuse decision is consistent with what the
// channels tab renders — no second hand-rolled channel scan. Membership
// is re-checked via channels.LoadMeta (the authoritative IsCurrentMember,
// the SAME gate emitSay enforces) so a left/closed channel is never
// reused. Deterministic: the channels are already sorted (newest first)
// by projectChannels, so the freshest reusable channel wins.
func findOpenChannelWith(root, me, agent string) string {
	for _, ch := range projectChannels(InitialWalkPanes(root, me)) {
		if ch.Opener != me && ch.Target != me {
			continue
		}
		other := ch.Opener
		if ch.Opener == me {
			other = ch.Target
		}
		if other != agent {
			continue
		}
		meta, err := channels.LoadMeta(root, ch.ID)
		if err != nil || meta.Closed || !meta.IsCurrentMember(me) {
			continue
		}
		return ch.ID
	}
	return ""
}
