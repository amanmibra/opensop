package pyx

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Obj is a JSON object that keeps its key order, like a Python dict.
type Obj []KV

// KV is one key and value of an Obj.
type KV struct {
	K string
	V any
}

// Get returns the value for key k.
func (o Obj) Get(k string) (any, bool) {
	for _, kv := range o {
		if kv.K == k {
			return kv.V, true
		}
	}
	return nil, false
}

// JSONOptions mirrors the json.dumps arguments OpenSOP uses.
type JSONOptions struct {
	Indent      int    // 0: no newlines (Python indent=None)
	EnsureASCII bool   // Python's default
	ItemSep     string // default ", " (or "," when Indent > 0)
	KeySep      string // default ": "
}

// Dumps is Python's json.dumps for the value types OpenSOP produces:
// nil, bool, int, int64, *big.Int, float64, string, []string, []any, Obj, map[string]string (sorted? no: use Obj).
func Dumps(v any, o JSONOptions) string {
	if o.ItemSep == "" {
		if o.Indent > 0 {
			o.ItemSep = ","
		} else {
			o.ItemSep = ", "
		}
	}
	if o.KeySep == "" {
		o.KeySep = ": "
	}
	var b strings.Builder
	encode(&b, v, o, 0)
	return b.String()
}

// DumpsIndent2 is json.dumps(v, indent=2).
func DumpsIndent2(v any) string { return Dumps(v, JSONOptions{Indent: 2, EnsureASCII: true}) }

// DumpsIndent2Unicode is json.dumps(v, indent=2, ensure_ascii=False).
func DumpsIndent2Unicode(v any) string { return Dumps(v, JSONOptions{Indent: 2}) }

// DumpsCompact is Pydantic's model_dump_json() style: no spaces, non-ASCII kept.
func DumpsCompact(v any) string { return Dumps(v, JSONOptions{ItemSep: ",", KeySep: ":"}) }

func newline(b *strings.Builder, o JSONOptions, level int) {
	if o.Indent > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", o.Indent*level))
	}
}

func encode(b *strings.Builder, v any, o JSONOptions, level int) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case *big.Int:
		b.WriteString(x.String())
	case float64:
		b.WriteString(jsonFloat(x))
	case string:
		b.WriteString(QuoteJSON(x, o.EnsureASCII))
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		encode(b, items, o, level)
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteString(o.ItemSep)
			}
			newline(b, o, level+1)
			encode(b, item, o, level+1)
		}
		newline(b, o, level)
		b.WriteByte(']')
	case Obj:
		if len(x) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteString(o.ItemSep)
			}
			newline(b, o, level+1)
			b.WriteString(QuoteJSON(kv.K, o.EnsureASCII))
			b.WriteString(o.KeySep)
			encode(b, kv.V, o, level+1)
		}
		newline(b, o, level)
		b.WriteByte('}')
	default:
		panic(fmt.Sprintf("pyx.Dumps: unsupported type %T", v))
	}
}

func jsonFloat(f float64) string {
	r := ReprFloat(f)
	switch r {
	case "inf":
		return "Infinity"
	case "-inf":
		return "-Infinity"
	case "nan":
		return "NaN"
	}
	return r
}

// QuoteJSON is Python's JSON string encoding (json.dumps of a str); ensureASCII escapes
// everything outside ASCII as \uXXXX (with surrogate pairs), as Python's default does.
// With ensureASCII false it matches both json.dumps(ensure_ascii=False) and Pydantic's
// model_dump_json (serde_json): only ", \ and control characters are escaped.
func QuoteJSON(s string, ensureASCII bool) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20:
				b.WriteString(`\u` + hexN(int(r), 4))
			case r < 0x7f || (r == 0x7f) || !ensureASCII:
				if r == 0x7f && ensureASCII {
					b.WriteString(`\u007f`)
				} else {
					b.WriteRune(r)
				}
			case r > 0xffff:
				r1, r2 := utf16.EncodeRune(r)
				b.WriteString(`\u` + hexN(int(r1), 4) + `\u` + hexN(int(r2), 4))
			default:
				b.WriteString(`\u` + hexN(int(r), 4))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
