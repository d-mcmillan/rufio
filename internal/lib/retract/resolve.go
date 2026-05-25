package retract

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
)

// canonicalIDRegex matches the full <unix-millis>-<rand6> shape. Same
// alphabet as thought.parentRegex / reason.decisionRegex — duplicated
// here rather than imported to keep the resolver standalone (and to
// avoid a circular import; thought already imports retract via the
// confirm/refute path).
var canonicalIDRegex = regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)

// shortIDRegex matches a bare 6-char [a-z0-9] suffix — what output.ShortID
// produces and what `thoughts list` / `recall` render in text mode. The
// resolver treats this shape as "do a suffix lookup".
var shortIDRegex = regexp.MustCompile(`^[a-z0-9]{6}$`)

// LooksLikeThoughtID returns true when input matches the canonical
// <unix-millis>-<rand6> shape. Used by promote (R29b) to detect a
// caller passing a thought-id where an artifact path was expected.
func LooksLikeThoughtID(s string) bool {
	return canonicalIDRegex.MatchString(s)
}

// LooksLikeShortID returns true when input matches the 6-char suffix
// shape `[a-z0-9]{6}` — the text-mode short form. Production callers
// don't usually need this — Resolve handles the dispatch — but promote
// (R29b) reaches for it directly to decide whether to attempt a
// thought-id resolution at all.
func LooksLikeShortID(s string) bool {
	return shortIDRegex.MatchString(s)
}

// Resolve takes a value that may be EITHER a canonical full
// <unix-millis>-<rand6> id OR a 6-char [a-z0-9]{6} suffix that
// `thoughts list` / `recall` render in text mode, and returns the
// canonical full id of the on-disk record.
//
//   - Canonical full id → pass through unchanged after a cheap shape
//     check; no I/O. (Skips the suffix scan even when the suffix
//     happens to be ambiguous in the corpus.)
//   - 6-char suffix → glob live/outbox/*/*-<suffix>.gdl + walk
//     learned/**/*-<suffix>.gdlm. After candidate collection, filter by
//     the privacy floor (#147): scope:agent records authored by a
//     different agent are excluded from the candidate set so existence
//     isn't leaked through disambiguation. Exactly one survivor →
//     canonical id. Multiple → *AmbiguousIDError listing each with
//     author + type + subject. Zero → *NoSuchThoughtError preserving
//     the original input for the user-visible message.
//   - Anything else → passes through to the legacy Lookup path so
//     non-matching shapes (legacy ids, malformed input) get the same
//     NoSuchThoughtError they got pre-R29.
//
// currentAgent is the resolved identity ("" for anonymous; the resolver
// applies the same firehose semantics as privacy.IsVisible). Callers
// that have an agent should pass it so the privacy gate filters out
// other-author scope:agent candidates. The empty case is preserved for
// admin/test paths.
func Resolve(root, idOrSuffix, currentAgent string) (string, error) {
	// Pass-through: full canonical id. No need to disambiguate even if
	// the corpus happens to contain another id with the same suffix —
	// the caller already supplied the precise form.
	if canonicalIDRegex.MatchString(idOrSuffix) {
		return idOrSuffix, nil
	}
	// Non-suffix shape → pass through verbatim. The CALLER's lookup
	// (Lookup / LookupTarget / LookupDecision / ValidateDecision shape)
	// will surface the appropriate typed error. Resolve must NOT change
	// the error surface for garbage input — that would mask shape
	// errors (exit 2) as not-found errors (exit 1).
	if !shortIDRegex.MatchString(idOrSuffix) {
		return idOrSuffix, nil
	}

	suffix := idOrSuffix
	candidates, err := collectSuffixCandidates(root, suffix)
	if err != nil {
		return "", err
	}
	// Privacy floor: drop other-author scope:agent records BEFORE the
	// uniqueness check, so a non-author can't probe existence by hitting
	// an ambiguous-vs-not boundary. Matches privacy.IsVisible.
	candidates = filterPrivacyCandidates(candidates, currentAgent)

	switch len(candidates) {
	case 0:
		return "", &rufioerr.NoSuchThoughtError{ID: idOrSuffix}
	case 1:
		return candidates[0].ID, nil
	default:
		// Surface every candidate with disambiguation context. Sort by
		// id for stable error rendering — filesystem walk order is
		// non-deterministic across platforms.
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		rows := make([]rufioerr.AmbiguousCandidate, 0, len(candidates))
		for _, c := range candidates {
			rows = append(rows, rufioerr.AmbiguousCandidate{
				ID:      c.ID,
				Author:  c.Author,
				Type:    c.Type,
				Subject: c.Subject,
			})
		}
		return "", &rufioerr.AmbiguousIDError{Short: suffix, Candidates: rows}
	}
}

// suffixCandidate is the internal collection row for the suffix scan.
// It carries the privacy fields too so filterPrivacyCandidates can run
// without re-reading the files.
type suffixCandidate struct {
	ID      string
	Author  string
	Type    string // thought type (decision|hypothesis|focus|observation|…)
	Subject string
	Scope   string // scope:agent|deployment|fleet — used by the privacy filter
}

// GetAuthor satisfies privacy.Record so suffixCandidate can flow
// through privacy.IsVisible. The scope/author pair is the only thing
// the predicate inspects.
func (c suffixCandidate) GetAuthor() string { return c.Author }

// GetScope satisfies privacy.Record. See GetAuthor.
func (c suffixCandidate) GetScope() string { return c.Scope }

// collectSuffixCandidates walks both lookup roots — live/outbox/*/ for
// thoughts and learned/**/*.gdlm for observations — for filenames whose
// stem ends in "-<suffix>". Each hit is parsed once for author/type/
// subject/scope so the disambiguation render + privacy filter can run
// downstream without a second read pass.
func collectSuffixCandidates(root, suffix string) ([]suffixCandidate, error) {
	var out []suffixCandidate

	// Outbox: live/outbox/<author>/<unix-millis>-<suffix>.gdl. One glob
	// per agent dir; cheap.
	outboxPattern := filepath.Join(root, "live", "outbox", "*", "*-"+suffix+".gdl")
	matches, err := filepath.Glob(outboxPattern)
	if err != nil {
		return nil, err
	}
	for _, p := range matches {
		base := filepath.Base(p)
		id := strings.TrimSuffix(base, ".gdl")
		// Defensive: ensure the suffix sits on the rand6 portion, not in
		// the middle of the unix-millis (impossible by alphabet but the
		// regex check costs nothing).
		if !canonicalIDRegex.MatchString(id) {
			continue
		}
		cand := suffixCandidate{
			ID:     id,
			Author: filepath.Base(filepath.Dir(p)),
		}
		if t, sub, sc, ok := parseThoughtMeta(p); ok {
			cand.Type, cand.Subject, cand.Scope = t, sub, sc
		}
		out = append(out, cand)
	}

	// Learned: learned/**/<unix-millis>-<suffix>.gdlm. observation.SubjectPath
	// nests by colon-segments so the file can sit at any depth. WalkDir
	// is the same shape findLearnedRecord uses.
	learnedDir := filepath.Join(root, "learned")
	if _, statErr := os.Stat(learnedDir); statErr == nil {
		walkErr := filepath.WalkDir(learnedDir, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".gdlm") {
				return nil
			}
			id := strings.TrimSuffix(name, ".gdlm")
			if !canonicalIDRegex.MatchString(id) {
				return nil
			}
			if !strings.HasSuffix(id, "-"+suffix) {
				return nil
			}
			cand := suffixCandidate{ID: id, Type: "observation"}
			if author, sub, sc, ok := parseObservationMeta(p); ok {
				cand.Author, cand.Subject, cand.Scope = author, sub, sc
			}
			out = append(out, cand)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}

	return out, nil
}

// parseThoughtMeta reads an outbox .gdl file once and returns the
// (type, subject, scope) of the first @thought record. Parse errors
// degrade to ok=false — the caller still keeps the candidate (the
// filename alone proves existence) but with empty meta. Empty type
// downstream renders as "thought" rather than a specific subtype, which
// is acceptable for the ambiguous-disambiguation line.
func parseThoughtMeta(path string) (typ, subject, scope string, ok bool) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", false
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return "", "", "", false
	}
	for _, r := range records {
		if r.Type != "thought" {
			continue
		}
		return r.Get("type"), r.Get("subject"), r.Get("scope"), true
	}
	return "", "", "", false
}

// parseObservationMeta is the learned/-side counterpart of
// parseThoughtMeta. Pulls author/subject/scope off the first
// @observation record. Author is the on-disk `author:` field, not the
// directory name (which is the subject) — see findLearnedRecord.
func parseObservationMeta(path string) (author, subject, scope string, ok bool) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", false
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return "", "", "", false
	}
	for _, r := range records {
		if r.Type != "observation" {
			continue
		}
		return r.Get("author"), r.Get("subject"), r.Get("scope"), true
	}
	return "", "", "", false
}

// filterPrivacyCandidates applies the #147 privacy floor before the
// uniqueness check. currentAgent="" preserves every candidate (firehose
// — admin/test paths). Otherwise: other-author scope:agent records are
// dropped (existence-leak prevention). Author's own scope:agent records
// always pass.
func filterPrivacyCandidates(cands []suffixCandidate, currentAgent string) []suffixCandidate {
	if currentAgent == "" {
		return cands
	}
	out := make([]suffixCandidate, 0, len(cands))
	for _, c := range cands {
		if !privacy.IsVisible(c, currentAgent) {
			continue
		}
		out = append(out, c)
	}
	return out
}
