package core

import (
	"sort"
	"strings"

	"github.com/amanmibra/opensop/internal/model"
	"github.com/amanmibra/opensop/internal/pyx"
)

func basePath(id string) string  { return "bases/" + id + ".md" }
func sopPath(id string) string   { return "procedures/" + id + ".yaml" }
func agentPath(id string) string { return "agents/" + id + ".yaml" }

// Validate runs the checks that need the whole workspace: references, cycles, locks, variables.
func Validate(ws *Workspace) []Issue {
	var issues []Issue
	agentNames := map[string]bool{}
	for _, a := range ws.Agents {
		agentNames[a.ID] = true
		agentNames[a.PlatformRef()] = true
	}

	// Python iterates a set intersection here (hash order); sorted is the deterministic equivalent.
	var dups []string
	for id := range ws.Bases {
		if _, ok := ws.SOPs[id]; ok {
			dups = append(dups, id)
		}
	}
	sort.Strings(dups)
	for _, id := range dups {
		issues = append(issues, Issue{Code: "duplicate_id", Message: "'" + id + "' is both a base and an SOP; ids must be unique"})
	}

	seenRefs := map[string]string{}
	for _, id := range ws.AgentOrder {
		a := ws.Agents[id]
		ref := a.PlatformRef()
		if prev, ok := seenRefs[ref]; ok {
			issues = append(issues, Issue{Code: "duplicate_platform_ref", Message: ref + " is used by '" + prev + "' and '" + a.ID + "'", Path: agentPath(a.ID)})
		}
		seenRefs[ref] = a.ID
	}

	type targeted struct {
		path string
		t    *model.Targeting
	}
	var blocks []targeted
	for _, id := range ws.BaseOrder {
		blocks = append(blocks, targeted{basePath(id), &ws.Bases[id].Targeting})
	}
	for _, id := range ws.SOPOrder {
		blocks = append(blocks, targeted{sopPath(id), &ws.SOPs[id].Targeting})
	}
	for _, b := range blocks {
		if !b.t.AgentsAll {
			for _, name := range b.t.Agents {
				if !agentNames[name] {
					issues = append(issues, Issue{Code: "unknown_agent", Message: "agents lists '" + name + "', which is not a known agent", Path: b.path})
				}
			}
		}
		for _, name := range b.t.Exclude {
			if !agentNames[name] {
				issues = append(issues, Issue{Code: "unknown_agent", Message: "exclude lists '" + name + "', which is not a known agent", Path: b.path})
			}
		}
	}

	for _, id := range ws.BaseOrder {
		for _, parent := range ws.Bases[id].Inherits {
			if _, ok := ws.Bases[parent]; !ok {
				issues = append(issues, Issue{Code: "unknown_base", Message: "inherits '" + parent + "', which is not a base", Path: basePath(id)})
			}
		}
	}
	issues = append(issues, inheritanceCycles(ws)...)

	for _, id := range ws.SOPOrder {
		if pyx.Strip(ws.SOPs[id].Description) == "" {
			issues = append(issues, Issue{Code: "missing_goal", Message: "no description (goal); QA can't judge whether the goal was met", Path: sopPath(id), Severity: "warning"})
		}
	}
	for _, id := range ws.Config.SopOrder {
		if _, ok := ws.SOPs[id]; !ok {
			issues = append(issues, Issue{Code: "unknown_sop", Message: "sop_order lists '" + id + "', which is not an SOP", Path: "opensop.yaml"})
		}
	}

	for _, id := range ws.AgentOrder {
		agent := ws.Agents[id]
		p := agentPath(agent.ID)
		for _, baseID := range agent.Inherits {
			if _, ok := ws.Bases[baseID]; !ok {
				issues = append(issues, Issue{Code: "unknown_base", Message: "inherits '" + baseID + "', which is not a base", Path: p})
			}
		}
		for _, blockID := range agent.Exclude {
			var t *model.Targeting
			locked, found := false, false
			if s, ok := ws.SOPs[blockID]; ok { // {**bases, **sops}: an SOP wins a shared id
				t, locked, found = &s.Targeting, s.Locked, true
			} else if b, ok := ws.Bases[blockID]; ok {
				t, locked, found = &b.Targeting, b.Locked, true
			}
			switch {
			case !found:
				issues = append(issues, Issue{Code: "unknown_block", Message: "exclude lists '" + blockID + "', which is not a base or SOP", Path: p})
			case locked && Targets(t, agent):
				issues = append(issues, Issue{Code: "locked", Message: "can't exclude '" + blockID + "': it is locked", Path: p})
			case !Targets(t, agent):
				issues = append(issues, Issue{Code: "useless_exclude", Message: "exclude lists '" + blockID + "', which doesn't target this agent", Path: p, Severity: "warning"})
			}
		}

		stop := false
		for _, i := range issues {
			if i.Code == "unknown_base" || i.Code == "inheritance_cycle" {
				stop = true
				break
			}
		}
		if stop {
			continue // resolution below would be misleading
		}
		used := map[string]bool{}
		addAll := func(m map[string]bool) {
			for k := range m {
				used[k] = true
			}
		}
		for _, b := range ResolveBases(ws, agent) {
			addAll(FindVariables(b.Text))
		}
		addAll(FindVariables(agent.Instructions))
		for _, s := range ResolveSOPs(ws, agent) {
			// Python searches str(sop_tool_payload(s)), i.e. the repr of each string in it.
			forEachString(SOPToolPayload(s), func(text string) { addAll(FindVariables(pyx.ReprStr(text))) })
		}
		values := model.Merge(ws.Config.Variables, agent.Variables)
		var missing []string
		for name := range used {
			if _, ok := values.Get(name); !ok {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		for _, name := range missing {
			issues = append(issues, Issue{Code: "unset_variable", Message: "'{{" + name + "}}' is used but has no value", Path: p})
		}
	}
	return issues
}

func forEachString(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case []any:
		for _, item := range x {
			forEachString(item, f)
		}
	case pyx.Obj:
		for _, kv := range x {
			forEachString(kv.V, f)
		}
	}
}

func inheritanceCycles(ws *Workspace) []Issue {
	var issues []Issue
	reported := map[string]bool{}
	var walk func(id string, stack []string)
	walk = func(id string, stack []string) {
		for i, s := range stack {
			if s == id {
				cycle := stack[i:]
				set := append([]string(nil), cycle...)
				sort.Strings(set)
				key := strings.Join(dedupe(set), "\x00")
				if !reported[key] {
					reported[key] = true
					issues = append(issues, Issue{Code: "inheritance_cycle", Message: strings.Join(append(append([]string(nil), cycle...), id), " → "), Path: basePath(cycle[0])})
				}
				return
			}
		}
		if base, ok := ws.Bases[id]; ok {
			for _, parent := range base.Inherits {
				walk(parent, append(append([]string(nil), stack...), id))
			}
		}
	}
	for _, id := range sortedKeys(ws.Bases) {
		walk(id, nil)
	}
	return issues
}

func dedupe(sorted []string) []string {
	var out []string
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}
