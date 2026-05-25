package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/observation"
	"github.com/d-mcmillan/rufio/internal/lib/reason"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// Topics: MCP tools take a native []string (the CLI parses a CSV flag).
// We only normalise nil↔empty via normTopics/jsonTopics/nonNil (defined
// at the bottom of this file); validation/order is identical to the CLI
// because the same lib validators are shared.

// ---- think ----

type thinkIn struct {
	Type    string `json:"type" jsonschema:"thought type: hypothesis|observation|decision|question|focus (required)"`
	Subject string `json:"subject" jsonschema:"entity id this thought is about, namespace:local e.g. customer:5821 (required)"`
	Content string `json:"content" jsonschema:"free-text content of the thought (required)"`
	// H3a (#125): scope defaults to "fleet" when omitted, matching the
	// CLI's unified write-verb default.
	Scope  string   `json:"scope,omitempty" jsonschema:"visibility scope: agent|deployment|fleet (default fleet)"`
	TTL    string   `json:"ttl,omitempty" jsonschema:"expiry in seconds (optional; default never expires)"`
	Parent string   `json:"parent,omitempty" jsonschema:"parent thought id (optional)"`
	Topics []string `json:"topics,omitempty" jsonschema:"topic tokens (optional)"`
}

// thinkOut mirrors runThink's --json payload keys EXACTLY (see
// internal/cli/think.go runThink): _type="think", _version="1", id,
// author, type, subject, content, scope, ts, ttl (int seconds),
// bundle_refs (always a non-nil array), topics (always a non-nil array),
// parent (the id string, or JSON null when unset).
type thinkOut struct {
	Type       string   `json:"_type"`
	Version    string   `json:"_version"`
	ID         string   `json:"id"`
	Author     string   `json:"author"`
	ThoughtTyp string   `json:"type"`
	Subject    string   `json:"subject"`
	Content    string   `json:"content"`
	Scope      string   `json:"scope"`
	TS         string   `json:"ts"`
	TTL        int      `json:"ttl"`
	BundleRefs []string `json:"bundle_refs"`
	Topics     []string `json:"topics"`
	Parent     *string  `json:"parent"`
}

func registerThink(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "think",
		Description: "Write a thought (ambient broadcast) to live/outbox/. type=decision also writes a sibling context bundle.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in thinkIn) (*mcp.CallToolResult, thinkOut, error) {
		content := strings.TrimSpace(in.Content)
		if err := thought.ValidateContent(content); err != nil {
			return nil, thinkOut{}, toolErr(err)
		}
		if err := thought.ValidateType(in.Type); err != nil {
			return nil, thinkOut{}, toolErr(err)
		}
		if err := thought.ValidateSubject(in.Subject); err != nil {
			return nil, thinkOut{}, toolErr(err)
		}
		// H3a (#125): default scope to fleet — mirror CLI runThink.
		scope := strings.TrimSpace(in.Scope)
		if scope == "" {
			scope = "fleet"
		}
		if err := thought.ValidateScope(scope); err != nil {
			return nil, thinkOut{}, toolErr(err)
		}
		in.Scope = scope
		ttl, err := thought.ParseTTL(in.TTL)
		if err != nil {
			return nil, thinkOut{}, toolErr(err)
		}
		if err := thought.ValidateParent(in.Parent); err != nil {
			return nil, thinkOut{}, toolErr(err)
		}
		topics := normTopics(in.Topics)
		if err := attention.ValidateTopics(topics); err != nil {
			return nil, thinkOut{}, toolErr(err)
		}

		id, err := thought.GenerateID()
		if err != nil {
			return nil, thinkOut{}, toolErr(err)
		}
		ts := versioning.NowISO()

		thoughtRec := thought.BuildThoughtRecord(thought.ThoughtInput{
			ID: id, Author: r.Agent, Type: in.Type, Subject: in.Subject,
			Content: content, Scope: in.Scope, Topics: topics,
			TS: ts, TTL: ttl, Parent: in.Parent,
		})
		records := []gdl.Record{thoughtRec}
		var bundleRefs []string
		if in.Type == "decision" {
			bundleRefs, err = thought.CollectGivenLearnedSHAs(r.Root)
			if err != nil {
				return nil, thinkOut{}, toolErr(err)
			}
			records = append(records, thought.BuildContextBundle(id, bundleRefs))
		}
		if err := thought.Write(r.Root, r.Agent, id, records); err != nil {
			return nil, thinkOut{}, toolErr(err)
		}

		out := thinkOut{
			Type: "think", Version: "1", ID: id, Author: r.Agent,
			ThoughtTyp: in.Type, Subject: in.Subject, Content: content,
			Scope: in.Scope, TS: ts, TTL: ttl,
			BundleRefs: nonNil(bundleRefs), Topics: jsonTopics(topics),
		}
		if in.Parent != "" {
			p := in.Parent
			out.Parent = &p
		}
		return nil, out, nil
	})
}

// ---- observe ----

type observeIn struct {
	Subject   string `json:"subject" jsonschema:"entity id this observation is about, namespace:local e.g. customer:5821 (required)"`
	Predicate string `json:"predicate" jsonschema:"relationship (required, kebab/snake-case)"`
	Object    string `json:"object" jsonschema:"value/object of the observation (required, free-text)"`
	// H3a (#125): scope defaults to "fleet" when omitted, matching the
	// CLI's unified write-verb default.
	Scope      string   `json:"scope,omitempty" jsonschema:"visibility scope: agent|deployment|fleet (default fleet)"`
	Confidence string   `json:"confidence,omitempty" jsonschema:"confidence in [0,1] (optional; default 1.0)"`
	Topics     []string `json:"topics,omitempty" jsonschema:"topic tokens (optional)"`
}

// observeOut mirrors runObserve's --json payload keys EXACTLY (see
// internal/cli/observe.go): _type="observe", _version="1", id, author,
// subject, predicate, object, scope, confidence (float), ts, topics
// (always a non-nil array).
type observeOut struct {
	Type       string   `json:"_type"`
	Version    string   `json:"_version"`
	ID         string   `json:"id"`
	Author     string   `json:"author"`
	Subject    string   `json:"subject"`
	Predicate  string   `json:"predicate"`
	Object     string   `json:"object"`
	Scope      string   `json:"scope"`
	Confidence float64  `json:"confidence"`
	TS         string   `json:"ts"`
	Topics     []string `json:"topics"`
}

func registerObserve(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "observe",
		Description: "Record a durable observation (subject-predicate-object triple) under learned/.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in observeIn) (*mcp.CallToolResult, observeOut, error) {
		if err := thought.ValidateSubject(in.Subject); err != nil {
			return nil, observeOut{}, toolErr(err)
		}
		if err := observation.ValidatePredicate(in.Predicate); err != nil {
			return nil, observeOut{}, toolErr(err)
		}
		object := strings.TrimSpace(in.Object)
		if err := observation.ValidateObject(object); err != nil {
			return nil, observeOut{}, toolErr(err)
		}
		// H3a (#125): default scope to fleet — mirror CLI runObserve.
		scope := strings.TrimSpace(in.Scope)
		if scope == "" {
			scope = "fleet"
		}
		if err := thought.ValidateScope(scope); err != nil {
			return nil, observeOut{}, toolErr(err)
		}
		in.Scope = scope
		confidence, err := observation.ParseConfidence(in.Confidence)
		if err != nil {
			return nil, observeOut{}, toolErr(err)
		}
		topics := normTopics(in.Topics)
		if err := attention.ValidateTopics(topics); err != nil {
			return nil, observeOut{}, toolErr(err)
		}

		id, err := thought.GenerateID()
		if err != nil {
			return nil, observeOut{}, toolErr(err)
		}
		ts := versioning.NowISO()

		rec := observation.BuildObservationRecord(observation.ObservationInput{
			ID: id, Author: r.Agent, Subject: in.Subject, Predicate: in.Predicate,
			Object: object, Scope: in.Scope, Topics: topics,
			Confidence: confidence, TS: ts,
		})
		if err := observation.Write(r.Root, in.Subject, id, rec); err != nil {
			return nil, observeOut{}, toolErr(err)
		}
		return nil, observeOut{
			Type: "observe", Version: "1", ID: id, Author: r.Agent,
			Subject: in.Subject, Predicate: in.Predicate, Object: object,
			Scope: in.Scope, Confidence: confidence, TS: ts,
			Topics: jsonTopics(topics),
		}, nil
	})
}

// ---- reason ----

type reasonIn struct {
	Content  string   `json:"content" jsonschema:"reasoning step content (required)"`
	Subject  string   `json:"subject,omitempty" jsonschema:"entity id this reasoning is about, namespace:local e.g. customer:5821 (optional)"`
	Parent   string   `json:"parent,omitempty" jsonschema:"parent reason id (optional)"`
	Decision string   `json:"decision,omitempty" jsonschema:"decision thought id this reasons under (optional)"`
	Topics   []string `json:"topics,omitempty" jsonschema:"topic tokens (optional)"`
	// Scope mirrors the CLI's --scope flag (#125). Defaults to "fleet"
	// when omitted — reasoning under a decision is broadcast by default.
	Scope string `json:"scope,omitempty" jsonschema:"visibility scope (agent|deployment|fleet), default fleet"`
}

// reasonOut mirrors runReason's --json payload keys EXACTLY (see
// internal/cli/reason.go): _type="reason", _version="1", id, author,
// content, ts, topics (always a non-nil array), parent (id or null),
// decision (id or null), subject (id or null — P2/R31).
type reasonOut struct {
	Type     string   `json:"_type"`
	Version  string   `json:"_version"`
	ID       string   `json:"id"`
	Author   string   `json:"author"`
	Content  string   `json:"content"`
	Scope    string   `json:"scope"`
	TS       string   `json:"ts"`
	Topics   []string `json:"topics"`
	Parent   *string  `json:"parent"`
	Decision *string  `json:"decision"`
	Subject  *string  `json:"subject"`
}

func registerReason(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "reason",
		Description: "Capture a step in the agent's reasoning chain under live/reasoning/.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in reasonIn) (*mcp.CallToolResult, reasonOut, error) {
		content := strings.TrimSpace(in.Content)
		if err := thought.ValidateContent(content); err != nil {
			return nil, reasonOut{}, toolErr(err)
		}
		if err := thought.ValidateParent(in.Parent); err != nil {
			return nil, reasonOut{}, toolErr(err)
		}
		if err := reason.ValidateDecision(in.Decision); err != nil {
			return nil, reasonOut{}, toolErr(err)
		}
		// P2/R31: optional subject (mirrors runReason). Empty allowed
		// for legacy parity; non-empty validates against the entity-id
		// regex.
		subject := strings.TrimSpace(in.Subject)
		if subject != "" {
			if err := thought.ValidateSubject(subject); err != nil {
				return nil, reasonOut{}, toolErr(err)
			}
		}
		topics := normTopics(in.Topics)
		if err := attention.ValidateTopics(topics); err != nil {
			return nil, reasonOut{}, toolErr(err)
		}
		// Resolve + type-check --decision BEFORE writing (GH #77): a
		// non-empty decision must name an existing type:decision thought.
		if err := reason.ValidateDecisionTarget(r.Root, in.Decision); err != nil {
			return nil, reasonOut{}, toolErr(err)
		}
		// #125: mirror runReason's default — empty -> fleet — and
		// validate against the canonical enum so a malformed value
		// errors before the disk write.
		scope := strings.TrimSpace(in.Scope)
		if scope == "" {
			scope = "fleet"
		}
		if err := thought.ValidateScope(scope); err != nil {
			return nil, reasonOut{}, toolErr(err)
		}

		id, err := thought.GenerateID()
		if err != nil {
			return nil, reasonOut{}, toolErr(err)
		}
		ts := versioning.NowISO()

		rec := reason.BuildRecord(reason.ReasonInput{
			ID: id, Author: r.Agent, Content: content, Scope: scope,
			Subject: subject, Topics: topics,
			Parent: in.Parent, Decision: in.Decision, TS: ts,
		})
		if err := reason.Write(r.Root, r.Agent, in.Decision, id, rec); err != nil {
			return nil, reasonOut{}, toolErr(err)
		}

		out := reasonOut{
			Type: "reason", Version: "1", ID: id, Author: r.Agent,
			Content: content, Scope: scope, TS: ts, Topics: jsonTopics(topics),
		}
		if in.Parent != "" {
			p := in.Parent
			out.Parent = &p
		}
		if in.Decision != "" {
			d := in.Decision
			out.Decision = &d
		}
		// P2/R31: subject is nullable in JSON, matching the CLI shape.
		if subject != "" {
			out.Subject = &subject
		}
		return nil, out, nil
	})
}

// ---- retract ----

type retractIn struct {
	ThoughtID string `json:"thought_id" jsonschema:"the id of the thought to retract (must be authored by this agent)"`
	Reason    string `json:"reason" jsonschema:"free-text reason for retraction (required)"`
}

// retractOut mirrors runRetract's --json payload keys EXACTLY (see
// internal/cli/retract.go): _type="retract", _version="1", target,
// reason, by, ts.
type retractOut struct {
	Type    string `json:"_type"`
	Version string `json:"_version"`
	Target  string `json:"target"`
	Reason  string `json:"reason"`
	By      string `json:"by"`
	TS      string `json:"ts"`
}

func registerRetract(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "retract",
		Description: "Retract one of this agent's own thoughts (writes live/retracted/<id>.gdl).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in retractIn) (*mcp.CallToolResult, retractOut, error) {
		reasonText := strings.TrimSpace(in.Reason)
		if reasonText == "" {
			return nil, retractOut{}, toolErr(&rufioerr.InvalidContentError{Field: "reason"})
		}
		author, err := retract.Lookup(r.Root, in.ThoughtID)
		if err != nil {
			return nil, retractOut{}, toolErr(err)
		}
		if author != r.Agent {
			return nil, retractOut{}, toolErr(&rufioerr.RetractAuthorError{ID: in.ThoughtID, Author: author})
		}
		ts := versioning.NowISO()
		rec := retract.BuildRecord(in.ThoughtID, reasonText, r.Agent, ts)
		if err := retract.Write(r.Root, in.ThoughtID, rec); err != nil {
			return nil, retractOut{}, toolErr(err)
		}
		return nil, retractOut{
			Type: "retract", Version: "1", Target: in.ThoughtID,
			Reason: reasonText, By: r.Agent, TS: ts,
		}, nil
	})
}

// normTopics mirrors splitCSVTrim's nil semantics for the validation path:
// the lib validators see the same slice the CLI's splitCSVTrim produced
// (nil for "none"). The MCP arg is already []string; an empty/absent slice
// → nil so attention.ValidateTopics behaves identically to the CLI.
func normTopics(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return in
}

// jsonTopics mirrors the CLI's "topics is always a non-nil JSON array"
// rule (runThink/runObserve/runReason: nil → []string{}).
func jsonTopics(topics []string) []string {
	if topics == nil {
		return []string{}
	}
	return topics
}

// nonNil mirrors bundleRefsOrEmpty: nil → []string{} so bundle_refs is
// always a JSON array.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
