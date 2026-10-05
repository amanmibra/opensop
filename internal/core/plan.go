package core

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amanmibra/opensop/internal/difflib"
	"github.com/amanmibra/opensop/internal/pyx"
)

// --- build/ writer ----------------------------------------------------------------------

// WriteBuild writes build/<agent>.prompt.md, build/<agent>.tool.json and build/lock.json,
// removing stale prompt and tool files first. It returns the written paths.
func WriteBuild(b *Build, outDir string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	for _, pattern := range []string{"*.prompt.md", "*.tool.json"} {
		stale, _ := filepath.Glob(filepath.Join(globEscape(outDir), pattern))
		for _, p := range stale {
			if err := os.Remove(p); err != nil {
				return nil, err
			}
		}
	}
	var written []string
	write := func(name, text string) error {
		p := pyx.Join(outDir, name)
		written = append(written, p)
		return os.WriteFile(p, []byte(text), 0o644)
	}
	for _, id := range b.IDs() {
		r := b.Agents[id]
		if err := write(id+".prompt.md", r.Prompt); err != nil {
			return nil, err
		}
		if len(r.ToolPayload) > 0 {
			if err := write(id+".tool.json", pyx.DumpsIndent2Unicode(r.ToolPayload)+"\n"); err != nil {
				return nil, err
			}
		}
	}
	if err := write("lock.json", pyx.DumpsIndent2Unicode(b.Lock())+"\n"); err != nil {
		return nil, err
	}
	return written, nil
}

// --- snapshots and plans ------------------------------------------------------------------

// AgentSnapshot is what a build looks like from outside, for one agent.
type AgentSnapshot struct {
	Prompt      string
	PlatformRef string
	Blocks      map[string]string // "kind:id" → hash
}

// Snapshot is agent id → snapshot.
type Snapshot map[string]*AgentSnapshot

// SnapshotOf takes a snapshot of a fresh build.
func SnapshotOf(b *Build) Snapshot {
	snap := Snapshot{}
	for _, id := range b.IDs() {
		r := b.Agents[id]
		blocks := map[string]string{"agent:" + r.Agent.ID: sha256Hex(r.Agent.DumpJSON())}
		for _, base := range r.Bases {
			blocks["base:"+base.ID] = sha256Hex(base.DumpJSON())
		}
		for _, s := range r.SOPs {
			blocks["sop:"+s.ID] = sha256Hex(s.DumpJSON())
		}
		snap[id] = &AgentSnapshot{Prompt: r.Prompt, PlatformRef: r.Agent.PlatformRef(), Blocks: blocks}
	}
	return snap
}

// ReadSnapshot reads a build/ folder. A missing lock.json is an empty snapshot.
func ReadSnapshot(buildDir string) (Snapshot, error) {
	data, err := os.ReadFile(filepath.Join(buildDir, "lock.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Snapshot{}, nil
	} else if err != nil {
		return nil, err
	}
	var lock struct {
		Agents map[string]struct {
			PlatformRef string `json:"platform_ref"`
			Blocks      []struct {
				Kind string `json:"kind"`
				ID   string `json:"id"`
				Hash string `json:"hash"`
			} `json:"blocks"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	snap := Snapshot{}
	for id, entry := range lock.Agents {
		prompt, err := pyx.ReadText(filepath.Join(buildDir, id+".prompt.md"))
		if err != nil {
			return nil, err
		}
		blocks := map[string]string{}
		for _, b := range entry.Blocks {
			blocks[b.Kind+":"+b.ID] = b.Hash
		}
		snap[id] = &AgentSnapshot{Prompt: prompt, PlatformRef: entry.PlatformRef, Blocks: blocks}
	}
	return snap, nil
}

// AgentChange is one agent whose prompt differs.
type AgentChange struct {
	AgentID     string
	Status      string // added | removed | changed
	PlatformRef string
	Blocks      map[string]string // block → edited | added | removed
	Diff        string
}

func (c *AgentChange) blockKeys() []string { return sortedKeys(c.Blocks) }

// Plan is the difference between two snapshots.
type Plan struct{ Changes []*AgentChange }

// Empty reports whether nothing changes.
func (p *Plan) Empty() bool { return len(p.Changes) == 0 }

// BlockGroup is one ("base:brand-voice", "edited") → agents entry.
type BlockGroup struct {
	Block, Change string
	Agents        []string
}

// ByBlock groups changes by (block, change), sorted.
func (p *Plan) ByBlock() []BlockGroup {
	idx := map[[2]string]int{}
	var groups []BlockGroup
	for _, c := range p.Changes {
		for _, block := range c.blockKeys() {
			key := [2]string{block, c.Blocks[block]}
			i, ok := idx[key]
			if !ok {
				i = len(groups)
				idx[key] = i
				groups = append(groups, BlockGroup{Block: block, Change: c.Blocks[block]})
			}
			groups[i].Agents = append(groups[i].Agents, c.AgentID)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Block != groups[j].Block {
			return groups[i].Block < groups[j].Block
		}
		return groups[i].Change < groups[j].Change
	})
	return groups
}

// Summary is one line per added/removed agent and per changed block.
func (p *Plan) Summary() []string {
	var lines []string
	for _, c := range p.Changes {
		if c.Status != "changed" {
			lines = append(lines, "agent `"+c.AgentID+"` "+c.Status)
		}
	}
	for _, g := range p.ByBlock() {
		lines = append(lines, label(g.Block)+" "+verb(g.Block, g.Change)+" → "+count(len(g.Agents))+": "+strings.Join(g.Agents, ", "))
	}
	return lines
}

func (p *Plan) changedIDs() int { return len(p.Changes) }

// Markdown is the plan as a PR comment.
func (p *Plan) Markdown() string {
	if p.Empty() {
		return "**opensop plan:** no agent prompts change."
	}
	out := []string{"**opensop plan:** " + count(p.changedIDs()) + " change", ""}
	for _, line := range p.Summary() {
		out = append(out, "- "+line)
	}
	for _, c := range p.Changes {
		out = append(out, "", "<details><summary>"+c.AgentID+" ("+c.Status+")</summary>", "", "```diff", pyx.RStrip(c.Diff), "```", "</details>")
	}
	return strings.Join(out, "\n") + "\n"
}

// Text is the plan for a terminal.
func (p *Plan) Text(diffs bool) string {
	if p.Empty() {
		return "No agent prompts change.\n"
	}
	out := []string{count(p.changedIDs()) + " change:"}
	for _, line := range p.Summary() {
		out = append(out, "  "+line)
	}
	if diffs {
		for _, c := range p.Changes {
			out = append(out, "", pyx.RStrip(c.Diff))
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// ToJSON is Plan.to_dict().
func (p *Plan) ToJSON() pyx.Obj {
	changes := []any{}
	for _, c := range p.Changes {
		blocks := pyx.Obj{}
		for _, b := range c.blockKeys() {
			blocks = append(blocks, pyx.KV{K: b, V: c.Blocks[b]})
		}
		changes = append(changes, pyx.Obj{
			{K: "agent", V: c.AgentID}, {K: "platform_ref", V: c.PlatformRef}, {K: "status", V: c.Status},
			{K: "blocks", V: blocks}, {K: "diff", V: c.Diff},
		})
	}
	byBlock := []any{}
	for _, g := range p.ByBlock() {
		byBlock = append(byBlock, pyx.Obj{{K: "block", V: g.Block}, {K: "change", V: g.Change}, {K: "agents", V: g.Agents}})
	}
	return pyx.Obj{{K: "changes", V: changes}, {K: "by_block", V: byBlock}}
}

// MakePlan compares two snapshots.
func MakePlan(before, after Snapshot) *Plan {
	ids := map[string]bool{}
	for id := range before {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	plan := &Plan{}
	for _, id := range sortedKeys(ids) {
		old, new := before[id], after[id]
		if old != nil && new != nil && old.Prompt == new.Prompt {
			continue
		}
		c := &AgentChange{AgentID: id, Blocks: map[string]string{}}
		oldPrompt, newPrompt := "", ""
		switch {
		case old == nil:
			c.Status, c.PlatformRef, newPrompt = "added", new.PlatformRef, new.Prompt
		case new == nil:
			c.Status, c.PlatformRef, oldPrompt = "removed", old.PlatformRef, old.Prompt
		default:
			c.Status, c.PlatformRef, oldPrompt, newPrompt = "changed", new.PlatformRef, old.Prompt, new.Prompt
			c.Blocks = blockChanges(old.Blocks, new.Blocks)
		}
		c.Diff = difflib.UnifiedDiff(pyx.SplitLines(oldPrompt, true), pyx.SplitLines(newPrompt, true), "a/"+id+".prompt.md", "b/"+id+".prompt.md")
		plan.Changes = append(plan.Changes, c)
	}
	for _, c := range plan.Changes {
		if c.Status == "changed" && len(c.Blocks) == 0 {
			c.Blocks["workspace:opensop.yaml"] = "edited" // e.g. a default variable changed
		}
	}
	return plan
}

func blockChanges(old, new map[string]string) map[string]string {
	out := map[string]string{}
	for b, h := range old {
		if nh, ok := new[b]; !ok {
			out[b] = "removed"
		} else if nh != h {
			out[b] = "edited"
		}
	}
	for b := range new {
		if _, ok := old[b]; !ok {
			out[b] = "added"
		}
	}
	return out
}

func label(block string) string {
	kind, id, _ := strings.Cut(block, ":")
	if kind == "workspace" {
		return "`" + id + "`"
	}
	return map[string]string{"sop": "SOP", "agent": "agent file", "base": "base"}[kind] + " `" + id + "`"
}

func verb(block, kind string) string {
	if strings.HasPrefix(block, "agent:") || strings.HasPrefix(block, "workspace:") {
		return "edited"
	}
	return map[string]string{"edited": "edited", "added": "now applies", "removed": "no longer applies"}[kind]
}

func count(n int) string {
	if n == 1 {
		return "1 agent"
	}
	return itoa(n) + " agents"
}
