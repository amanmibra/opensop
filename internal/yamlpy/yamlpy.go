// Package yamlpy loads YAML the way PyYAML's yaml.safe_load does, closely enough for
// OpenSOP files: YAML 1.1 implicit types for plain scalars (yes/no/on/off booleans,
// sexagesimal 1:30 ints, octal 017, dates), merge keys, anchors, and Python-style
// dict semantics for duplicate keys (first position, last value).
//
// Parsing is done by gopkg.in/yaml.v3; only scalar typing and construction are redone
// here. Error messages for malformed YAML come from yaml.v3 and differ in wording from
// PyYAML's.
package yamlpy

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/amanmibra/opensop/internal/pyx"
)

// Kind is the Python type a YAML node constructs to.
type Kind int

const (
	KNull Kind = iota
	KBool
	KInt
	KFloat
	KStr
	KDate     // datetime.date
	KDateTime // datetime.datetime
	KList
	KDict
	KBytes // !!binary and other values OpenSOP never accepts
)

// Value is a constructed YAML value.
type Value struct {
	Kind  Kind
	Bool  bool
	Int   *big.Int
	Float float64
	Str   string // Str: the text; Date/DateTime: Python's str(); Bytes: a description
	repr  string // Date/DateTime: Python's repr()
	List  []*Value
	Dict  *Dict
}

// Dict is an insertion-ordered mapping, like a Python dict.
type Dict struct {
	Keys   []*Value
	Values []*Value
}

// Len is the number of keys.
func (d *Dict) Len() int { return len(d.Keys) }

// Get looks up a string key.
func (d *Dict) Get(key string) (*Value, bool) {
	for i, k := range d.Keys {
		if k.Kind == KStr && k.Str == key {
			return d.Values[i], true
		}
	}
	return nil, false
}

// Set assigns a key the way a Python dict does: an existing equal key keeps its position.
func (d *Dict) Set(key, value *Value) {
	for i, k := range d.Keys {
		if KeyEqual(k, key) {
			d.Values[i] = value
			return
		}
	}
	d.Keys = append(d.Keys, key)
	d.Values = append(d.Values, value)
}

// SetStr sets a string key.
func (d *Dict) SetStr(key string, value *Value) { d.Set(&Value{Kind: KStr, Str: key}, value) }

// Copy returns a shallow copy.
func (d *Dict) Copy() *Dict {
	return &Dict{Keys: append([]*Value(nil), d.Keys...), Values: append([]*Value(nil), d.Values...)}
}

func numeric(v *Value) (*big.Float, bool) {
	switch v.Kind {
	case KBool:
		if v.Bool {
			return big.NewFloat(1), true
		}
		return big.NewFloat(0), true
	case KInt:
		return new(big.Float).SetInt(v.Int), true
	case KFloat:
		if math.IsNaN(v.Float) || math.IsInf(v.Float, 0) {
			return nil, false
		}
		return big.NewFloat(v.Float), true
	}
	return nil, false
}

// KeyEqual is Python's == for hashable YAML values (1 == 1.0 == True).
func KeyEqual(a, b *Value) bool {
	if na, ok := numeric(a); ok {
		if nb, ok := numeric(b); ok {
			return na.Cmp(nb) == 0
		}
		return false
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KNull:
		return true
	case KFloat:
		return a.Float == b.Float
	case KStr, KDate, KDateTime, KBytes:
		return a.Str == b.Str
	}
	return false
}

// Truthy is Python's bool(value).
func (v *Value) Truthy() bool {
	switch v.Kind {
	case KNull:
		return false
	case KBool:
		return v.Bool
	case KInt:
		return v.Int.Sign() != 0
	case KFloat:
		return v.Float != 0
	case KStr, KBytes:
		return v.Str != ""
	case KList:
		return len(v.List) > 0
	case KDict:
		return v.Dict.Len() > 0
	}
	return true
}

// PyStr is Python's str(value).
func (v *Value) PyStr() string {
	switch v.Kind {
	case KStr, KDate, KDateTime, KBytes:
		return v.Str
	}
	return v.Repr()
}

// Repr is Python's repr(value).
func (v *Value) Repr() string {
	switch v.Kind {
	case KNull:
		return "None"
	case KBool:
		if v.Bool {
			return "True"
		}
		return "False"
	case KInt:
		return v.Int.String()
	case KFloat:
		return pyx.ReprFloat(v.Float)
	case KStr:
		return pyx.ReprStr(v.Str)
	case KDate, KDateTime, KBytes:
		return v.repr
	case KList:
		parts := make([]string, len(v.List))
		for i, item := range v.List {
			parts[i] = item.Repr()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case KDict:
		parts := make([]string, v.Dict.Len())
		for i := range v.Dict.Keys {
			parts[i] = v.Dict.Keys[i].Repr() + ": " + v.Dict.Values[i].Repr()
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return "?"
}

// Error is a YAML problem, reported as invalid_yaml.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// SafeLoad parses text like yaml.safe_load. An empty document is nil (Python None).
func SafeLoad(text string) (*Value, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, &Error{cleanErr(err)}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, &Error{"expected a single document in the stream but found another document"}
	} else if !errors.Is(err, io.EOF) {
		return nil, &Error{cleanErr(err)}
	}
	if err := checkTabs(text); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	c := &constructor{}
	v, err := c.construct(doc.Content[0], 0)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func cleanErr(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	return strings.Join(strings.Fields(msg), " ")
}

type constructor struct{}

const maxDepth = 1000

func (c *constructor) construct(n *yaml.Node, depth int) (*Value, error) {
	if depth > maxDepth {
		return nil, &Error{"too deeply nested (recursive alias?)"}
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return &Value{Kind: KNull}, nil
		}
		return c.construct(n.Content[0], depth+1)
	case yaml.AliasNode:
		return c.construct(n.Alias, depth+1)
	case yaml.ScalarNode:
		return constructScalar(n)
	case yaml.SequenceNode:
		if n.Style&yaml.TaggedStyle != 0 && n.Tag != "!!seq" {
			return nil, noConstructor(n)
		}
		out := &Value{Kind: KList, List: []*Value{}}
		for _, item := range n.Content {
			v, err := c.construct(item, depth+1)
			if err != nil {
				return nil, err
			}
			out.List = append(out.List, v)
		}
		return out, nil
	case yaml.MappingNode:
		if n.Style&yaml.TaggedStyle != 0 && n.Tag != "!!map" {
			return nil, noConstructor(n)
		}
		pairs, err := c.flatten(n, depth)
		if err != nil {
			return nil, err
		}
		d := &Dict{}
		for _, p := range pairs {
			k, err := c.construct(p[0], depth+1)
			if err != nil {
				return nil, err
			}
			if k.Kind == KList || k.Kind == KDict {
				return nil, &Error{fmt.Sprintf("while constructing a mapping found unhashable key (line %d, column %d)", p[0].Line, p[0].Column)}
			}
			v, err := c.construct(p[1], depth+1)
			if err != nil {
				return nil, err
			}
			d.Set(k, v)
		}
		return &Value{Kind: KDict, Dict: d}, nil
	}
	return nil, &Error{"unsupported YAML node"}
}

func noConstructor(n *yaml.Node) error {
	return &Error{fmt.Sprintf("could not determine a constructor for the tag '%s' (line %d, column %d)", longTag(n.Tag), n.Line, n.Column)}
}

func longTag(tag string) string {
	if strings.HasPrefix(tag, "!!") {
		return "tag:yaml.org,2002:" + tag[2:]
	}
	return tag
}

func deref(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func isMergeKey(k *yaml.Node) bool {
	k = deref(k)
	if k.Kind != yaml.ScalarNode {
		return false
	}
	if k.Style&yaml.TaggedStyle != 0 {
		return k.Tag == "!!merge"
	}
	return isPlain(k) && k.Value == "<<"
}

// flatten is PyYAML's SafeConstructor.flatten_mapping: merged pairs come first.
func (c *constructor) flatten(n *yaml.Node, depth int) ([][2]*yaml.Node, error) {
	if depth > maxDepth {
		return nil, &Error{"too deeply nested (recursive alias?)"}
	}
	var merge, own [][2]*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !isMergeKey(k) {
			own = append(own, [2]*yaml.Node{k, v})
			continue
		}
		v = deref(v)
		switch v.Kind {
		case yaml.MappingNode:
			sub, err := c.flatten(v, depth+1)
			if err != nil {
				return nil, err
			}
			merge = append(merge, sub...)
		case yaml.SequenceNode:
			var subs [][][2]*yaml.Node
			for _, item := range v.Content {
				item = deref(item)
				if item.Kind != yaml.MappingNode {
					return nil, &Error{"while constructing a mapping expected a mapping for merging"}
				}
				sub, err := c.flatten(item, depth+1)
				if err != nil {
					return nil, err
				}
				subs = append(subs, sub)
			}
			for i := len(subs) - 1; i >= 0; i-- {
				merge = append(merge, subs[i]...)
			}
		default:
			return nil, &Error{"while constructing a mapping expected a mapping or list of mappings for merging"}
		}
	}
	return append(merge, own...), nil
}

func isPlain(n *yaml.Node) bool {
	return n.Style&(yaml.TaggedStyle|yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0
}

// PyYAML's implicit resolvers (YAML 1.1), tried in this order.
var (
	reBool      = regexp.MustCompile(`^(?:yes|Yes|YES|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	reFloat     = regexp.MustCompile(`^(?:[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+][0-9]+)?|\.[0-9][0-9_]*(?:[eE][-+][0-9]+)?|[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
	reInt       = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+|[-+]?0[0-7_]+|[-+]?(?:0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+|[-+]?[1-9][0-9_]*(?::[0-5]?[0-9])+)$`)
	reNull      = regexp.MustCompile(`^(?:~|null|Null|NULL|)$`)
	reTimestamp = regexp.MustCompile(`^(?:[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]|[0-9][0-9][0-9][0-9]-[0-9][0-9]?-[0-9][0-9]?(?:[Tt]|[ \t]+)[0-9][0-9]?:[0-9][0-9]:[0-9][0-9](?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9][0-9]?(?::[0-9][0-9])?))?)$`)
	reTSParts   = regexp.MustCompile(`^([0-9]{4})-([0-9][0-9]?)-([0-9][0-9]?)(?:(?:[Tt]|[ \t]+)([0-9][0-9]?):([0-9][0-9]):([0-9][0-9])(?:\.([0-9]*))?(?:[ \t]*(Z|([-+])([0-9][0-9]?)(?::([0-9][0-9]))?))?)?$`)
)

func constructScalar(n *yaml.Node) (*Value, error) {
	s := n.Value
	tag := ""
	switch {
	case n.Style&yaml.TaggedStyle != 0:
		tag = n.Tag
	case !isPlain(n):
		tag = "!!str"
	case reBool.MatchString(s):
		tag = "!!bool"
	case reFloat.MatchString(s):
		tag = "!!float"
	case reInt.MatchString(s):
		tag = "!!int"
	case s == "<<":
		tag = "!!merge"
	case reNull.MatchString(s):
		tag = "!!null"
	case reTimestamp.MatchString(s):
		tag = "!!timestamp"
	case s == "=":
		tag = "!!value"
	default:
		tag = "!!str"
	}
	switch tag {
	case "!!str", "!":
		return &Value{Kind: KStr, Str: s}, nil
	case "!!null":
		return &Value{Kind: KNull}, nil
	case "!!bool":
		l := strings.ToLower(s)
		switch l {
		case "yes", "true", "on":
			return &Value{Kind: KBool, Bool: true}, nil
		case "no", "false", "off":
			return &Value{Kind: KBool, Bool: false}, nil
		}
	case "!!int":
		if i, ok := constructInt(s); ok {
			return &Value{Kind: KInt, Int: i}, nil
		}
	case "!!float":
		if f, ok := constructFloat(s); ok {
			return &Value{Kind: KFloat, Float: f}, nil
		}
	case "!!timestamp":
		if v, ok := constructTimestamp(s); ok {
			return v, nil
		}
	case "!!binary":
		clean := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, s)
		if data, err := base64.StdEncoding.DecodeString(clean); err == nil {
			r := bytesRepr(data)
			return &Value{Kind: KBytes, Str: r, repr: r}, nil
		}
	}
	return nil, noConstructor(&yaml.Node{Tag: tag, Line: n.Line, Column: n.Column})
}

func constructInt(s string) (*big.Int, bool) {
	s = strings.ReplaceAll(s, "_", "")
	if s == "" {
		return nil, false
	}
	sign := int64(1)
	if s[0] == '-' {
		sign = -1
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	var v *big.Int
	var ok bool
	switch {
	case s == "0":
		v, ok = big.NewInt(0), true
	case strings.HasPrefix(s, "0b"):
		v, ok = new(big.Int).SetString(s[2:], 2)
	case strings.HasPrefix(s, "0x"):
		v, ok = new(big.Int).SetString(s[2:], 16)
	case strings.HasPrefix(s, "0"):
		v, ok = new(big.Int).SetString(s, 8)
	case strings.Contains(s, ":"):
		parts := strings.Split(s, ":")
		v, ok = big.NewInt(0), true
		base := big.NewInt(1)
		for i := len(parts) - 1; i >= 0; i-- {
			d, good := new(big.Int).SetString(parts[i], 10)
			if !good {
				return nil, false
			}
			v.Add(v, new(big.Int).Mul(d, base))
			base.Mul(base, big.NewInt(60))
		}
	default:
		v, ok = new(big.Int).SetString(s, 10)
	}
	if !ok {
		return nil, false
	}
	return v.Mul(v, big.NewInt(sign)), true
}

func constructFloat(s string) (float64, bool) {
	s = strings.ToLower(strings.ReplaceAll(s, "_", ""))
	if s == "" {
		return 0, false
	}
	sign := 1.0
	if s[0] == '-' {
		sign = -1
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	switch {
	case s == ".inf":
		return sign * math.Inf(1), true
	case s == ".nan":
		return math.NaN(), true
	case strings.Contains(s, ":"):
		parts := strings.Split(s, ":")
		v, base := 0.0, 1.0
		for i := len(parts) - 1; i >= 0; i-- {
			d, err := strconv.ParseFloat(parts[i], 64)
			if err != nil {
				return 0, false
			}
			v += d * base
			base *= 60
		}
		return sign * v, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && errors.Is(ne.Err, strconv.ErrRange) {
			return sign * f, true
		}
		return 0, false
	}
	return sign * f, true
}

func constructTimestamp(s string) (*Value, bool) {
	m := reTSParts.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	if month < 1 || month > 12 || day < 1 || day > daysIn(year, month) || year < 1 {
		return nil, false
	}
	date := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	if m[4] == "" {
		return &Value{Kind: KDate, Str: date, repr: fmt.Sprintf("datetime.date(%d, %d, %d)", year, month, day)}, true
	}
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	second, _ := strconv.Atoi(m[6])
	if hour > 23 || minute > 59 || second > 59 {
		return nil, false
	}
	frac := 0
	if m[7] != "" {
		f := m[7]
		if len(f) > 6 {
			f = f[:6]
		}
		f += strings.Repeat("0", 6-len(f))
		frac, _ = strconv.Atoi(f)
	}
	str := fmt.Sprintf("%s %02d:%02d:%02d", date, hour, minute, second)
	if frac != 0 {
		str += fmt.Sprintf(".%06d", frac)
	}
	reprArgs := []int{year, month, day, hour, minute}
	if second != 0 || frac != 0 {
		reprArgs = append(reprArgs, second)
	}
	if frac != 0 {
		reprArgs = append(reprArgs, frac)
	}
	parts := make([]string, len(reprArgs))
	for i, a := range reprArgs {
		parts[i] = strconv.Itoa(a)
	}
	repr := "datetime.datetime(" + strings.Join(parts, ", ")
	switch {
	case m[9] != "":
		th, _ := strconv.Atoi(m[10])
		tm, _ := strconv.Atoi(m[11])
		offset := th*60 + tm
		sign := "+"
		if m[9] == "-" {
			sign = "-"
		}
		str += fmt.Sprintf("%s%02d:%02d", sign, offset/60, offset%60)
		secs := offset * 60
		if sign == "-" {
			secs = -secs
		}
		repr += ", tzinfo=" + tzRepr(secs)
	case m[8] != "":
		str += "+00:00"
		repr += ", tzinfo=datetime.timezone.utc"
	}
	return &Value{Kind: KDateTime, Str: str, repr: repr + ")"}, true
}

func tzRepr(secs int) string {
	if secs == 0 {
		return "datetime.timezone.utc"
	}
	days := 0
	if secs < 0 {
		days = -1
		secs += 86400
	}
	if days != 0 {
		return fmt.Sprintf("datetime.timezone(datetime.timedelta(days=%d, seconds=%d))", days, secs)
	}
	return fmt.Sprintf("datetime.timezone(datetime.timedelta(seconds=%d))", secs)
}

func daysIn(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// bytesRepr is Python's repr() of bytes.
func bytesRepr(b []byte) string {
	quote := byte('\'')
	if strings.IndexByte(string(b), '\'') >= 0 && strings.IndexByte(string(b), '"') < 0 {
		quote = '"'
	}
	var out strings.Builder
	out.WriteString("b")
	out.WriteByte(quote)
	for _, c := range b {
		switch {
		case c == quote || c == '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case c == '\t':
			out.WriteString(`\t`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c < 0x20 || c >= 0x7f:
			out.WriteString(fmt.Sprintf(`\x%02x`, c))
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte(quote)
	return out.String()
}
