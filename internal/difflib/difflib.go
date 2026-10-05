// Package difflib is a faithful port of the parts of Python's difflib OpenSOP uses:
// SequenceMatcher (ratio, opcodes, grouped opcodes; isjunk=None, with or without
// autojunk) and unified_diff.
package difflib

import (
	"sort"
	"strconv"
	"strings"
)

// Match is a matching block: a[A:A+Size] == b[B:B+Size].
type Match struct{ A, B, Size int }

// OpCode is one (tag, i1, i2, j1, j2) instruction.
type OpCode struct {
	Tag            string
	I1, I2, J1, J2 int
}

// SequenceMatcher compares two sequences of comparable items.
type SequenceMatcher[T comparable] struct {
	a, b     []T
	b2j      map[T][]int
	autojunk bool
	matching []Match
	opcodes  []OpCode
}

// New is SequenceMatcher(None, a, b, autojunk=autojunk).
func New[T comparable](a, b []T, autojunk bool) *SequenceMatcher[T] {
	m := &SequenceMatcher[T]{a: a, b: b, autojunk: autojunk}
	m.chainB()
	return m
}

func (m *SequenceMatcher[T]) chainB() {
	m.b2j = map[T][]int{}
	for i, elt := range m.b {
		m.b2j[elt] = append(m.b2j[elt], i)
	}
	n := len(m.b)
	if m.autojunk && n >= 200 {
		ntest := n/100 + 1
		for elt, idxs := range m.b2j {
			if len(idxs) > ntest {
				delete(m.b2j, elt)
			}
		}
	}
}

func (m *SequenceMatcher[T]) findLongestMatch(alo, ahi, blo, bhi int) Match {
	a, b := m.a, m.b
	besti, bestj, bestsize := alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range m.b2j[a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	// No junk (isjunk is None), so only the first two extension loops of Python's version apply.
	for besti > alo && bestj > blo && a[besti-1] == b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && a[besti+bestsize] == b[bestj+bestsize] {
		bestsize++
	}
	return Match{besti, bestj, bestsize}
}

// MatchingBlocks is get_matching_blocks().
func (m *SequenceMatcher[T]) MatchingBlocks() []Match {
	if m.matching != nil {
		return m.matching
	}
	la, lb := len(m.a), len(m.b)
	queue := [][4]int{{0, la, 0, lb}}
	var blocks []Match
	for len(queue) > 0 {
		q := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		alo, ahi, blo, bhi := q[0], q[1], q[2], q[3]
		x := m.findLongestMatch(alo, ahi, blo, bhi)
		i, j, k := x.A, x.B, x.Size
		if k > 0 {
			blocks = append(blocks, x)
			if alo < i && blo < j {
				queue = append(queue, [4]int{alo, i, blo, j})
			}
			if i+k < ahi && j+k < bhi {
				queue = append(queue, [4]int{i + k, ahi, j + k, bhi})
			}
		}
	}
	sort.Slice(blocks, func(x, y int) bool {
		if blocks[x].A != blocks[y].A {
			return blocks[x].A < blocks[y].A
		}
		if blocks[x].B != blocks[y].B {
			return blocks[x].B < blocks[y].B
		}
		return blocks[x].Size < blocks[y].Size
	})
	i1, j1, k1 := 0, 0, 0
	var out []Match
	for _, blk := range blocks {
		if i1+k1 == blk.A && j1+k1 == blk.B {
			k1 += blk.Size
		} else {
			if k1 > 0 {
				out = append(out, Match{i1, j1, k1})
			}
			i1, j1, k1 = blk.A, blk.B, blk.Size
		}
	}
	if k1 > 0 {
		out = append(out, Match{i1, j1, k1})
	}
	out = append(out, Match{la, lb, 0})
	m.matching = out
	return out
}

// OpCodes is get_opcodes().
func (m *SequenceMatcher[T]) OpCodes() []OpCode {
	if m.opcodes != nil {
		return m.opcodes
	}
	i, j := 0, 0
	answer := []OpCode{}
	for _, blk := range m.MatchingBlocks() {
		tag := ""
		switch {
		case i < blk.A && j < blk.B:
			tag = "replace"
		case i < blk.A:
			tag = "delete"
		case j < blk.B:
			tag = "insert"
		}
		if tag != "" {
			answer = append(answer, OpCode{tag, i, blk.A, j, blk.B})
		}
		i, j = blk.A+blk.Size, blk.B+blk.Size
		if blk.Size > 0 {
			answer = append(answer, OpCode{"equal", blk.A, i, blk.B, j})
		}
	}
	m.opcodes = answer
	return answer
}

// Ratio is ratio(): 2*M/T.
func (m *SequenceMatcher[T]) Ratio() float64 {
	matches := 0
	for _, blk := range m.MatchingBlocks() {
		matches += blk.Size
	}
	total := len(m.a) + len(m.b)
	if total == 0 {
		return 1.0
	}
	return 2.0 * float64(matches) / float64(total)
}

// GroupedOpCodes is get_grouped_opcodes(n).
func (m *SequenceMatcher[T]) GroupedOpCodes(n int) [][]OpCode {
	codes := append([]OpCode(nil), m.OpCodes()...)
	if len(codes) == 0 {
		codes = []OpCode{{"equal", 0, 1, 0, 1}}
	}
	if codes[0].Tag == "equal" {
		c := codes[0]
		codes[0] = OpCode{c.Tag, max(c.I1, c.I2-n), c.I2, max(c.J1, c.J2-n), c.J2}
	}
	if last := len(codes) - 1; codes[last].Tag == "equal" {
		c := codes[last]
		codes[last] = OpCode{c.Tag, c.I1, min(c.I2, c.I1+n), c.J1, min(c.J2, c.J1+n)}
	}
	nn := n + n
	var groups [][]OpCode
	var group []OpCode
	for _, c := range codes {
		i1, i2, j1, j2 := c.I1, c.I2, c.J1, c.J2
		if c.Tag == "equal" && i2-i1 > nn {
			group = append(group, OpCode{c.Tag, i1, min(i2, i1+n), j1, min(j2, j1+n)})
			groups = append(groups, group)
			group = nil
			i1, j1 = max(i1, i2-n), max(j1, j2-n)
		}
		group = append(group, OpCode{c.Tag, i1, i2, j1, j2})
	}
	if len(group) > 0 && !(len(group) == 1 && group[0].Tag == "equal") {
		groups = append(groups, group)
	}
	return groups
}

func formatRangeUnified(start, stop int) string {
	beginning := start + 1
	length := stop - start
	if length == 1 {
		return strconv.Itoa(beginning)
	}
	if length == 0 {
		beginning--
	}
	return strconv.Itoa(beginning) + "," + strconv.Itoa(length)
}

// UnifiedDiff is "".join(difflib.unified_diff(a, b, fromfile, tofile)) with n=3 and
// lineterm="\n"; a and b are lines that keep their line endings.
func UnifiedDiff(a, b []string, fromfile, tofile string) string {
	var out strings.Builder
	started := false
	for _, group := range New(a, b, true).GroupedOpCodes(3) {
		if !started {
			started = true
			out.WriteString("--- " + fromfile + "\n")
			out.WriteString("+++ " + tofile + "\n")
		}
		first, last := group[0], group[len(group)-1]
		out.WriteString("@@ -" + formatRangeUnified(first.I1, last.I2) + " +" + formatRangeUnified(first.J1, last.J2) + " @@\n")
		for _, c := range group {
			if c.Tag == "equal" {
				for _, line := range a[c.I1:c.I2] {
					out.WriteString(" " + line)
				}
				continue
			}
			if c.Tag == "replace" || c.Tag == "delete" {
				for _, line := range a[c.I1:c.I2] {
					out.WriteString("-" + line)
				}
			}
			if c.Tag == "replace" || c.Tag == "insert" {
				for _, line := range b[c.J1:c.J2] {
					out.WriteString("+" + line)
				}
			}
		}
	}
	return out.String()
}
