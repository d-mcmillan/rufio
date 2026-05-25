// Package format provides interop utilities for moving substrate data
// in and out of non-GDL pipelines. JSONL is the canonical interop format
// — one JSON object per line, byte-identical to what `rufio recall --json`
// already emits per record.
//
// IMPORTANT: JSONL is import/export ONLY. It is NEVER added as a storage
// format on disk. The GDL-on-disk manifesto stays — JSONL is sugar for
// pipelines that don't speak GDL.
package format

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// EncodeJSONL writes records as JSONL to w. Each record becomes one
// JSON object on its own line. Order is preserved from the input slice.
func EncodeJSONL(w io.Writer, records []map[string]interface{}) error {
	enc := json.NewEncoder(w)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// DecodeJSONL reads JSONL from r, one record per line. Returns the
// records and the count of malformed lines (with their content
// truncated to 80 chars for safe logging) so the caller can report
// per-line errors without abandoning the import.
func DecodeJSONL(r io.Reader) ([]map[string]interface{}, []LineError, error) {
	var records []map[string]interface{}
	var errs []LineError
	scanner := bufio.NewScanner(r)
	// 1 MiB per line — substrate records routinely include multi-line
	// content (after escaping), and the default 64 KiB max is too
	// tight.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal(line, &rec); err != nil {
			errs = append(errs, LineError{
				Line:    lineNum,
				Message: err.Error(),
				Preview: truncate(string(line), 80),
			})
			continue
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return records, errs, err
	}
	if len(records) == 0 && len(errs) == 0 {
		return nil, nil, errors.New("no records in input")
	}
	return records, errs, nil
}

// LineError captures a single parse failure during DecodeJSONL.
type LineError struct {
	Line    int
	Message string
	Preview string
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
