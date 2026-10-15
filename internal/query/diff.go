package query

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines around each change.
const diffContext = 3

// maxLCSCells bounds the LCS table; larger inputs diff as one
// replacement.
const maxLCSCells = 4_000_000

// diffOp is one line of an edit script: ' ' kept, '-' removed from a,
// '+' added from b; a and b index the lines.
type diffOp struct {
	kind byte
	a, b int
}

// unifiedDiff renders a unified diff of the lines a and b, whose first
// lines are line startA and startB of their files, under the headers
// nameA and nameB. It is empty when the lines are equal.
func unifiedDiff(nameA, nameB string, a, b []string, startA, startB int) string {
	ops := editScript(a, b)
	var out strings.Builder
	for _, h := range diffHunks(ops) {
		if out.Len() == 0 {
			fmt.Fprintf(&out, "--- %s\n+++ %s\n", nameA, nameB)
		}
		writeHunk(&out, ops[h[0]:h[1]], a, b, startA, startB)
	}
	return out.String()
}

// editScript computes a shortest edit script from a to b through their
// longest common subsequence, deletions before insertions.
func editScript(a, b []string) []diffOp {
	n, m := len(a), len(b)
	if n*m > maxLCSCells {
		return replaceAll(n, m)
	}
	lcs := lcsTable(a, b)
	var ops []diffOp
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, diffOp{' ', i, j})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, diffOp{'-', i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', i, j})
			j++
		}
	}
	return ops
}

// replaceAll is the edit script removing n lines and adding m.
func replaceAll(n, m int) []diffOp {
	ops := make([]diffOp, 0, n+m)
	for i := 0; i < n; i++ {
		ops = append(ops, diffOp{'-', i, 0})
	}
	for j := 0; j < m; j++ {
		ops = append(ops, diffOp{'+', n, j})
	}
	return ops
}

// lcsTable holds at [i][j] the length of the longest common subsequence
// of a[i:] and b[j:].
func lcsTable(a, b []string) [][]int32 {
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	return lcs
}

// diffHunks returns the op ranges [lo, hi) of the hunks: changes with up
// to diffContext kept lines around them, merged when they touch.
func diffHunks(ops []diffOp) [][2]int {
	var out [][2]int
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo, hi := max(0, i-diffContext), min(len(ops), i+1+diffContext)
		if n := len(out); n > 0 && lo <= out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], hi)
			continue
		}
		out = append(out, [2]int{lo, hi})
	}
	return out
}

// writeHunk writes one hunk with its @@ header.
func writeHunk(out *strings.Builder, ops []diffOp, a, b []string, startA, startB int) {
	lenA, lenB := 0, 0
	for _, op := range ops {
		if op.kind != '+' {
			lenA++
		}
		if op.kind != '-' {
			lenB++
		}
	}
	fromA, fromB := startA+ops[0].a, startB+ops[0].b
	if lenA == 0 {
		fromA--
	}
	if lenB == 0 {
		fromB--
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", fromA, lenA, fromB, lenB)
	for _, op := range ops {
		line := ""
		if op.kind == '+' {
			line = b[op.b]
		} else {
			line = a[op.a]
		}
		out.WriteByte(op.kind)
		out.WriteString(line)
		out.WriteByte('\n')
	}
}
