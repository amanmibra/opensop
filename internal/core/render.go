package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/amanmibra/opensop/internal/model"
	"github.com/amanmibra/opensop/internal/pyx"
)

// VariableRE is Python's r"\{\{\s*([A-Za-z_][\w.-]*)\s*\}\}" with Unicode \s and \w.
var VariableRE = regexp.MustCompile(`\{\{[` + pyx.SpaceClass + `]*([A-Za-z_][\p{L}\p{N}_.-]*)[` + pyx.SpaceClass + `]*\}\}`)

// ToolPayload is what get_sop serves for one SOP.
type ToolPayload = pyx.Obj

// RenderedAgent is one agent's built prompt.
type RenderedAgent struct {
	Agent       *model.Agent
	Prompt      string
	Bases       []*model.Base
	SOPs        []*model.SOP
	ToolPayload pyx.Obj // SOP id → payload, for SOPs not delivered entirely in the prompt
	Tools       []string
}

// Hash is the sha256 of the prompt.
func (r *RenderedAgent) Hash() string { return sha256Hex(r.Prompt) }

// Build is a rendered workspace.
type Build struct {
	Agents   map[string]*RenderedAgent
	Warnings []Issue
}

// IDs returns the agent ids, sorted.
func (b *Build) IDs() []string {
	ids := make([]string, 0, len(b.Agents))
	for id := range b.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Lock is lock.json.
func (b *Build) Lock() pyx.Obj {
	agents := pyx.Obj{}
	for _, id := range b.IDs() {
		r := b.Agents[id]
		blocks := []any{pyx.Obj{{K: "kind", V: "agent"}, {K: "id", V: r.Agent.ID}, {K: "hash", V: sha256Hex(r.Agent.DumpJSON())}}}
		for _, base := range r.Bases {
			blocks = append(blocks, pyx.Obj{{K: "kind", V: "base"}, {K: "id", V: base.ID}, {K: "hash", V: sha256Hex(base.DumpJSON())}})
		}
		for _, s := range r.SOPs {
			blocks = append(blocks, pyx.Obj{{K: "kind", V: "sop"}, {K: "id", V: s.ID}, {K: "hash", V: sha256Hex(s.DumpJSON())}})
		}
		agents = append(agents, pyx.KV{K: id, V: pyx.Obj{
			{K: "platform_ref", V: r.Agent.PlatformRef()},
			{K: "hash", V: r.Hash()},
			{K: "blocks", V: blocks},
			{K: "tools", V: r.Tools},
		}})
	}
	return pyx.Obj{{K: "version", V: 1}, {K: "agents", V: agents}}
}

// RenderWorkspace validates and renders every agent.
func RenderWorkspace(ws *Workspace) (*Build, error) {
	issues := Validate(ws)
	var errs, warnings []Issue
	for _, i := range issues {
		if i.IsError() {
			errs = append(errs, i)
		} else {
			warnings = append(warnings, i)
		}
	}
	if len(errs) > 0 {
		return nil, &Error{errs}
	}
	b := &Build{Agents: map[string]*RenderedAgent{}, Warnings: warnings}
	for _, id := range ws.AgentOrder {
		b.Agents[id] = RenderAgent(ws, ws.Agents[id])
	}
	return b, nil
}

// RenderAgent builds one agent's prompt.
func RenderAgent(ws *Workspace, agent *model.Agent) *RenderedAgent {
	bases := ResolveBases(ws, agent)
	sops := ResolveSOPs(ws, agent)

	var sections []string
	for _, b := range bases {
		if b.Position == "top" {
			sections = append(sections, b.Text)
		}
	}
	if instr := pyx.Strip(agent.Instructions); instr != "" {
		sections = append(sections, instr)
	}
	if len(sops) > 0 {
		parts := make([]string, len(sops))
		for i, s := range sops {
			parts[i] = RenderSOPInPrompt(s)
		}
		sections = append(sections, ws.Config.SopsHeading+"\n\n"+strings.Join(parts, "\n\n"))
	}
	for _, b := range bases {
		if b.Position == "bottom" {
			sections = append(sections, b.Text)
		}
	}
	var nonEmpty []string
	for _, s := range sections {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	values := model.Merge(ws.Config.Variables, agent.Variables)
	prompt := FillVariables(strings.Join(nonEmpty, "\n\n"), values) + "\n"

	payload := pyx.Obj{}
	toolSet := map[string]bool{}
	for _, s := range sops {
		if s.Delivery != "prompt" {
			payload = append(payload, pyx.KV{K: s.ID, V: fillPayload(SOPToolPayload(s), values)})
		}
		for _, t := range SOPTools(s) {
			toolSet[t] = true
		}
	}
	tools := []string{}
	for t := range toolSet {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	return &RenderedAgent{Agent: agent, Prompt: prompt, Bases: bases, SOPs: sops, ToolPayload: payload, Tools: tools}
}

// fillPayload is json.loads(fill_variables(json.dumps(payload), values, escape_json=True)):
// variables are filled inside each JSON-encoded (ASCII-escaped) string.
func fillPayload(v any, values model.Vars) any {
	switch x := v.(type) {
	case string:
		quoted := pyx.QuoteJSON(x, true)
		filled := fillVariables(quoted, values, true)
		var out string
		if err := json.Unmarshal([]byte(filled), &out); err != nil {
			return x
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = fillPayload(item, values)
		}
		return out
	case pyx.Obj:
		out := make(pyx.Obj, len(x))
		for i, kv := range x {
			out[i] = pyx.KV{K: kv.K, V: fillPayload(kv.V, values)}
		}
		return out
	}
	return v
}

// --- resolution ---------------------------------------------------------------------

func names(agent *model.Agent) map[string]bool {
	return map[string]bool{agent.ID: true, agent.PlatformRef(): true}
}

func contains(list []string, set map[string]bool) bool {
	for _, x := range list {
		if set[x] {
			return true
		}
	}
	return false
}

// Targets reports whether a base/SOP applies to the agent through its own `agents:` field.
func Targets(t *model.Targeting, agent *model.Agent) bool {
	n := names(agent)
	if contains(t.Exclude, n) {
		return false
	}
	if t.AgentsAll {
		return true
	}
	return contains(t.Agents, n)
}

func has(list []string, x string) bool {
	for _, item := range list {
		if item == x {
			return true
		}
	}
	return false
}

func optedOut(id string, locked bool, agent *model.Agent) bool {
	return has(agent.Exclude, id) && !locked
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ResolveBases is the ordered list of bases an agent gets.
func ResolveBases(ws *Workspace, agent *model.Agent) []*model.Base {
	var targeted []string
	for _, id := range sortedKeys(ws.Bases) {
		if Targets(&ws.Bases[id].Targeting, agent) {
			targeted = append(targeted, id)
		}
	}
	var ordered []string
	var visit func(id string, stack []string)
	visit = func(id string, stack []string) {
		if has(ordered, id) || has(stack, id) {
			return
		}
		base, ok := ws.Bases[id]
		if !ok {
			return
		}
		for _, parent := range base.Inherits {
			visit(parent, append(append([]string(nil), stack...), id))
		}
		ordered = append(ordered, id)
	}
	for _, id := range agent.Inherits {
		visit(id, nil)
	}
	for _, id := range targeted {
		b := ws.Bases[id]
		if !optedOut(b.ID, b.Locked, agent) {
			visit(id, nil)
		}
	}
	out := make([]*model.Base, len(ordered))
	for i, id := range ordered {
		out[i] = ws.Bases[id]
	}
	return out
}

// ResolveSOPs is the ordered list of SOPs an agent gets.
func ResolveSOPs(ws *Workspace, agent *model.Agent) []*model.SOP {
	matching := map[string]*model.SOP{}
	for _, s := range ws.SOPs {
		if Targets(&s.Targeting, agent) && !optedOut(s.ID, s.Locked, agent) {
			matching[s.ID] = s
		}
	}
	var out []*model.SOP
	for _, id := range ws.Config.SopOrder {
		if s, ok := matching[id]; ok {
			out = append(out, s)
		}
	}
	for _, id := range sortedKeys(matching) {
		if !has(ws.Config.SopOrder, id) {
			out = append(out, matching[id])
		}
	}
	return out
}

// --- SOP text -----------------------------------------------------------------------

func clean(text string) string { return strings.TrimRight(pyx.Strip(text), ".") }

// RenderStep renders a step; kind is "step", "forbidden" or "warning".
func RenderStep(s model.Step, kind string) string {
	if s.Tool == nil || *s.Tool == "" {
		return pyx.Strip(s.Text)
	}
	if kind == "forbidden" {
		return clean(s.Text) + ". This applies to the `" + *s.Tool + "` tool."
	}
	return clean(s.Text) + ". Use the `" + *s.Tool + "` tool."
}

// RenderSOP renders an SOP's text; the flags leave parts out.
func RenderSOP(s *model.SOP, steps, guards, details bool) string {
	lines := []string{"### " + s.Name}
	if details && s.Description != "" {
		lines = append(lines, "Goal: "+pyx.Strip(s.Description))
	}
	if s.Scope != "" {
		lines = append(lines, "When this applies: "+pyx.Strip(s.Scope))
	}
	if details && s.Guidance != "" {
		lines = append(lines, "", pyx.Strip(s.Guidance))
	}
	if steps && len(s.ProcedureSteps) > 0 {
		lines = append(lines, "", "Steps:")
		for n, st := range s.ProcedureSteps {
			lines = append(lines, itoa(n+1)+". "+RenderStep(st, "step"))
		}
	}
	if guards && len(s.ForbiddenActions) > 0 {
		lines = append(lines, "", "Never:")
		for _, st := range s.ForbiddenActions {
			lines = append(lines, "- "+RenderStep(st, "forbidden"))
		}
	}
	if guards && len(s.WarningSigns) > 0 {
		lines = append(lines, "", "Warning signs:")
		for _, st := range s.WarningSigns {
			lines = append(lines, "- "+RenderStep(st, "warning"))
		}
	}
	return strings.Join(lines, "\n")
}

// RenderSOPInPrompt is what the prompt carries for an SOP, by delivery mode.
func RenderSOPInPrompt(s *model.SOP) string {
	if s.Delivery == "prompt" {
		return RenderSOP(s, true, true, true)
	}
	fetch := "Before following this procedure, call the `get_sop` tool with id `" + s.ID + "` for the full steps."
	if s.Delivery == "auto" {
		return RenderSOP(s, false, true, false) + "\n\n" + fetch
	}
	return RenderSOP(s, false, false, false) + "\n\n" + fetch
}

func stepsPayload(steps []model.Step) []any {
	out := make([]any, len(steps))
	for i, s := range steps {
		out[i] = s.PayloadJSON()
	}
	return out
}

// SOPToolPayload is what get_sop serves for an SOP (before variables are filled).
func SOPToolPayload(s *model.SOP) pyx.Obj {
	return pyx.Obj{
		{K: "id", V: s.ID},
		{K: "name", V: s.Name},
		{K: "text", V: RenderSOP(s, true, true, true)},
		{K: "description", V: s.Description},
		{K: "scope", V: s.Scope},
		{K: "guidance", V: s.Guidance},
		{K: "procedureSteps", V: stepsPayload(s.ProcedureSteps)},
		{K: "forbiddenActions", V: stepsPayload(s.ForbiddenActions)},
		{K: "warningSigns", V: stepsPayload(s.WarningSigns)},
	}
}

// SOPTools lists the tools an SOP's steps name, in order.
func SOPTools(s *model.SOP) []string {
	var out []string
	for _, group := range [][]model.Step{s.ProcedureSteps, s.ForbiddenActions, s.WarningSigns} {
		for _, st := range group {
			if st.Tool != nil && *st.Tool != "" {
				out = append(out, *st.Tool)
			}
		}
	}
	return out
}

// --- helpers --------------------------------------------------------------------------

// FindVariables returns the placeholder names used in text.
func FindVariables(text string) map[string]bool {
	out := map[string]bool{}
	for _, m := range VariableRE.FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
	}
	return out
}

// FillVariables replaces {{name}} with values; unknown names are left as they are.
func FillVariables(text string, values model.Vars) string { return fillVariables(text, values, false) }

func fillVariables(text string, values model.Vars, escapeJSON bool) string {
	return VariableRE.ReplaceAllStringFunc(text, func(m string) string {
		name := VariableRE.FindStringSubmatch(m)[1]
		v, ok := values.Get(name)
		if !ok {
			return m
		}
		if escapeJSON {
			q := pyx.QuoteJSON(v, true)
			return q[1 : len(q)-1]
		}
		return v
	})
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
