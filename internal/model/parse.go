package model

import (
	"strings"

	y "github.com/amanmibra/opensop/internal/yamlpy"
)

// FieldError is one validation error, like an entry of Pydantic's ValidationError.errors().
// Messages follow Pydantic v2's wording for the cases OpenSOP files can hit.
type FieldError struct {
	Loc []string
	Msg string
}

// String is "loc: msg", or just msg when there's no location (as the Python loader prints).
func (e FieldError) String() string {
	if len(e.Loc) == 0 {
		return e.Msg
	}
	return strings.Join(e.Loc, ".") + ": " + e.Msg
}

const (
	msgString   = "Input should be a valid string"
	msgList     = "Input should be a valid list"
	msgDict     = "Input should be a valid dictionary"
	msgBool     = "Input should be a valid boolean"
	msgBoolText = "Input should be a valid boolean, unable to interpret input"
	msgRequired = "Field required"
	msgExtra    = "Extra inputs are not permitted"
	msgKeys     = "Keys should be strings"
)

type errs []FieldError

func (e *errs) add(loc []string, msg string) {
	*e = append(*e, FieldError{Loc: append([]string(nil), loc...), Msg: msg})
}

func at(loc []string, parts ...string) []string {
	return append(append([]string(nil), loc...), parts...)
}

// keyLoc is how Pydantic shows a dict key in an error location.
func keyLoc(k *y.Value) string {
	switch k.Kind {
	case y.KStr:
		return k.Str
	case y.KBool:
		if k.Bool {
			return "1"
		}
		return "0"
	case y.KInt:
		return k.Int.String()
	}
	return k.Repr()
}

func vStr(v *y.Value, loc []string, e *errs) string {
	if v.Kind != y.KStr {
		e.add(loc, msgString)
		return ""
	}
	return v.Str
}

func vOptStr(v *y.Value, loc []string, e *errs) *string {
	if v.Kind == y.KNull {
		return nil
	}
	s := vStr(v, loc, e)
	return &s
}

func vBool(v *y.Value, loc []string, e *errs) bool {
	switch v.Kind {
	case y.KBool:
		return v.Bool
	case y.KInt:
		if v.Int.IsInt64() && (v.Int.Int64() == 0 || v.Int.Int64() == 1) {
			return v.Int.Int64() == 1
		}
		e.add(loc, msgBoolText)
	case y.KFloat:
		if v.Float == 0 || v.Float == 1 {
			return v.Float == 1
		}
		e.add(loc, msgBool)
	case y.KStr:
		switch strings.ToLower(v.Str) {
		case "0", "off", "f", "false", "n", "no":
			return false
		case "1", "on", "t", "true", "y", "yes":
			return true
		}
		e.add(loc, msgBoolText)
	default:
		e.add(loc, msgBool)
	}
	return false
}

func literalMsg(choices []string) string {
	quoted := make([]string, len(choices))
	for i, c := range choices {
		quoted[i] = "'" + c + "'"
	}
	if len(quoted) == 1 {
		return "Input should be " + quoted[0]
	}
	return "Input should be " + strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

func vLiteral(v *y.Value, choices []string, loc []string, e *errs) string {
	if v.Kind == y.KStr {
		for _, c := range choices {
			if v.Str == c {
				return c
			}
		}
	}
	e.add(loc, literalMsg(choices))
	return choices[0]
}

func vStrList(v *y.Value, loc []string, e *errs) []string {
	if v.Kind != y.KList {
		e.add(loc, msgList)
		return nil
	}
	out := []string{}
	for i, item := range v.List {
		out = append(out, vStr(item, at(loc, itoa(i)), e))
	}
	return out
}

func vVars(v *y.Value, loc []string, e *errs) Vars {
	var out Vars
	if v.Kind != y.KDict {
		e.add(loc, msgDict)
		return out
	}
	for i, k := range v.Dict.Keys {
		kl := keyLoc(k)
		key := vStr(k, at(loc, kl, "[key]"), e)
		val := vStr(v.Dict.Values[i], at(loc, kl), e)
		out.Set(key, val)
	}
	return out
}

// agents: Literal["*"] | list[str]. Union errors are reported for every branch, and only
// when no branch accepts the input.
func vTargets(v *y.Value, loc []string, e *errs) (all bool, list []string) {
	if v.Kind == y.KStr && v.Str == "*" {
		return true, nil
	}
	var listErrs errs
	list = vStrList(v, at(loc, "list[str]"), &listErrs)
	if len(listErrs) == 0 {
		return false, list
	}
	e.add(at(loc, "literal['*']"), "Input should be '*'")
	*e = append(*e, listErrs...)
	return false, nil
}

// One step: str | Step
func vStep(v *y.Value, loc []string, e *errs) Step {
	if v.Kind == y.KStr {
		return Step{IsText: true, Text: v.Str}
	}
	e.add(at(loc, "str"), msgString)
	if v.Kind != y.KDict {
		e.add(at(loc, "Step"), "Input should be a valid dictionary or instance of Step")
		return Step{}
	}
	var s Step
	var stepErrs errs
	validateFields(v.Dict, at(loc, "Step"), &stepErrs, []field{
		{name: "text", required: true, set: func(v *y.Value, l []string) { s.Text = vStr(v, l, &stepErrs) }},
		{name: "tool", set: func(v *y.Value, l []string) { s.Tool = vOptStr(v, l, &stepErrs) }},
		{name: "required", set: func(v *y.Value, l []string) { s.Required = vBool(v, l, &stepErrs) }},
	})
	if len(stepErrs) == 0 {
		*e = (*e)[:len(*e)-1] // the dict is a valid Step: drop the str branch's error
		return s
	}
	*e = append(*e, stepErrs...)
	return s
}

func vSteps(v *y.Value, loc []string, e *errs) []Step {
	if v.Kind != y.KList {
		e.add(loc, msgList)
		return nil
	}
	out := []Step{}
	for i, item := range v.List {
		out = append(out, vStep(item, at(loc, itoa(i)), e))
	}
	return out
}

type field struct {
	name     string
	required bool
	set      func(v *y.Value, loc []string)
}

// validateFields mirrors a Pydantic model with extra="forbid": declared fields in order,
// then unknown or non-string keys in input order.
func validateFields(d *y.Dict, loc []string, e *errs, fields []field) {
	known := map[string]bool{}
	for _, f := range fields {
		known[f.name] = true
		v, ok := d.Get(f.name)
		if !ok {
			if f.required {
				e.add(at(loc, f.name), msgRequired)
			}
			continue
		}
		f.set(v, at(loc, f.name))
	}
	for _, k := range d.Keys {
		switch {
		case k.Kind != y.KStr:
			e.add(at(loc, keyLoc(k)), msgKeys)
		case !known[k.Str]:
			e.add(at(loc, k.Str), msgExtra)
		}
	}
}

func targetingFields(t *Targeting, e *errs) []field {
	t.Agents, t.Exclude = []string{}, []string{}
	return []field{
		{name: "agents", set: func(v *y.Value, l []string) { t.AgentsAll, t.Agents = vTargets(v, l, e) }},
		{name: "exclude", set: func(v *y.Value, l []string) { t.Exclude = vStrList(v, l, e) }},
	}
}

// ParseBase validates a base's front matter (with "id" and "text" already set).
func ParseBase(d *y.Dict) (*Base, []FieldError) {
	var e errs
	b := &Base{Inherits: []string{}, Position: "top"}
	fields := append(targetingFields(&b.Targeting, &e),
		field{name: "id", required: true, set: func(v *y.Value, l []string) { b.ID = vStr(v, l, &e) }},
		field{name: "inherits", set: func(v *y.Value, l []string) { b.Inherits = vStrList(v, l, &e) }},
		field{name: "locked", set: func(v *y.Value, l []string) { b.Locked = vBool(v, l, &e) }},
		field{name: "position", set: func(v *y.Value, l []string) { b.Position = vLiteral(v, []string{"top", "bottom"}, l, &e) }},
		field{name: "text", required: true, set: func(v *y.Value, l []string) { b.Text = vStr(v, l, &e) }},
	)
	validateFields(d, nil, &e, fields)
	if len(e) > 0 {
		return nil, e
	}
	return b, nil
}

// ParseSOP validates a procedure file (with "id" already set).
func ParseSOP(d *y.Dict) (*SOP, []FieldError) {
	var e errs
	s := &SOP{Delivery: "prompt", ProcedureSteps: []Step{}, ForbiddenActions: []Step{}, WarningSigns: []Step{}}
	fields := append(targetingFields(&s.Targeting, &e),
		field{name: "id", required: true, set: func(v *y.Value, l []string) { s.ID = vStr(v, l, &e) }},
		field{name: "name", required: true, set: func(v *y.Value, l []string) { s.Name = vStr(v, l, &e) }},
		field{name: "locked", set: func(v *y.Value, l []string) { s.Locked = vBool(v, l, &e) }},
		field{name: "delivery", set: func(v *y.Value, l []string) {
			s.Delivery = vLiteral(v, []string{"prompt", "auto", "tool"}, l, &e)
		}},
		field{name: "description", set: func(v *y.Value, l []string) { s.Description = vStr(v, l, &e) }},
		field{name: "scope", set: func(v *y.Value, l []string) { s.Scope = vStr(v, l, &e) }},
		field{name: "guidance", set: func(v *y.Value, l []string) { s.Guidance = vStr(v, l, &e) }},
		field{name: "procedureSteps", set: func(v *y.Value, l []string) { s.ProcedureSteps = vSteps(v, l, &e) }},
		field{name: "forbiddenActions", set: func(v *y.Value, l []string) { s.ForbiddenActions = vSteps(v, l, &e) }},
		field{name: "warningSigns", set: func(v *y.Value, l []string) { s.WarningSigns = vSteps(v, l, &e) }},
	)
	validateFields(d, nil, &e, fields)
	if len(e) > 0 {
		return nil, e
	}
	return s, nil
}

// ParseAgent validates an agent file (with "id" already set).
func ParseAgent(d *y.Dict) (*Agent, []FieldError) {
	var e errs
	a := &Agent{Inherits: []string{}, Exclude: []string{}}
	validateFields(d, nil, &e, []field{
		{name: "id", required: true, set: func(v *y.Value, l []string) { a.ID = vStr(v, l, &e) }},
		{name: "livekit", set: func(v *y.Value, l []string) { a.Livekit = vOptStr(v, l, &e) }},
		{name: "vapi", set: func(v *y.Value, l []string) { a.Vapi = vOptStr(v, l, &e) }},
		{name: "elevenlabs", set: func(v *y.Value, l []string) { a.Elevenlabs = vOptStr(v, l, &e) }},
		{name: "inherits", set: func(v *y.Value, l []string) { a.Inherits = vStrList(v, l, &e) }},
		{name: "exclude", set: func(v *y.Value, l []string) { a.Exclude = vStrList(v, l, &e) }},
		{name: "variables", set: func(v *y.Value, l []string) { a.Variables = vVars(v, l, &e) }},
		{name: "instructions", set: func(v *y.Value, l []string) { a.Instructions = vStr(v, l, &e) }},
	})
	if len(e) > 0 {
		return nil, e
	}
	n := 0
	for _, p := range Platforms {
		if v := a.PlatformValue(p); v != nil && *v != "" {
			n++
		}
	}
	if n != 1 {
		return nil, []FieldError{{Msg: "Value error, agent '" + a.ID + "' must set exactly one of " + strings.Join(Platforms, ", ")}}
	}
	return a, nil
}

// ParseConfig validates opensop.yaml.
func ParseConfig(d *y.Dict) (*Config, []FieldError) {
	var e errs
	c := &Config{Version: 1, SopsHeading: "## Procedures", SopOrder: []string{}}
	validateFields(d, nil, &e, []field{
		{name: "version", set: func(v *y.Value, l []string) {
			ok := false
			switch v.Kind {
			case y.KInt:
				ok = v.Int.IsInt64() && v.Int.Int64() == 1
			case y.KBool:
				ok = v.Bool
			case y.KFloat:
				ok = v.Float == 1
			}
			if !ok {
				e.add(l, "Input should be 1")
			}
		}},
		{name: "variables", set: func(v *y.Value, l []string) { c.Variables = vVars(v, l, &e) }},
		{name: "sops_heading", set: func(v *y.Value, l []string) { c.SopsHeading = vStr(v, l, &e) }},
		{name: "sop_order", set: func(v *y.Value, l []string) { c.SopOrder = vStrList(v, l, &e) }},
	})
	if len(e) > 0 {
		return nil, e
	}
	return c, nil
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}
