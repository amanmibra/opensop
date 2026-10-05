// Package model holds the OpenSOP file formats (bases, SOPs, agents, opensop.yaml), their
// validation from parsed YAML, and their canonical JSON (the same bytes as Pydantic's
// model_dump_json() in the Python version, which lock.json block hashes are taken over).
//
// Field order and JSON property names must stay in sync with spec/*.schema.json; a test
// checks this.
package model

import "github.com/amanmibra/opensop/internal/pyx"

// Platforms in the order the format checks them.
var Platforms = []string{"livekit", "vapi", "elevenlabs"}

// Targeting is the `agents` / `exclude` pair shared by bases and SOPs.
type Targeting struct {
	AgentsAll bool     // agents: "*"
	Agents    []string // agents: [...]
	Exclude   []string
}

// Step is a step, forbidden action or warning sign. A plain-string step has IsText set.
type Step struct {
	IsText   bool // written as a plain string
	Text     string
	Tool     *string
	Required bool
}

// Base is bases/<id>.md.
type Base struct {
	Targeting
	ID       string
	Inherits []string
	Locked   bool
	Position string // "top" | "bottom"
	Text     string
}

// SOP is procedures/<id>.yaml.
type SOP struct {
	Targeting
	ID               string
	Name             string
	Locked           bool
	Delivery         string // "prompt" | "auto" | "tool"
	Description      string
	Scope            string
	Guidance         string
	ProcedureSteps   []Step
	ForbiddenActions []Step
	WarningSigns     []Step
}

// Vars is an insertion-ordered string map (Python dict[str, str]).
type Vars struct {
	Keys []string
	m    map[string]string
}

// Set adds or replaces a value, keeping the first position.
func (v *Vars) Set(k, val string) {
	if v.m == nil {
		v.m = map[string]string{}
	}
	if _, ok := v.m[k]; !ok {
		v.Keys = append(v.Keys, k)
	}
	v.m[k] = val
}

// Get looks up a value.
func (v Vars) Get(k string) (string, bool) {
	val, ok := v.m[k]
	return val, ok
}

// Merge is {**a, **b}.
func Merge(a, b Vars) Vars {
	var out Vars
	for _, k := range a.Keys {
		out.Set(k, a.m[k])
	}
	for _, k := range b.Keys {
		out.Set(k, b.m[k])
	}
	return out
}

func (v Vars) obj() pyx.Obj {
	o := pyx.Obj{}
	for _, k := range v.Keys {
		o = append(o, pyx.KV{K: k, V: v.m[k]})
	}
	return o
}

// Agent is agents/<id>.yaml.
type Agent struct {
	ID           string
	Livekit      *string
	Vapi         *string
	Elevenlabs   *string
	Inherits     []string
	Exclude      []string
	Variables    Vars
	Instructions string
}

// PlatformValue is getattr(agent, platform).
func (a *Agent) PlatformValue(p string) *string {
	switch p {
	case "livekit":
		return a.Livekit
	case "vapi":
		return a.Vapi
	case "elevenlabs":
		return a.Elevenlabs
	}
	return nil
}

// Platform is the one platform the agent sets.
func (a *Agent) Platform() string {
	for _, p := range Platforms {
		if v := a.PlatformValue(p); v != nil && *v != "" {
			return p
		}
	}
	return ""
}

// PlatformID is the platform's own id for the agent.
func (a *Agent) PlatformID() string { return *a.PlatformValue(a.Platform()) }

// PlatformRef is "livekit:tonys-pizza".
func (a *Agent) PlatformRef() string { return a.Platform() + ":" + a.PlatformID() }

// Config is opensop.yaml.
type Config struct {
	Version     int
	Variables   Vars
	SopsHeading string
	SopOrder    []string
}

// Workspace is a whole OpenSOP folder.
type Workspace struct {
	Config Config
	Bases  map[string]*Base
	SOPs   map[string]*SOP
	Agents map[string]*Agent
}

// --- canonical JSON (Pydantic model_dump_json) ---------------------------------------

func strs(s []string) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}

func optStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func (t Targeting) agentsJSON() any {
	if t.AgentsAll {
		return "*"
	}
	return strs(t.Agents)
}

func stepsJSON(steps []Step) []any {
	out := make([]any, len(steps))
	for i, s := range steps {
		if s.IsText {
			out[i] = s.Text
		} else {
			out[i] = pyx.Obj{{K: "text", V: s.Text}, {K: "tool", V: optStr(s.Tool)}, {K: "required", V: s.Required}}
		}
	}
	return out
}

// DumpJSON is Pydantic's Base.model_dump_json().
func (b *Base) DumpJSON() string {
	return pyx.DumpsCompact(pyx.Obj{
		{K: "agents", V: b.agentsJSON()},
		{K: "exclude", V: strs(b.Exclude)},
		{K: "id", V: b.ID},
		{K: "inherits", V: strs(b.Inherits)},
		{K: "locked", V: b.Locked},
		{K: "position", V: b.Position},
		{K: "text", V: b.Text},
	})
}

// DumpJSON is Pydantic's SOP.model_dump_json().
func (s *SOP) DumpJSON() string {
	return pyx.DumpsCompact(pyx.Obj{
		{K: "agents", V: s.agentsJSON()},
		{K: "exclude", V: strs(s.Exclude)},
		{K: "id", V: s.ID},
		{K: "name", V: s.Name},
		{K: "locked", V: s.Locked},
		{K: "delivery", V: s.Delivery},
		{K: "description", V: s.Description},
		{K: "scope", V: s.Scope},
		{K: "guidance", V: s.Guidance},
		{K: "procedureSteps", V: stepsJSON(s.ProcedureSteps)},
		{K: "forbiddenActions", V: stepsJSON(s.ForbiddenActions)},
		{K: "warningSigns", V: stepsJSON(s.WarningSigns)},
	})
}

// DumpJSON is Pydantic's Agent.model_dump_json().
func (a *Agent) DumpJSON() string {
	return pyx.DumpsCompact(pyx.Obj{
		{K: "id", V: a.ID},
		{K: "livekit", V: optStr(a.Livekit)},
		{K: "vapi", V: optStr(a.Vapi)},
		{K: "elevenlabs", V: optStr(a.Elevenlabs)},
		{K: "inherits", V: strs(a.Inherits)},
		{K: "exclude", V: strs(a.Exclude)},
		{K: "variables", V: a.Variables.obj()},
		{K: "instructions", V: a.Instructions},
	})
}

// PayloadJSON is Step.model_dump(exclude_defaults=True), for get_sop payloads.
func (s Step) PayloadJSON() pyx.Obj {
	o := pyx.Obj{{K: "text", V: s.Text}}
	if s.Tool != nil {
		o = append(o, pyx.KV{K: "tool", V: *s.Tool})
	}
	if s.Required {
		o = append(o, pyx.KV{K: "required", V: true})
	}
	return o
}
