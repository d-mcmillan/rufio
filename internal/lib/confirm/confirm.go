// Package confirm implements `rufio confirm` and `rufio refute` (write
// side) and feeds the AutoPromoteHandler engine (PR #13 Task 6).
//
// Records are appended to a shared file live/confirms/<thought-id>.gdl
// via O_APPEND. POSIX guarantees atomic appends for writes < PIPE_BUF
// (4KB on macOS/Linux). Each @confirm/@refute record is well under 4KB,
// so no lock domain is needed.
package confirm

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// Tally is the deduplicated count of confirmers and refuters for a
// single thought-id. Used by the AutoPromote engine.
type Tally struct {
	Confirms []string // sorted, deduplicated agent ids
	Refutes  []string // sorted, deduplicated agent ids
}

// Record is one rendered @confirm or @refute as surfaced to read
// callers (lineage, thoughts list). Kind is "confirm" or "refute".
// Evidence carries the optional --evidence flag value; Reason is
// populated only for refutes (the required reason field on @refute).
//
// Unlike Tally, Records preserves every individual record — no
// dedup, no agent collapse — because display callers want to render
// each social-validation event distinctly with its ts + evidence.
type Record struct {
	Kind     string // "confirm" or "refute"
	Agent    string
	TS       string
	Reason   string // refute only; empty for confirm
	Evidence string // optional
}

// Confidence returns confirms / (confirms + refutes). Returns 1.0 when
// there are confirms but no refutes; 0.0 when there are neither.
func (t Tally) Confidence() float64 {
	c := len(t.Confirms)
	r := len(t.Refutes)
	if c+r == 0 {
		return 0
	}
	return float64(c) / float64(c+r)
}

// BuildConfirm returns the @confirm gdl.Record. evidence is omitted when empty.
func BuildConfirm(target, by, evidence, ts string) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "target", Value: target},
		{Key: "by", Value: by},
	}
	if evidence != "" {
		fields = append(fields, gdl.RecordField{Key: "evidence", Value: evidence})
	}
	fields = append(fields, gdl.RecordField{Key: "ts", Value: ts})
	return gdl.Record{Type: "confirm", Fields: fields}
}

// BuildRefute returns the @refute gdl.Record. reason is required;
// evidence is omitted when empty.
func BuildRefute(target, by, reason, evidence, ts string) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "target", Value: target},
		{Key: "by", Value: by},
		{Key: "reason", Value: reason},
	}
	if evidence != "" {
		fields = append(fields, gdl.RecordField{Key: "evidence", Value: evidence})
	}
	fields = append(fields, gdl.RecordField{Key: "ts", Value: ts})
	return gdl.Record{Type: "refute", Fields: fields}
}

// Append opens live/confirms/<targetID>.gdl with O_APPEND|O_CREATE|O_WRONLY
// (0644) and writes the rendered record line. Atomic per POSIX for
// writes < PIPE_BUF (4KB). No lock domain.
func Append(root, targetID string, record gdl.Record) error {
	dir := filepath.Join(root, "live", "confirms")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, targetID+".gdl")
	f, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(gdl.RenderLine(record) + "\n")
	return err
}

// ReadRecords parses live/confirms/<targetID>.gdl and returns every
// @confirm and @refute record as a Record slice in file order (which,
// because Append is O_APPEND-only, is also chronological). Missing
// file → empty slice + nil err. Malformed records or those missing
// `by` are skipped silently — matches the best-effort posture of
// ReadAll which uses the same source file.
//
// Used by `rufio lineage` and `rufio thoughts list --json` to surface
// social-validation events with their ts + evidence. ReadAll continues
// to drive the AutoPromote engine (dedup'd agent ids); the two helpers
// share a parse path but diverge in projection.
func ReadRecords(root, targetID string) ([]Record, error) {
	path := filepath.Join(root, "live", "confirms", targetID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(records))
	for _, r := range records {
		by := r.Get("by")
		if by == "" {
			continue
		}
		switch r.Type {
		case "confirm":
			out = append(out, Record{
				Kind:     "confirm",
				Agent:    by,
				TS:       r.Get("ts"),
				Evidence: r.Get("evidence"),
			})
		case "refute":
			out = append(out, Record{
				Kind:     "refute",
				Agent:    by,
				TS:       r.Get("ts"),
				Reason:   r.Get("reason"),
				Evidence: r.Get("evidence"),
			})
		}
	}
	return out, nil
}

// ReadAll parses the confirms file and returns a Tally with confirmers
// and refuters deduplicated and sorted. Missing file → empty Tally
// (not an error).
func ReadAll(root, targetID string) (Tally, error) {
	path := filepath.Join(root, "live", "confirms", targetID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Tally{}, nil
		}
		return Tally{}, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return Tally{}, err
	}
	confirmSet := make(map[string]bool)
	refuteSet := make(map[string]bool)
	for _, r := range records {
		by := r.Get("by")
		if by == "" {
			continue
		}
		switch r.Type {
		case "confirm":
			confirmSet[by] = true
		case "refute":
			refuteSet[by] = true
		}
	}
	t := Tally{}
	for a := range confirmSet {
		t.Confirms = append(t.Confirms, a)
	}
	for a := range refuteSet {
		t.Refutes = append(t.Refutes, a)
	}
	sort.Strings(t.Confirms)
	sort.Strings(t.Refutes)
	return t, nil
}
