// Package lineage implements the read-side decision audit pipeline:
// linear-scan an id across live/outbox/ + live/expired/, parse the
// embedded @context-bundle, resolve each sha against .rufio/refs/,
// and walk the @reason chain under live/reasoning/*/<id>/ across all
// authors (cross-agent reasoning is first-class — see #138).
//
// Pure read; no daemon, no locks, no writes. Mirrors the patterns used
// by retract.Lookup (filepath.Glob across agent dirs) and recall's
// outbox/expired traversal. Per design §2.D + §4.A.
package lineage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// Decision is the parsed @thought decision record + its enclosing
// @context-bundle records (raw, as gdl.Records).
//
// Bundle holds every @context-bundle record found in the same file as
// the @thought (per L2.9 there is typically exactly one, but the type
// allows multiple for forward-compat).
type Decision struct {
	ID      string
	Author  string
	Subject string
	Content string
	Scope   string
	TS      string
	Expired bool         // true if found in live/expired/
	Bundle  []gdl.Record // @context-bundle records from the SAME file; may be empty
}

// ContextRef is one resolved entry from a @context-bundle's refs list.
// Resolved=false means the sha could not be matched to any @ref record
// under .rufio/refs/ — render as "(unknown sha: <sha>)" per design line
// 350.
type ContextRef struct {
	SHA256   string
	Path     string // empty if sha not resolvable
	Version  int    // 0 if sha not resolvable
	Stage    string // empty if sha not resolvable
	Resolved bool
}

// ReasoningStep is one @reason record in the chain. Depth is the
// distance from the root step (0 for root, 1 for direct child, etc.)
// after sortByChain has run.
type ReasoningStep struct {
	ID      string
	Author  string
	Content string
	Scope   string // #125: on-disk @reason scope; empty for legacy
	// records that pre-date the field. Callers apply privacy at the
	// CLI layer via privacy.IsVisible — the walker stays scope-blind.
	TS       string
	Parent   string // empty for root
	Decision string
	Depth    int // 0 for root step
}

// GetScope satisfies privacy.Record so a ReasoningStep can flow through
// privacy.IsVisible at the lineage CLI layer.
func (s ReasoningStep) GetScope() string { return s.Scope }

// GetAuthor satisfies privacy.Record. The on-disk `author:` of the
// @reason record IS the only agent whose scope:agent steps survive
// privacy filtering for that caller (#125 + privacy.IsVisible).
func (s ReasoningStep) GetAuthor() string { return s.Author }

// LookupDecision linear-scans live/outbox/*/<id>.gdl then
// live/expired/*/<id>.gdl. Returns:
//
//   - *NoSuchDecisionError on miss (or when the first record is not a
//     @thought — treat malformed files as "decision not found" so the
//     audit-side render is uniform with a true miss).
//   - *NotADecisionError when the first @thought record's type field is
//     not "decision".
//   - Decision with Expired=true when found in live/expired/.
//   - Decision.Bundle populated with all @context-bundle records in the
//     same file.
func LookupDecision(root, id string) (Decision, error) {
	expired := false
	pattern := filepath.Join(root, "live", "outbox", "*", id+".gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return Decision{}, err
	}
	if len(matches) == 0 {
		// Try expired.
		pattern = filepath.Join(root, "live", "expired", "*", id+".gdl")
		matches, err = filepath.Glob(pattern)
		if err != nil {
			return Decision{}, err
		}
		if len(matches) == 0 {
			return Decision{}, &rufioerr.NoSuchDecisionError{ID: id}
		}
		expired = true
	}
	// matches[0] = <root>/live/outbox/<author>/<id>.gdl (or expired)
	author := filepath.Base(filepath.Dir(matches[0]))
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		return Decision{}, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		// Malformed file — surface as a miss so the lineage command
		// renders a consistent "decision not found" rather than leaking
		// a parser error to the audit caller.
		return Decision{}, &rufioerr.NoSuchDecisionError{ID: id}
	}
	if len(records) == 0 || records[0].Type != "thought" {
		return Decision{}, &rufioerr.NoSuchDecisionError{ID: id}
	}
	th := records[0]
	if th.Get("type") != "decision" {
		return Decision{}, &rufioerr.NotADecisionError{ID: id, Type: th.Get("type")}
	}
	// Collect @context-bundle records from the same file.
	var bundle []gdl.Record
	for _, r := range records[1:] {
		if r.Type == "context-bundle" {
			bundle = append(bundle, r)
		}
	}
	return Decision{
		ID:      id,
		Author:  author,
		Subject: th.Get("subject"),
		Content: th.Get("content"),
		Scope:   th.Get("scope"),
		TS:      th.Get("ts"),
		Expired: expired,
		Bundle:  bundle,
	}, nil
}

// ResolveBundleRefs takes the bundle records (likely len==1 per L2.9 —
// one @context-bundle per decision), parses each refs:<csv> field, and
// for each sha walks .rufio/refs/**/*.gdl looking for matching
// sha256:<sha>. Returns one ContextRef per (decision, sha) pair, in
// the bundle's declaration order with cross-bundle dedup (a sha
// appearing twice across multiple bundle records resolves once).
//
// Unresolved shas are returned with Resolved=false; ENOENT on a refs
// file is non-fatal (the bundle may reference paths that have since
// been deleted — render as unresolved).
//
// Implementation: walks .rufio/refs/ ONCE up front, building a
// map[string]ContextRef keyed by sha. Per-sha lookup is O(1).
func ResolveBundleRefs(root string, bundleRecords []gdl.Record) ([]ContextRef, error) {
	// Collect SHAs in declaration order, dedup'd across bundle records.
	var shas []string
	seen := make(map[string]bool)
	for _, r := range bundleRecords {
		refsCSV := r.Get("refs")
		if refsCSV == "" {
			continue
		}
		for _, sha := range strings.Split(refsCSV, ",") {
			sha = strings.TrimSpace(sha)
			if sha == "" || seen[sha] {
				continue
			}
			seen[sha] = true
			shas = append(shas, sha)
		}
	}
	if len(shas) == 0 {
		return nil, nil
	}

	// Build sha → ContextRef map by walking .rufio/refs/ once.
	index, err := buildSHAIndex(root)
	if err != nil {
		return nil, err
	}

	out := make([]ContextRef, 0, len(shas))
	for _, sha := range shas {
		if ref, ok := index[sha]; ok {
			out = append(out, ref)
		} else {
			out = append(out, ContextRef{SHA256: sha, Resolved: false})
		}
	}
	return out, nil
}

// buildSHAIndex walks .rufio/refs/ recursively and returns a sha →
// ContextRef map. Multiple @ref records with the same sha (e.g., a
// path promoted draft → staged → live) collapse to the highest-version
// entry to give the most useful annotation.
func buildSHAIndex(root string) (map[string]ContextRef, error) {
	refsDir := filepath.Join(root, ".rufio", "refs")
	index := make(map[string]ContextRef)
	_, statErr := os.Stat(refsDir)
	if errors.Is(statErr, fs.ErrNotExist) {
		return index, nil
	}
	if statErr != nil {
		return nil, statErr
	}
	walkErr := filepath.WalkDir(refsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries — best-effort index build per
			// design line 350 (unresolved shas are not fatal).
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".gdl") {
			return nil
		}
		bs, readErr := os.ReadFile(p)
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				return nil
			}
			return readErr
		}
		records, parseErr := gdl.ParseDocument(string(bs))
		if parseErr != nil {
			// Malformed refs file — skip rather than abort the whole
			// audit. Best-effort per design line 350.
			return nil
		}
		rel, relErr := filepath.Rel(refsDir, p)
		if relErr != nil {
			return relErr
		}
		contentPath := strings.TrimSuffix(filepath.ToSlash(rel), ".gdl")
		for _, r := range records {
			if r.Type != "ref" {
				continue
			}
			sha := r.Get("sha256")
			if sha == "" {
				continue
			}
			version := atoiOrZero(r.Get("version"))
			stage := r.Get("stage")
			candidate := ContextRef{
				SHA256:   sha,
				Path:     contentPath,
				Version:  version,
				Stage:    stage,
				Resolved: true,
			}
			if existing, ok := index[sha]; !ok || candidate.Version > existing.Version {
				index[sha] = candidate
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return index, nil
}

// atoiOrZero parses s as a base-10 integer, returning 0 on failure. The
// version field on a malformed @ref is treated as 0 for index
// purposes; ResolveBundleRefs callers see Version=0 for that ref but
// Resolved=true, matching the existing-but-broken case explicitly.
func atoiOrZero(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// WalkReasoning globs live/reasoning/*/<decisionID>/*.gdl across ALL
// authors (#138 fix), parses @reason records, and returns them sorted
// by parent chain: root first (parent field empty or pointing to a
// non-existent reason id), then depth-first traversal of children.
// Depth is populated. Missing directories → empty slice + nil err.
// Malformed @reason files are skipped silently (best-effort audit per
// design line 350).
//
// Cross-author reasoning matters: when agent-B writes a @reason against
// agent-A's decision, the file lands in live/reasoning/agent-b/<id>/.
// A single-author walk would silently drop it, which is the worst
// possible failure for the shared-cognition primitive — invisible
// reasoning is the same as no reasoning. Each returned ReasoningStep
// carries its own author so callers can show who wrote what.
func WalkReasoning(root, decisionID string) ([]ReasoningStep, error) {
	pattern := filepath.Join(root, "live", "reasoning", "*", decisionID, "*.gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var steps []ReasoningStep
	for _, p := range matches {
		bs, readErr := os.ReadFile(p)
		if readErr != nil {
			// Skip unreadable file — best-effort audit.
			continue
		}
		records, parseErr := gdl.ParseDocument(string(bs))
		if parseErr != nil {
			continue
		}
		for _, r := range records {
			if r.Type != "reason" {
				continue
			}
			steps = append(steps, ReasoningStep{
				ID:       r.Get("id"),
				Author:   r.Get("author"),
				Content:  r.Get("content"),
				Scope:    r.Get("scope"),
				TS:       r.Get("ts"),
				Parent:   r.Get("parent"),
				Decision: r.Get("decision"),
			})
		}
	}
	return sortByChain(steps), nil
}

// TopicVoice is one post-decision contribution on the same subject —
// a @thought (any type) or @observation. Surfaces in the lineage's
// "Topic-adjacent voices:" section so non-Claude / non-structured-
// reasoner agents whose primary cognitive output is `think
// --type=focus|hypothesis` (rather than `reason --decision=`) become
// first-class voices instead of dark matter. (R28 / K1.)
//
// Type is one of "thought" | "observation". ThoughtType carries the
// on-disk @thought `type:` (decision|hypothesis|observation|focus|
// question) so the renderer can label the voice with its cognitive
// mode. Empty for `observation` records (those live under learned/ and
// carry no separate type discriminator).
type TopicVoice struct {
	Type        string // "thought" | "observation"
	ID          string
	Author      string
	Subject     string
	ThoughtType string // @thought.type:, "" for observations
	Content     string
	Object      string // @observation.object, "" for thoughts
	Predicate   string // @observation.predicate, "" for thoughts
	Scope       string // for privacy.IsVisible at the CLI layer
	TS          string
}

// GetScope satisfies privacy.Record so a TopicVoice can flow through
// privacy.IsVisible at the lineage CLI layer (same pattern as
// ReasoningStep).
func (v TopicVoice) GetScope() string { return v.Scope }

// GetAuthor satisfies privacy.Record.
func (v TopicVoice) GetAuthor() string { return v.Author }

// WalkTopicAdjacent scans live/outbox/*/<*.gdl> for @thought records
// AND learned/.../**/*.gdlm for @observation records whose subject
// equals `subject` and whose `ts > decisionTS` (strict — pre-decision
// records are context, not lineage). Returns one TopicVoice per match,
// sorted by ts ascending.
//
// `excludeDecisionID` is the decision's own id — passed so the
// decision's @thought record (which by definition shares its subject)
// is omitted from its own voices section even when the ts comparison
// would otherwise include it (defensive: the strict ts> check already
// excludes it, but an exact-id skip protects against clock-skew
// boundary cases).
//
// The walker is scope-blind: scope IS carried on each TopicVoice so the
// CLI layer can apply privacy.IsVisible uniformly (same pattern as
// WalkReasoning + filterReasoningPrivacy).
//
// Missing directories → (nil, nil). Malformed files are skipped
// silently — best-effort audit per design line 350.
func WalkTopicAdjacent(root, subject, decisionTS, excludeDecisionID string) ([]TopicVoice, error) {
	if subject == "" {
		return nil, nil
	}
	var voices []TopicVoice

	// Outbox @thought records.
	outPattern := filepath.Join(root, "live", "outbox", "*", "*.gdl")
	outMatches, err := filepath.Glob(outPattern)
	if err != nil {
		return nil, err
	}
	for _, p := range outMatches {
		bs, readErr := os.ReadFile(p)
		if readErr != nil {
			continue
		}
		records, parseErr := gdl.ParseDocument(string(bs))
		if parseErr != nil {
			continue
		}
		for _, r := range records {
			if r.Type != "thought" {
				continue
			}
			if r.Get("subject") != subject {
				continue
			}
			id := r.Get("id")
			if excludeDecisionID != "" && id == excludeDecisionID {
				continue
			}
			ts := r.Get("ts")
			if !tsAfter(ts, decisionTS) {
				continue
			}
			voices = append(voices, TopicVoice{
				Type:        "thought",
				ID:          id,
				Author:      r.Get("author"),
				Subject:     subject,
				ThoughtType: r.Get("type"),
				Content:     r.Get("content"),
				Scope:       r.Get("scope"),
				TS:          ts,
			})
		}
	}

	// Learned @observation records. Walk learned/ recursively for
	// *.gdlm — observation.SubjectPath nests one directory per
	// colon-segment so depth is unbounded.
	learnedDir := filepath.Join(root, "learned")
	if _, statErr := os.Stat(learnedDir); statErr == nil {
		walkErr := filepath.WalkDir(learnedDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".gdlm") {
				return nil
			}
			bs, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil
			}
			records, parseErr := gdl.ParseDocument(string(bs))
			if parseErr != nil {
				return nil
			}
			for _, r := range records {
				if r.Type != "observation" {
					continue
				}
				if r.Get("subject") != subject {
					continue
				}
				ts := r.Get("ts")
				if !tsAfter(ts, decisionTS) {
					continue
				}
				voices = append(voices, TopicVoice{
					Type:      "observation",
					ID:        r.Get("id"),
					Author:    r.Get("author"),
					Subject:   subject,
					Content:   r.Get("content"),
					Object:    r.Get("object"),
					Predicate: r.Get("predicate"),
					Scope:     r.Get("scope"),
					TS:        ts,
				})
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
			return nil, walkErr
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, statErr
	}

	sort.SliceStable(voices, func(i, j int) bool { return voices[i].TS < voices[j].TS })
	return voices, nil
}

// tsAfter returns true iff candidate > reference, both as RFC3339Nano
// strings. Empty reference (the decision had no ts) returns true for
// any non-empty candidate so the walker degrades gracefully — a
// missing decision ts treats every same-subject record as "post-
// decision". Empty candidate fails the comparison defensively.
//
// Lexicographic compare on RFC3339Nano matches the sort discipline
// used elsewhere (sortByChain in this file, summon.ReadAll's TS-
// desc sort) because the writer pins UTC and the format is sortable
// as a string when both are UTC.
func tsAfter(candidate, reference string) bool {
	if candidate == "" {
		return false
	}
	if reference == "" {
		return true
	}
	return candidate > reference
}

// sortByChain returns steps in audit order: roots first (sorted by TS
// ascending), then depth-first traversal of each root's children
// (children sorted by TS ascending). Depth is assigned incrementally.
//
// A "root" is a step whose Parent is empty OR whose Parent points to
// an id not present among the loaded steps (the parent may live in
// another agent's reasoning dir, or have been retracted).
func sortByChain(steps []ReasoningStep) []ReasoningStep {
	if len(steps) == 0 {
		return nil
	}
	byID := make(map[string]ReasoningStep, len(steps))
	for _, s := range steps {
		byID[s.ID] = s
	}
	childrenOf := make(map[string][]ReasoningStep, len(steps))
	var roots []ReasoningStep
	for _, s := range steps {
		if s.Parent == "" {
			roots = append(roots, s)
			continue
		}
		if _, ok := byID[s.Parent]; !ok {
			// Parent not in this set — treat as root.
			roots = append(roots, s)
			continue
		}
		childrenOf[s.Parent] = append(childrenOf[s.Parent], s)
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].TS < roots[j].TS })
	for k := range childrenOf {
		kids := childrenOf[k]
		sort.SliceStable(kids, func(i, j int) bool { return kids[i].TS < kids[j].TS })
		childrenOf[k] = kids
	}

	var out []ReasoningStep
	var visit func(s ReasoningStep, depth int)
	visit = func(s ReasoningStep, depth int) {
		s.Depth = depth
		out = append(out, s)
		for _, child := range childrenOf[s.ID] {
			visit(child, depth+1)
		}
	}
	for _, r := range roots {
		visit(r, 0)
	}
	return out
}
