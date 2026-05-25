package format

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeJSONL_OneRecordPerLine(t *testing.T) {
	records := []map[string]interface{}{
		{"_type": "thought", "id": "1", "content": "first"},
		{"_type": "thought", "id": "2", "content": "second"},
		{"_type": "thought", "id": "3", "content": "third"},
	}
	var buf bytes.Buffer
	if err := EncodeJSONL(&buf, records); err != nil {
		t.Fatalf("EncodeJSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestEncodeJSONL_EachLineIsValidJSON(t *testing.T) {
	records := []map[string]interface{}{
		{"_type": "thought", "id": "1", "content": "alpha"},
	}
	var buf bytes.Buffer
	if err := EncodeJSONL(&buf, records); err != nil {
		t.Fatalf("EncodeJSONL: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("each line should be newline-terminated")
	}
}

func TestDecodeJSONL_Roundtrip(t *testing.T) {
	original := []map[string]interface{}{
		{"_type": "thought", "id": "1", "content": "first"},
		{"_type": "observation", "id": "2", "subject": "test:1"},
	}
	var buf bytes.Buffer
	_ = EncodeJSONL(&buf, original)
	parsed, errs, err := DecodeJSONL(&buf)
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("unexpected line errors: %+v", errs)
	}
	if len(parsed) != len(original) {
		t.Errorf("roundtrip lost records: got %d, want %d", len(parsed), len(original))
	}
}

func TestDecodeJSONL_ReportsMalformedLines(t *testing.T) {
	input := `{"_type":"thought","id":"1"}
not valid json
{"_type":"thought","id":"2"}
`
	parsed, errs, err := DecodeJSONL(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	if len(parsed) != 2 {
		t.Errorf("expected 2 parseable records, got %d", len(parsed))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 line error, got %d", len(errs))
	}
	if errs[0].Line != 2 {
		t.Errorf("error should report line 2, got %d", errs[0].Line)
	}
}

func TestDecodeJSONL_EmptyLinesSkipped(t *testing.T) {
	input := `
{"_type":"thought","id":"1"}

{"_type":"thought","id":"2"}

`
	parsed, _, err := DecodeJSONL(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	if len(parsed) != 2 {
		t.Errorf("expected 2 records, got %d", len(parsed))
	}
}

func TestDecodeJSONL_EmptyInput(t *testing.T) {
	_, _, err := DecodeJSONL(strings.NewReader(""))
	if err == nil {
		t.Error("expected error for empty input")
	}
}
