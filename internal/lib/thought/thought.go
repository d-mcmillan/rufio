// Package thought holds the validation, ID-generation, record-build, and
// write helpers for `rufio think` (write side, this PR) and the read-side
// consumers `recall` (PR #9) and `lineage` (PR #20).
//
// Write-side contract from design §2.B:
//
//	ResolveIdentity → BuildRecord → WriteAtomic(.tmp + rename) → EmitConfirmation
//
// No lock domain: each thought has a unique <unix-millis>-<rand6> filename
// per design lock D5.9 (no concurrent-write target collision).
//
// Decision-type thoughts (`--type=decision`) additionally include a
// sibling @context-bundle record IN THE SAME FILE so the dual-record
// write remains a single atomic `.tmp + os.Rename` operation (L2.9).
package thought

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// Locked enums per design §2.B and v1-spec line 135-136.
var (
	allowedTypes  = []string{"hypothesis", "observation", "decision", "question", "focus"}
	allowedScopes = []string{"agent", "deployment", "fleet"}

	subjectRegex = regexp.MustCompile(`^[a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+$`)
	parentRegex  = regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)
)

// ValidateType returns *InvalidTypeError when v is not in the allowed enum.
func ValidateType(v string) error {
	for _, a := range allowedTypes {
		if v == a {
			return nil
		}
	}
	return &rufioerr.InvalidTypeError{Value: v, Allowed: allowedTypes}
}

// ValidateSubject returns *InvalidSubjectError when subject is empty or
// fails the entity-id regex.
func ValidateSubject(subject string) error {
	if subject == "" {
		return &rufioerr.InvalidSubjectError{}
	}
	if !subjectRegex.MatchString(subject) {
		return &rufioerr.InvalidSubjectError{Subject: subject}
	}
	return nil
}

// ValidateScope returns *InvalidScopeError when scope is not in the
// allowed enum.
func ValidateScope(scope string) error {
	for _, a := range allowedScopes {
		if scope == a {
			return nil
		}
	}
	return &rufioerr.InvalidScopeError{Value: scope, Allowed: allowedScopes}
}

// ValidateContent returns *InvalidContentError{Field:"content"} when the
// trimmed value is empty.
func ValidateContent(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return &rufioerr.InvalidContentError{Field: "content"}
	}
	return nil
}

// ParseTTL converts the --ttl flag value to an integer seconds count.
// Returns (0, nil) when raw is empty (D5.1 — default is "never expires").
// Returns *InvalidTTLError on non-integer input, leading/trailing
// whitespace, or non-positive value.
func ParseTTL(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	// strconv.Atoi rejects leading/trailing whitespace and non-integer
	// formats — those are exactly the cases InvalidTTLError covers.
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, &rufioerr.InvalidTTLError{Raw: raw}
	}
	if n <= 0 {
		return 0, &rufioerr.InvalidTTLError{Raw: raw}
	}
	return n, nil
}

// ValidateParent returns nil when the parent flag is omitted (empty
// string) and *InvalidParentError when present but malformed.
//
// Parent IS a thought-id of the form <unix-millis>-<rand6>; v1 validates
// FORMAT only, not existence (the parent thought may live in another
// agent's outbox, be expired, or be retracted).
func ValidateParent(parent string) error {
	if parent == "" {
		return nil
	}
	if !parentRegex.MatchString(parent) {
		return &rufioerr.InvalidParentError{ID: parent}
	}
	return nil
}

// GenerateID returns a new thought-id of the form <unix-millis>-<rand6>
// where rand6 is six lowercase alphanumeric characters drawn from
// crypto/rand. Per design §2.B "ID format" + D5.10.
func GenerateID() (string, error) {
	return generateIDFromSource(
		func() int64 { return time.Now().UnixMilli() },
		rand.Reader,
	)
}

// generateIDFromSource is the testable variant: callers supply the clock
// and the random source. Production callers use GenerateID. Tests pin a
// known output by passing a fixed io.Reader + deterministic now.
func generateIDFromSource(now func() int64, src io.Reader) (string, error) {
	buf := make([]byte, 6)
	n, err := io.ReadFull(src, buf)
	if err != nil || n != 6 {
		return "", fmt.Errorf("thought: rand source read %d/6 bytes: %w", n, err)
	}
	// Map raw bytes to [a-z0-9] (36 chars). Use modulo since uniform-
	// distribution isn't load-bearing for a 36^6 ≈ 2.2e9 space.
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 6)
	for i, b := range buf {
		out[i] = alphabet[int(b)%36]
	}
	return fmt.Sprintf("%d-%s", now(), out), nil
}

// ThoughtInput is the value type that BuildThoughtRecord projects to a
// gdl.Record. All fields are caller-supplied; validation happens upstream.
type ThoughtInput struct {
	ID      string
	Author  string
	Type    string
	Subject string
	Content string
	Scope   string
	Topics  []string // optional; omitted when nil/empty
	TS      string
	TTL     int    // 0 = never expire (D5.1); always rendered
	Parent  string // optional; omitted when empty
}

// BuildThoughtRecord returns the @thought gdl.Record. Field order is
// locked at id, author, type, subject, content, scope, topics?, ts, ttl,
// parent? (per design §2.B + D5.1).
//
// Inputs are assumed pre-validated by ValidateType, ValidateSubject,
// ValidateScope, ValidateContent, ValidateParent.
func BuildThoughtRecord(in ThoughtInput) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "id", Value: in.ID},
		{Key: "author", Value: in.Author},
		{Key: "type", Value: in.Type},
		{Key: "subject", Value: in.Subject},
		{Key: "content", Value: in.Content},
		{Key: "scope", Value: in.Scope},
	}
	if len(in.Topics) > 0 {
		fields = append(fields, gdl.RecordField{Key: "topics", Value: strings.Join(in.Topics, ",")})
	}
	fields = append(fields, gdl.RecordField{Key: "ts", Value: in.TS})
	fields = append(fields, gdl.RecordField{Key: "ttl", Value: strconv.Itoa(in.TTL)})
	if in.Parent != "" {
		fields = append(fields, gdl.RecordField{Key: "parent", Value: in.Parent})
	}
	return gdl.Record{Type: "thought", Fields: fields}
}

// BuildContextBundle returns the @context-bundle gdl.Record. Used only
// for --type=decision thoughts (D5.7). The bundle lives in the SAME file
// as the @thought record so the two writes are atomic via a single
// .tmp + os.Rename (L2.9).
func BuildContextBundle(decisionID string, refs []string) gdl.Record {
	return gdl.Record{Type: "context-bundle", Fields: []gdl.RecordField{
		{Key: "decision", Value: decisionID},
		{Key: "refs", Value: strings.Join(refs, ",")},
	}}
}

// CollectGivenLearnedSHAs returns the SHA256 of the latest-live ref for
// every content path under given/ and learned/. Used by --type=decision
// to build the @context-bundle that pins the corpus the decision was
// based on (D5.8).
//
// Walks <root>/.rufio/refs/given/ and <root>/.rufio/refs/learned/
// recursively; missing top-level directories are NOT an error (return
// empty slice). Content paths with refs only in draft/staged are skipped
// — decision context tracks the *live* corpus (matching recall's default
// view).
//
// Returns SHAs in deterministic sorted-content-path order.
func CollectGivenLearnedSHAs(root string) ([]string, error) {
	var contentPaths []string
	for _, sub := range []string{"given", "learned"} {
		refsDir := filepath.Join(root, ".rufio", "refs", sub)
		if _, err := os.Stat(refsDir); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		err := filepath.WalkDir(refsDir, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(p, ".gdl") {
				return nil
			}
			// Convert refs path back to content path:
			//   <root>/.rufio/refs/given/x.md.gdl → given/x.md
			rel, err := filepath.Rel(filepath.Join(root, ".rufio", "refs"), p)
			if err != nil {
				return err
			}
			contentPath := strings.TrimSuffix(filepath.ToSlash(rel), ".gdl")
			contentPaths = append(contentPaths, contentPath)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(contentPaths)

	var shas []string
	for _, cp := range contentPaths {
		refs, err := versioning.ReadRefs(root, cp)
		if err != nil {
			return nil, err
		}
		latest := versioning.LatestRefByStage(refs, versioning.StageLive)
		if latest == nil {
			continue
		}
		shas = append(shas, latest.SHA256)
	}
	return shas, nil
}

// Write atomically writes records to live/outbox/<agent>/<id>.gdl. For
// non-decision thoughts, records is [thought]; for --type=decision it's
// [thought, context-bundle] — both records land in the same file via a
// single .tmp + os.Rename (D5.7, lock L2.9).
//
// No lock domain (D5.9): id is unique per call via GenerateID, so two
// concurrent Write invocations write to distinct targets. POSIX rename
// is atomic, so observers see prior-or-new content per file.
func Write(root, agent, id string, records []gdl.Record) error {
	dir := filepath.Join(root, "live", "outbox", agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, id+".gdl")
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <id>.gdl.tmp under live/outbox/ (#141). Success path: Rename
	// already moved tmp, so this is a no-op. Ordering unchanged — the
	// single .tmp + os.Rename remains the sole atomicity mechanism (L2.9).
	defer func() { _ = os.Remove(tmp) }()

	var buf strings.Builder
	for _, r := range records {
		buf.WriteString(gdl.RenderLine(r))
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
