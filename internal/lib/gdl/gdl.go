// Package gdl is the Greppable parser/writer Rufio uses for its internal
// records (rufio.gdl, ref files, future attention/summons/channels/goals).
//
// Mirrors src/lib/gdl.ts. The format:
//
//	@type|key:value|key:value
//
// Backslash-escaping for `\`, `|`, `:`, `\n`, `\r` in values. Field-insertion
// order is preserved (Go maps don't preserve order, so we use a slice of
// pairs).
//
// Newline/CR escaping was added in G/#R28: GDL is a LINE-oriented format
// (one @record per text line), so raw `\n`/`\r` inside a value would split
// a single record across multiple lines and the next read would error at
// the embedded line with `malformed GDL line (must start with @)`. The
// write path escapes `\n`→`\n` (literal backslash-n) and `\r`→`\r`; the
// read path symmetrically un-escapes them so round-trip is byte-identity.
package gdl

import (
	"fmt"
	"strings"
)

// RecordField is a single key:value pair within a record. Using a struct
// instead of a map preserves field-insertion order, which matters for
// grep-friendliness of output (e.g., we want `version:` to appear in a
// consistent column across @ref records).
type RecordField struct {
	Key   string
	Value string
}

// Record is a single GDL line: @type|f1|f2|...
type Record struct {
	Type   string
	Fields []RecordField
}

// Get returns the value of the named field, or "" if not present.
func (r Record) Get(key string) string {
	for _, f := range r.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

// EscapeValue escapes `\`, `|`, `:`, `\n`, `\r` so a value can be safely
// embedded in a key:value field on a single GDL line.
//
// Backslash and the two field delimiters (`|`, `:`) are escaped because
// they're structurally meaningful to the line grammar.
//
// Newline and carriage-return are escaped because the format is
// line-oriented: a raw `\n` in a value would split one @record across
// two text lines and the next read would error at the embedded line
// (G/#R28). Encoding: `\n`→`\n` (two characters: backslash, lowercase n),
// `\r`→`\r`. UnescapeValue inverts this symmetrically.
func EscapeValue(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, ch := range value {
		switch ch {
		case '\\':
			b.WriteString(`\\`)
		case '|':
			b.WriteString(`\|`)
		case ':':
			b.WriteString(`\:`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// UnescapeValue is the inverse of EscapeValue.
//
// Specifically decodes `\n`→`\n` (newline) and `\r`→`\r` (CR) so
// multi-line field values round-trip byte-identically (G/#R28).
//
// For any OTHER backslash-prefixed character — `\\`, `\|`, `\:`, and the
// long-tail of "unknown" escapes — we preserve the original TS-week-1
// behaviour of stripping the backslash before the next byte. This keeps
// hand-written legacy GDL (e.g. a `\x` someone typed into a ref file)
// parsing the same way it always has, and means values written by the
// pre-G/#R28 writer (which only emitted `\\`, `\|`, `\:`) still parse
// correctly through the new reader. Backward compatibility is the
// non-negotiable on this layer.
func UnescapeValue(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch == '\\' && i+1 < len(value) {
			next := value[i+1]
			switch next {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			default:
				// TS-week-1 strip-backslash-before-next-byte behaviour.
				// Covers `\\`→`\`, `\|`→`|`, `\:`→`:`, and any other
				// backslash sequence written by either the current or
				// the pre-G/#R28 writer (back-compat gate).
				b.WriteByte(next)
			}
			i++
		} else {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// splitEscaped splits line on delim, respecting backslash-escaping.
func splitEscaped(line, delim string) []string {
	if len(delim) != 1 {
		// Conservative — only single-char delimiters supported.
		return []string{line}
	}
	d := delim[0]
	var out []string
	var buf strings.Builder
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '\\' && i+1 < len(line) {
			buf.WriteByte(line[i])
			buf.WriteByte(line[i+1])
			i++
		} else if ch == d {
			out = append(out, buf.String())
			buf.Reset()
		} else {
			buf.WriteByte(ch)
		}
	}
	out = append(out, buf.String())
	return out
}

// ParseLine parses a single GDL line. Returns (nil, nil) for blank lines
// and comments. Returns an error for malformed lines.
func ParseLine(line string) (*Record, error) {
	trimmed := strings.TrimRight(line, " \t\r\n")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, "@") {
		return nil, fmt.Errorf("malformed GDL line (must start with @): %s", line)
	}
	parts := splitEscaped(trimmed, "|")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("malformed GDL line (empty head): %s", line)
	}
	r := &Record{Type: strings.TrimPrefix(parts[0], "@")}
	for i := 1; i < len(parts); i++ {
		kv := splitEscaped(parts[i], ":")
		if len(kv) < 2 {
			return nil, fmt.Errorf("malformed field (no colon): %s", parts[i])
		}
		key := kv[0]
		value := strings.Join(kv[1:], ":")
		r.Fields = append(r.Fields, RecordField{Key: key, Value: UnescapeValue(value)})
	}
	return r, nil
}

// ParseDocument parses a multi-line GDL document. Blank lines and
// comments are skipped. Returns the first parse error encountered.
func ParseDocument(text string) ([]Record, error) {
	var out []Record
	for _, line := range strings.Split(text, "\n") {
		r, err := ParseLine(line)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

// RenderLine renders a record as a single GDL line (no trailing newline).
func RenderLine(record Record) string {
	var b strings.Builder
	b.WriteString("@")
	b.WriteString(record.Type)
	for _, f := range record.Fields {
		b.WriteString("|")
		b.WriteString(f.Key)
		b.WriteString(":")
		b.WriteString(EscapeValue(f.Value))
	}
	return b.String()
}

// AppendRecord appends record to existing, returning the rendered line and
// the resulting full document text. Handles trailing-newline semantics so
// the document always ends with a newline after the appended record.
func AppendRecord(existing string, record Record) (line string, result string) {
	line = RenderLine(record)
	sep := ""
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		sep = "\n"
	}
	return line, existing + sep + line + "\n"
}
