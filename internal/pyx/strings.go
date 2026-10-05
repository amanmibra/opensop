// Package pyx reproduces the Python string, path and JSON behaviors the OpenSOP CLI
// relied on, so the Go port's output matches the Python version byte for byte.
package pyx

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SpaceClass is a regexp character class body matching Python's str.isspace() characters.
const SpaceClass = `\t-\r\x{1c}-\x{1f}\x{85}\p{Z}`

// IsSpace reports whether r is whitespace for Python's str.isspace() / str.strip().
func IsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// Strip is Python's str.strip() with no arguments.
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// LStrip is Python's str.lstrip() with no arguments.
func LStrip(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// RStrip is Python's str.rstrip() with no arguments.
func RStrip(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// Fields is Python's str.split() with no arguments.
func Fields(s string) []string { return strings.FieldsFunc(s, IsSpace) }

func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// SplitLines is Python's str.splitlines(keepends).
func SplitLines(s string, keepends bool) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isLineBreak(r) {
			i += size
			continue
		}
		end := i + size
		if r == '\r' && end < len(s) && s[end] == '\n' {
			end++
		}
		if keepends {
			out = append(out, s[start:end])
		} else {
			out = append(out, s[start:i])
		}
		start, i = end, end
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// UniversalNewlines converts \r\n and \r to \n, as Python's text-mode reads do.
func UniversalNewlines(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// Lower is Python's str.lower() (close enough: Go's per-rune lowering).
func Lower(s string) string { return strings.ToLower(s) }

func isPrintable(r rune) bool {
	if r == ' ' {
		return true
	}
	return unicode.IsPrint(r)
}

// ReprStr is Python's repr() of a str.
func ReprStr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x80 && (r < 0x20 || r == 0x7f):
			b.WriteString(`\x` + hex2(int(r)))
		case r < 0x80 || isPrintable(r):
			b.WriteRune(r)
		case r <= 0xff:
			b.WriteString(`\x` + hex2(int(r)))
		case r <= 0xffff:
			b.WriteString(`\u` + hexN(int(r), 4))
		default:
			b.WriteString(`\U` + hexN(int(r), 8))
		}
	}
	b.WriteByte(quote)
	return b.String()
}

func hex2(n int) string { return hexN(n, 2) }

func hexN(n, width int) string {
	h := strconv.FormatInt(int64(n), 16)
	return strings.Repeat("0", width-len(h)) + h
}

// ReprFloat is Python's repr() of a float.
func ReprFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // e.g. -1.2345e+06
	neg := strings.HasPrefix(e, "-")
	e = strings.TrimPrefix(e, "-")
	mant, expStr, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expStr)
	digits := strings.Replace(mant, ".", "", 1)
	sign := ""
	if neg {
		sign = "-"
	}
	if exp < -4 || exp >= 16 {
		m := digits[:1]
		if len(digits) > 1 {
			m += "." + digits[1:]
		}
		es := "+"
		if exp < 0 {
			es = "-"
			exp = -exp
		}
		ex := strconv.Itoa(exp)
		if len(ex) < 2 {
			ex = "0" + ex
		}
		return sign + m + "e" + es + ex
	}
	var out string
	if exp < 0 {
		out = "0." + strings.Repeat("0", -exp-1) + digits
	} else if len(digits) <= exp+1 {
		out = digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
	} else {
		out = digits[:exp+1] + "." + digits[exp+1:]
	}
	return sign + out
}

// ReprInt is Python's repr() of an int.
func ReprInt(i *big.Int) string { return i.String() }
