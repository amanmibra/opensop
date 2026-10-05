package core

import (
	"sort"
	"strings"

	"github.com/amanmibra/opensop/internal/pyx"
)

// AffectedAgent is one agent to test.
type AffectedAgent struct {
	ID          string
	PlatformRef string
	Platform    string
	PlatformID  string
	Reason      string // changed | added | requested | all
	Changed     []string
	ChangedSOPs []string
	SOPs        []string
}

func (a *AffectedAgent) obj() pyx.Obj {
	return pyx.Obj{
		{K: "id", V: a.ID}, {K: "platform_ref", V: a.PlatformRef}, {K: "platform", V: a.Platform},
		{K: "platform_id", V: a.PlatformID}, {K: "reason", V: a.Reason}, {K: "changed", V: a.Changed},
		{K: "changed_sops", V: a.ChangedSOPs}, {K: "sops", V: a.SOPs},
	}
}

// Affected is the agents to test for a change.
type Affected struct {
	Agents []*AffectedAgent
	All    bool
}

// IDs are the OpenSOP ids.
func (r *Affected) IDs() []string {
	out := []string{}
	for _, a := range r.Agents {
		out = append(out, a.ID)
	}
	return out
}

// ToJSON is Affected.to_dict().
func (r *Affected) ToJSON() pyx.Obj {
	agents := []any{}
	for _, a := range r.Agents {
		agents = append(agents, a.obj())
	}
	return pyx.Obj{{K: "all", V: r.All}, {K: "count", V: len(r.Agents)}, {K: "agents", V: agents}}
}

// GitHubOutputs are the values for $GITHUB_OUTPUT, in order.
func (r *Affected) GitHubOutputs() pyx.Obj {
	var platformIDs []string
	matrix := []any{}
	for _, a := range r.Agents {
		platformIDs = append(platformIDs, a.PlatformID)
		matrix = append(matrix, a.obj())
	}
	all := "false"
	if r.All {
		all = "true"
	}
	return pyx.Obj{
		{K: "ids", V: strings.Join(r.IDs(), " ")},
		{K: "platform_ids", V: strings.Join(platformIDs, " ")},
		{K: "matrix", V: pyx.Dumps(matrix, pyx.JSONOptions{EnsureASCII: true, ItemSep: ",", KeySep: ":"})},
		{K: "count", V: itoa(len(r.Agents))},
		{K: "all", V: all},
	}
}

// Markdown is the step summary.
func (r *Affected) Markdown() string {
	if len(r.Agents) == 0 {
		return "### Agents to test\n\nNone.\n"
	}
	why := "affected by this change"
	if r.All {
		why = "all agents"
	}
	lines := []string{"### Agents to test (" + itoa(len(r.Agents)) + ", " + why + ")", ""}
	for _, a := range r.Agents {
		detail := " (" + a.Reason + ")"
		if len(a.Changed) > 0 {
			detail = ": " + strings.Join(a.Changed, ", ")
		}
		lines = append(lines, "- `"+a.ID+"` ("+a.Platform+" `"+a.PlatformID+"`)"+detail)
	}
	return strings.Join(lines, "\n") + "\n"
}

// ComputeAffected picks the agents to test. requested overrides the comparison; with a base,
// agents whose prompt changed are selected; allIfNone selects everyone when nothing else is.
func ComputeAffected(head, base *Build, requested []string, allIfNone bool) (*Affected, error) {
	if len(requested) > 0 {
		ids, err := resolveRequested(head, requested)
		if err != nil {
			return nil, err
		}
		out := &Affected{Agents: []*AffectedAgent{}}
		for _, id := range ids {
			out.Agents = append(out.Agents, affectedAgent(head, id, "requested"))
		}
		return out, nil
	}
	picked := []*AffectedAgent{}
	if base != nil {
		plan := MakePlan(SnapshotOf(base), SnapshotOf(head))
		for _, c := range plan.Changes {
			if c.Status == "removed" {
				continue
			}
			reason := "changed"
			if c.Status == "added" {
				reason = "added"
			}
			a := affectedAgent(head, c.AgentID, reason)
			a.Changed = c.blockKeys()
			for _, b := range a.Changed {
				if strings.HasPrefix(b, "sop:") {
					a.ChangedSOPs = append(a.ChangedSOPs, strings.SplitN(b, ":", 2)[1])
				}
			}
			sort.Strings(a.ChangedSOPs)
			picked = append(picked, a)
		}
	}
	if len(picked) > 0 || !(allIfNone || base == nil) {
		return &Affected{Agents: picked}, nil
	}
	out := &Affected{Agents: []*AffectedAgent{}, All: true}
	for _, id := range head.IDs() {
		out.Agents = append(out.Agents, affectedAgent(head, id, "all"))
	}
	return out, nil
}

func affectedAgent(head *Build, id, reason string) *AffectedAgent {
	r := head.Agents[id]
	a := r.Agent
	sops := []string{}
	for _, s := range r.SOPs {
		sops = append(sops, s.ID)
	}
	return &AffectedAgent{
		ID: id, PlatformRef: a.PlatformRef(), Platform: a.Platform(), PlatformID: a.PlatformID(),
		Reason: reason, Changed: []string{}, ChangedSOPs: []string{}, SOPs: sops,
	}
}

func resolveRequested(head *Build, requested []string) ([]string, error) {
	lookup := map[string]string{}
	// Python iterates head.agents in sorted order; setdefault keeps the first platform id.
	for _, id := range head.IDs() {
		a := head.Agents[id].Agent
		lookup[a.PlatformRef()] = id
		if _, ok := lookup[a.PlatformID()]; !ok {
			lookup[a.PlatformID()] = id
		}
	}
	for _, id := range head.IDs() {
		lookup[id] = id
	}
	var ids, unknown []string
	for _, name := range requested {
		id, ok := lookup[name]
		if !ok {
			unknown = append(unknown, name)
		} else if !has(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(unknown) > 0 {
		return nil, &Error{[]Issue{{Code: "unknown_agent", Message: "unknown agent(s): " + strings.Join(unknown, ", ") + ". Known: " + strings.Join(head.IDs(), ", ")}}}
	}
	return ids, nil
}
