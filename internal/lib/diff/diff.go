// Package diff implements a small in-house unified line-diff for the
// `rufio diff` command. Mirrors src/lib/diff.ts.
//
// The acceptance bar (locked in week-1 Phase 7 review): produces a
// readable diff for human-sized text files. Inputs above MaxDiffLines
// throw a typed error rather than running the LCS-table allocation
// (week-1 review M2 fix — without this, 10k×10k allocates ~760MB and
// can OOM container CI).
package diff

import (
	"fmt"
	"strings"
)

const (
	// MaxDiffLines is the per-side line cap for unifiedDiff. Above this,
	// the LCS table memory becomes unreasonable. Callers exceeding the
	// cap should branch to an external diff tool or summarise differently.
	MaxDiffLines = 20000

	// contextLines is the number of unchanged lines emitted around each
	// change block. Standard `diff -u` default.
	contextLines = 3
)

// IsBinary heuristically detects binary content via a NUL byte in the
// first 8KB. Matches what `git` uses.
func IsBinary(buf []byte) bool {
	limit := len(buf)
	if limit > 8192 {
		limit = 8192
	}
	for i := 0; i < limit; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

type opKind int

const (
	opCtx opKind = iota
	opAdd
	opDel
)

type op struct {
	kind opKind
	line string
}

type positionedOp struct {
	op
	aLine int // 1-indexed line in FROM
	bLine int // 1-indexed line in TO
}

// UnifiedDiff returns a unified line-diff between a and b. Returns "" on
// identical inputs. Output ends with a trailing newline when non-empty.
//
// Throws a non-typed error when either side exceeds MaxDiffLines (the
// caller can wrap it in a typed error if it wants to map to an exit code).
func UnifiedDiff(a, b, fromLabel, toLabel string) (string, error) {
	if a == b {
		return "", nil
	}
	aLines := strings.Split(a, "\n")
	bLines := strings.Split(b, "\n")
	if len(aLines) > MaxDiffLines || len(bLines) > MaxDiffLines {
		return "", fmt.Errorf(
			"diff input exceeds %d-line limit (left=%d, right=%d); use an external diff tool",
			MaxDiffLines, len(aLines), len(bLines),
		)
	}
	ops := computeOps(aLines, bLines)
	allCtx := true
	for _, o := range ops {
		if o.kind != opCtx {
			allCtx = false
			break
		}
	}
	if allCtx {
		return "", nil
	}
	positioned := positionOps(ops)
	hunks := computeHunks(positioned)
	if len(hunks) == 0 {
		return "", nil
	}
	var b1 strings.Builder
	fmt.Fprintf(&b1, "--- %s\n", fromLabel)
	fmt.Fprintf(&b1, "+++ %s\n", toLabel)
	for _, h := range hunks {
		var aCount, bCount int
		for k := h.start; k <= h.end; k++ {
			o := positioned[k]
			if o.kind == opCtx || o.kind == opDel {
				aCount++
			}
			if o.kind == opCtx || o.kind == opAdd {
				bCount++
			}
		}
		head := positioned[h.start]
		fmt.Fprintf(&b1, "@@ -%d,%d +%d,%d @@\n", head.aLine, aCount, head.bLine, bCount)
		for k := h.start; k <= h.end; k++ {
			o := positioned[k]
			prefix := " "
			if o.kind == opAdd {
				prefix = "+"
			} else if o.kind == opDel {
				prefix = "-"
			}
			b1.WriteString(prefix)
			b1.WriteString(o.line)
			b1.WriteString("\n")
		}
	}
	out := b1.String()
	return strings.TrimRight(out, "\n") + "\n", nil
}

// computeOps builds the LCS table and backtracks into a sequence of
// ctx/add/del ops. Standard dynamic-programming algorithm.
func computeOps(aLines, bLines []string) []op {
	m, n := len(aLines), len(bLines)
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if aLines[i-1] == bLines[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] >= lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}
	var ops []op
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && aLines[i-1] == bLines[j-1] {
			ops = append([]op{{kind: opCtx, line: aLines[i-1]}}, ops...)
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			ops = append([]op{{kind: opAdd, line: bLines[j-1]}}, ops...)
			j--
		} else {
			ops = append([]op{{kind: opDel, line: aLines[i-1]}}, ops...)
			i--
		}
	}
	return ops
}

// positionOps tags each op with its 1-indexed line numbers in the FROM/TO files.
func positionOps(ops []op) []positionedOp {
	out := make([]positionedOp, len(ops))
	aIdx, bIdx := 0, 0
	for i, o := range ops {
		out[i] = positionedOp{op: o, aLine: aIdx + 1, bLine: bIdx + 1}
		if o.kind == opCtx || o.kind == opDel {
			aIdx++
		}
		if o.kind == opCtx || o.kind == opAdd {
			bIdx++
		}
	}
	return out
}

type hunk struct{ start, end int }

// computeHunks groups change ops with up to contextLines of context, merging
// overlapping ranges.
func computeHunks(positioned []positionedOp) []hunk {
	var changeIdx []int
	for k, o := range positioned {
		if o.kind != opCtx {
			changeIdx = append(changeIdx, k)
		}
	}
	if len(changeIdx) == 0 {
		return nil
	}
	lastIdx := len(positioned) - 1
	var hunks []hunk
	start := max0(changeIdx[0] - contextLines)
	end := minN(lastIdx, changeIdx[0]+contextLines)
	for i := 1; i < len(changeIdx); i++ {
		idx := changeIdx[i]
		padStart := max0(idx - contextLines)
		padEnd := minN(lastIdx, idx+contextLines)
		if padStart <= end+1 {
			if padEnd > end {
				end = padEnd
			}
		} else {
			hunks = append(hunks, hunk{start, end})
			start = padStart
			end = padEnd
		}
	}
	hunks = append(hunks, hunk{start, end})
	return hunks
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

func minN(a, b int) int {
	if a < b {
		return a
	}
	return b
}
