package diff

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestIsBinary_AsciiFalse(t *testing.T) {
	if IsBinary([]byte("hello world\nplain text\n")) {
		t.Error("expected false for ASCII")
	}
}

func TestIsBinary_NulInFirst8KBTrue(t *testing.T) {
	buf := []byte{0x68, 0x69, 0x00, 0x6f}
	if !IsBinary(buf) {
		t.Error("expected true for buffer with NUL")
	}
}

func TestIsBinary_NulPast8KBFalse(t *testing.T) {
	buf := bytes.Repeat([]byte{0x61}, 8200)
	buf[8195] = 0x00
	if IsBinary(buf) {
		t.Error("expected false for NUL past 8KB sample")
	}
}

func TestIsBinary_EmptyFalse(t *testing.T) {
	if IsBinary(nil) {
		t.Error("expected false for empty buffer")
	}
}

func TestIsBinary_PNGHeaderTrue(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00}
	if !IsBinary(png) {
		t.Error("expected true for PNG header (NUL in IHDR)")
	}
}

func TestUnifiedDiff_IdenticalEmpty(t *testing.T) {
	got, err := UnifiedDiff("a\nb\nc", "a\nb\nc", "x", "y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestUnifiedDiff_SingleLineChangeEmitsHunk(t *testing.T) {
	out, err := UnifiedDiff("hello\n", "world\n", "a", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustMatch(t, out, `^--- a\n`)
	mustMatch(t, out, `(?m)^\+\+\+ b`)
	mustMatch(t, out, `@@ `)
	mustMatch(t, out, `(?m)^-hello`)
	mustMatch(t, out, `(?m)^\+world`)
}

func TestUnifiedDiff_InsertionOnly(t *testing.T) {
	a := "alpha\nbravo\ncharlie\n"
	b := "alpha\nbravo\nNEW\ncharlie\n"
	out, err := UnifiedDiff(a, b, "old", "new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustMatch(t, out, `(?m)^\+NEW$`)
	mustNotMatch(t, out, `(?m)^-[A-Za-z]`)
}

func TestUnifiedDiff_DeletionOnly(t *testing.T) {
	a := "alpha\nbravo\ncharlie\ndelta\n"
	b := "alpha\ncharlie\ndelta\n"
	out, err := UnifiedDiff(a, b, "old", "new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustMatch(t, out, `(?m)^-bravo$`)
	mustNotMatch(t, out, `(?m)^\+[A-Za-z]`)
}

func TestUnifiedDiff_MultiHunkSeparation(t *testing.T) {
	a := strings.Join([]string{
		"01", "02", "03", "04", "05", "06", "07", "08", "09", "10",
		"11", "12", "13", "14", "15", "16", "17", "18", "19", "20",
	}, "\n")
	b := strings.Join([]string{
		"01", "02", "03", "X4", "05", "06", "07", "08", "09", "10",
		"11", "12", "13", "14", "15", "16", "Y7", "18", "19", "20",
	}, "\n")
	out, err := UnifiedDiff(a, b, "a", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	matches := regexp.MustCompile(`(?m)^@@ `).FindAllString(out, -1)
	if len(matches) != 2 {
		t.Errorf("got %d hunk headers, want 2; output:\n%s", len(matches), out)
	}
}

func TestUnifiedDiff_HunkHeaderLineNumbers(t *testing.T) {
	a := "line1\nline3\nline4\n"
	b := "line1\nline2\nline3\nline4\n"
	out, err := UnifiedDiff(a, b, "a", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustMatch(t, out, `@@ -1,4 \+1,5 @@`)
	mustMatch(t, out, `(?m)^\+line2$`)
}

func TestUnifiedDiff_HunkMergesAdjacent(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n"
	b := "X\n2\n3\n4\n5\nY\n"
	out, err := UnifiedDiff(a, b, "a", "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	matches := regexp.MustCompile(`(?m)^@@ `).FindAllString(out, -1)
	if len(matches) != 1 {
		t.Errorf("got %d hunk headers, want 1 (merge); output:\n%s", len(matches), out)
	}
}

func TestUnifiedDiff_ThrowsAboveMaxDiffLines(t *testing.T) {
	bigLines := make([]string, MaxDiffLines+1)
	for i := range bigLines {
		bigLines[i] = "line"
	}
	big := strings.Join(bigLines, "\n")

	_, err := UnifiedDiff("small\n", big, "a", "b")
	if err == nil {
		t.Fatal("expected error for big right side")
	}
	mustMatch(t, err.Error(), `(?i)diff input exceeds.*line limit`)

	_, err = UnifiedDiff(big, "small\n", "a", "b")
	if err == nil {
		t.Fatal("expected error for big left side")
	}
}

func TestUnifiedDiff_AcceptsAtMaxDiffLines(t *testing.T) {
	lines := make([]string, MaxDiffLines)
	for i := range lines {
		lines[i] = "line"
	}
	same := strings.Join(lines, "\n")
	if _, err := UnifiedDiff(same, same, "a", "b"); err != nil {
		t.Errorf("at boundary: got error %v, want nil", err)
	}
}

func mustMatch(t *testing.T, s string, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("expected match for %q in:\n%s", pattern, s)
	}
}

func mustNotMatch(t *testing.T, s string, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("expected NO match for %q in:\n%s", pattern, s)
	}
}
