// Package analyze is the deterministic text analysis used for importing and reviewing
// prompts (Python's analyze.py): overlap, compare and check.
package analyze

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/amanmibra/opensop/internal/core"
	"github.com/amanmibra/opensop/internal/difflib"
	"github.com/amanmibra/opensop/internal/model"
	"github.com/amanmibra/opensop/internal/pyx"
)

const sp = `[` + pyx.SpaceClass + `]`

var (
	listMarker    = regexp.MustCompile(`^` + sp + `*(?:[-*+]|\p{Nd}+[.)])` + sp + `+`)
	heading       = regexp.MustCompile(`^` + sp + `*#{1,6}` + sp + `+`)
	labelOnly     = regexp.MustCompile(`^[A-Za-z][A-Za-z ]{0,30}:$`)
	opensopPrefix = regexp.MustCompile(`^(?:Goal|When this applies):` + sp + `+`)
	wordRE        = regexp.MustCompile(`[a-z0-9]+(?:'[a-z]+)?`)
	numberRE      = regexp.MustCompile(`[0-9]+(?:[.:][0-9]+)?`)
	negations     = map[string]bool{"not": true, "never": true, "no": true, "don't": true, "dont": true, "doesn't": true, "cannot": true, "can't": true, "won't": true, "without": true, "nothing": true}
)

// MinWords is the shortest unit worth comparing.
const MinWords = 3

func isSentenceStart(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '"' || r == '\'' || r == '('
}

// splitSentences is re.split(r"(?<=[.!?])\s+(?=[A-Z0-9\"'(])", line).
func splitSentences(line string) []string {
	var out []string
	start := 0
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		if !pyx.IsSpace(r) || i == 0 {
			i += size
			continue
		}
		prev, _ := utf8.DecodeLastRuneInString(line[:i])
		if prev != '.' && prev != '!' && prev != '?' {
			i += size
			continue
		}
		j := i
		for j < len(line) {
			r2, s2 := utf8.DecodeRuneInString(line[j:])
			if !pyx.IsSpace(r2) {
				break
			}
			j += s2
		}
		if j < len(line) {
			next, _ := utf8.DecodeRuneInString(line[j:])
			if isSentenceStart(next) {
				out = append(out, line[start:i])
				start = j
			}
		}
		i = j
	}
	return append(out, line[start:])
}

// Units splits prompt text into sentences, list items and lines; headings and bare labels are dropped.
func Units(text string) []string {
	var out []string
	for _, line := range pyx.SplitLines(text, false) {
		line = pyx.Strip(line)
		if line == "" || heading.MatchString(line) || labelOnly.MatchString(line) {
			continue
		}
		line = listMarker.ReplaceAllString(line, "")
		line = opensopPrefix.ReplaceAllString(line, "")
		for _, s := range splitSentences(line) {
			if s = pyx.Strip(s); s != "" {
				out = append(out, s)
			}
		}
	}
	var kept []string
	for _, u := range out {
		if len(Words(u)) >= MinWords {
			kept = append(kept, u)
		}
	}
	return kept
}

// Words are the lowercased words of text.
func Words(text string) []string {
	return wordRE.FindAllString(strings.ReplaceAll(pyx.Lower(text), "’", "'"), -1)
}

// Norm is the words joined by spaces.
func Norm(text string) string { return strings.Join(Words(text), " ") }

// Similarity is SequenceMatcher(None, words(a), words(b), autojunk=False).ratio().
func Similarity(a, b string) float64 {
	return difflib.New(Words(a), Words(b), false).Ratio()
}

func trimPunct(s string) string { return strings.Trim(s, ".,;:!?\"'") }

// DifferingWords is the words that differ between two near-identical sentences.
func DifferingWords(a, b string) (string, string) {
	wa, wb := pyx.Fields(a), pyx.Fields(b)
	key := func(ws []string) []string {
		out := make([]string, len(ws))
		for i, w := range ws {
			out[i] = pyx.Lower(trimPunct(w))
		}
		return out
	}
	sm := difflib.New(key(wa), key(wb), false)
	var da, db []string
	for _, op := range sm.OpCodes() {
		if op.Tag != "equal" {
			da = append(da, wa[op.I1:op.I2]...)
			db = append(db, wb[op.J1:op.J2]...)
		}
	}
	return strings.Trim(strings.Join(da, " "), ".,;:!?"), strings.Trim(strings.Join(db, " "), ".,;:!?")
}

// --- overlap ---------------------------------------------------------------------------------

// SharedText is a sentence several prompts share.
type SharedText struct {
	Agents []string
	Text   string
}

// NearCopy is the same sentence with a different value in some prompts.
type NearCopy struct {
	Variants  pyx.Obj // agent → its version (string values), sorted by agent
	Differing pyx.Obj // agent → just the words that differ
}

// Overlap is what a set of prompts shares.
type Overlap struct {
	Agents     []string
	Shared     []SharedText
	NearCopies []NearCopy
	units      map[string][]string
}

// Text is the overlap report.
func (o *Overlap) Text() string {
	out := []string{fmt.Sprintf("%d prompts: %s", len(o.Agents), strings.Join(o.Agents, ", ")), ""}
	type group struct {
		agents []string
		texts  []string
	}
	var groups []*group
	idx := map[string]*group{}
	for _, s := range o.Shared {
		k := strings.Join(s.Agents, "\x00")
		g, ok := idx[k]
		if !ok {
			g = &group{agents: s.Agents}
			idx[k] = g
			groups = append(groups, g)
		}
		g.texts = append(g.texts, s.Text)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i].agents, groups[j].agents
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return lessStrings(a, b)
	})
	for _, g := range groups {
		label := strings.Join(g.agents, ", ")
		if len(g.agents) == len(o.Agents) {
			label = "ALL agents"
		}
		out = append(out, fmt.Sprintf("Shared by %s (%d sentences):", label, len(g.texts)))
		for _, t := range g.texts {
			out = append(out, "  - "+t)
		}
		out = append(out, "")
	}
	if len(o.NearCopies) > 0 {
		out = append(out, "Near-copies (same sentence, different value; use a {{placeholder}}):")
		for _, nc := range o.NearCopies {
			out = append(out, "  - "+nc.Variants[0].V.(string))
			for _, kv := range nc.Differing {
				out = append(out, "      "+kv.K+": "+pyx.ReprStr(kv.V.(string)))
			}
		}
		out = append(out, "")
	}
	covered := map[[2]string]bool{}
	for _, s := range o.Shared {
		for _, a := range s.Agents {
			covered[[2]string{a, Norm(s.Text)}] = true
		}
	}
	for _, nc := range o.NearCopies {
		for _, kv := range nc.Variants {
			covered[[2]string{kv.K, Norm(kv.V.(string))}] = true
		}
	}
	var parts []string
	for _, a := range o.Agents {
		n := 0
		for _, u := range o.units[a] {
			if !covered[[2]string{a, Norm(u)}] {
				n++
			}
		}
		parts = append(parts, fmt.Sprintf("%s %d", a, n))
	}
	out = append(out, "Only in one prompt (sentences): "+strings.Join(parts, ", "))
	return strings.Join(out, "\n") + "\n"
}

func lessStrings(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// ordered map: norm → (agent → sentence), in insertion order
type exactEntry struct {
	key    string
	agents pyx.Obj // agent → sentence (insertion order)
}

func objKeys(o pyx.Obj) []string {
	out := make([]string, len(o))
	for i, kv := range o {
		out[i] = kv.K
	}
	return out
}

func objSet(o pyx.Obj, k string, v any) pyx.Obj {
	for i, kv := range o {
		if kv.K == k {
			o[i].V = v
			return o
		}
	}
	return append(o, pyx.KV{K: k, V: v})
}

func sortObj(o pyx.Obj) pyx.Obj {
	out := append(pyx.Obj(nil), o...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].K < out[j].K })
	return out
}

// ComputeOverlap finds what prompts share (Python's overlap(prompts, near=0.75)).
func ComputeOverlap(prompts map[string]string, near float64) *Overlap {
	agents := make([]string, 0, len(prompts))
	for a := range prompts {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	perAgent := map[string][]string{}
	for _, a := range agents {
		perAgent[a] = dedupe(Units(prompts[a]))
	}

	var exact []*exactEntry
	exactIdx := map[string]*exactEntry{}
	for _, a := range agents {
		for _, u := range perAgent[a] {
			k := Norm(u)
			e, ok := exactIdx[k]
			if !ok {
				e = &exactEntry{key: k}
				exactIdx[k] = e
				exact = append(exact, e)
			}
			e.agents = objSet(e.agents, a, u)
		}
	}
	var shared []SharedText
	for _, e := range exact {
		if len(e.agents) > 1 {
			keys := objKeys(e.agents)
			sort.Strings(keys)
			shared = append(shared, SharedText{Agents: keys, Text: e.agents[0].V.(string)})
		}
	}

	var singles []*exactEntry
	for _, e := range exact {
		if len(e.agents) < len(agents) {
			singles = append(singles, e)
		}
	}
	used := map[string]bool{}
	var nearCopies []NearCopy
	for i, e := range singles {
		if used[e.key] {
			continue
		}
		variants := append(pyx.Obj(nil), e.agents...)
		for _, other := range singles[i+1:] {
			if used[other.key] || overlaps(objKeys(other.agents), objKeys(variants)) {
				continue
			}
			if Similarity(e.key, other.key) >= near {
				for _, kv := range other.agents {
					variants = objSet(variants, kv.K, kv.V)
				}
				used[other.key] = true
			}
		}
		norms := map[string]bool{}
		for _, kv := range variants {
			norms[Norm(kv.V.(string))] = true
		}
		if len(variants) > 1 && len(norms) > 1 {
			used[e.key] = true
			differing := pyx.Obj{}
			for _, kv := range variants {
				_, d := DifferingWords(otherVariant(variants, kv.K), kv.V.(string))
				differing = append(differing, pyx.KV{K: kv.K, V: d})
			}
			nearCopies = append(nearCopies, NearCopy{Variants: sortObj(variants), Differing: sortObj(differing)})
		}
	}
	var keptShared []SharedText
	for _, s := range shared {
		if !used[Norm(s.Text)] {
			keptShared = append(keptShared, s)
		}
	}
	return &Overlap{Agents: agents, Shared: keptShared, NearCopies: nearCopies, units: perAgent}
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func otherVariant(variants pyx.Obj, agent string) string {
	for _, kv := range variants {
		if kv.K != agent {
			return kv.V.(string)
		}
	}
	return ""
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range items {
		n := Norm(u)
		if !seen[n] {
			seen[n] = true
			out = append(out, u)
		}
	}
	return out
}

// --- compare ---------------------------------------------------------------------------------

// Pair is (original, rendered).
type Pair struct{ Original, Rendered string }

// Comparison is how one rendered prompt covers its original.
type Comparison struct {
	Agent    string
	Total    int
	Missing  []string
	Changed  []Pair
	Reworded []string
	Added    []string
}

// Coverage is the share of original sentences kept.
func (c *Comparison) Coverage() float64 {
	if c.Total == 0 {
		return 1.0
	}
	return float64(c.Total-len(c.Missing)-len(c.Changed)) / float64(c.Total)
}

// OK reports whether nothing was lost or changed.
func (c *Comparison) OK() bool { return len(c.Missing) == 0 && len(c.Changed) == 0 }

var generated = regexp.MustCompile("^(?:Use the `[^`]+` tool\\.|This applies to the `[^`]+` tool\\.|Before following this procedure, call the `get_sop` tool.*)$")

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields("a an and are as at be by for from has have if in is it its of on or so that the their them they this to was were when with you your") {
		m[w] = true
	}
	return m
}()

// Compare checks each rendered prompt still says everything its original said.
func Compare(build *core.Build, originals map[string]string, threshold float64) []*Comparison {
	agents := make([]string, 0, len(originals))
	for a := range originals {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	var results []*Comparison
	for _, agent := range agents {
		var renderedUnits []string
		if r, ok := build.Agents[agent]; ok {
			renderedUnits = Units(r.Prompt)
		}
		originalUnits := Units(originals[agent])
		c := &Comparison{Agent: agent, Total: len(originalUnits)}
		for _, u := range originalUnits {
			if found(u, renderedUnits, threshold) {
				continue
			}
			closest, best := "", -1.0
			for _, p := range renderedUnits {
				if s := Similarity(u, p); s > best {
					closest, best = p, s
				}
			}
			switch {
			case splitAcross(u, renderedUnits):
				c.Reworded = append(c.Reworded, u)
			case closest != "" && Similarity(u, closest) >= 0.5:
				c.Changed = append(c.Changed, Pair{u, closest})
			default:
				c.Missing = append(c.Missing, u)
			}
		}
		origWords := toSet(Words(originals[agent]))
		for _, u := range renderedUnits {
			if !generated.MatchString(u) && !found(u, originalUnits, threshold) && wordCoverage(u, origWords) < 0.9 {
				c.Added = append(c.Added, u)
			}
		}
		results = append(results, c)
	}
	return results
}

func toSet(words []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range words {
		m[w] = true
	}
	return m
}

func found(unit string, pool []string, threshold float64) bool {
	n := Norm(unit)
	for _, p := range pool {
		np := Norm(p)
		if n == np || strings.Contains(np, n) || Similarity(unit, p) >= threshold {
			return true
		}
	}
	return false
}

func splitAcross(unit string, pool []string) bool {
	unitWords := toSet(Words(unit))
	var parts []string
	for _, p := range pool {
		if !generated.MatchString(p) && wordCoverage(p, unitWords) >= 0.6 {
			parts = append(parts, p)
		}
	}
	if len(parts) < 2 {
		return false
	}
	union := map[string]bool{}
	for _, p := range parts {
		for _, w := range Words(p) {
			union[w] = true
		}
	}
	return wordCoverage(unit, union) >= 0.7
}

func wordCoverage(unit string, pool map[string]bool) float64 {
	var content []string
	for _, w := range Words(unit) {
		if !stopwords[w] {
			content = append(content, w)
		}
	}
	if len(content) == 0 {
		return 1.0
	}
	n := 0
	for _, w := range content {
		if pool[w] {
			n++
		}
	}
	return float64(n) / float64(len(content))
}

// CompareText is the compare report.
func CompareText(results []*Comparison, build *core.Build) string {
	var out []string
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Agent] = true
		status := "ok"
		if !r.OK() {
			status = "LOST OR CHANGED TEXT"
		}
		out = append(out, fmt.Sprintf("%s: %.0f%% of %d sentences kept (%s)", r.Agent, r.Coverage()*100, r.Total, status))
		for _, u := range r.Missing {
			out = append(out, "  - missing:  "+u)
		}
		for _, p := range r.Changed {
			was, now := DifferingWords(p.Original, p.Rendered)
			out = append(out, "  ! changed:  "+p.Original)
			out = append(out, "              now: "+p.Rendered+"   ("+pyx.ReprStr(was)+" → "+pyx.ReprStr(now)+")")
		}
		for _, u := range r.Reworded {
			out = append(out, "  ~ reworded: "+u)
		}
		for _, u := range r.Added {
			out = append(out, "  + added:    "+u)
		}
	}
	var unmatched []string
	for _, id := range build.IDs() {
		if !seen[id] {
			unmatched = append(unmatched, id)
		}
	}
	if len(unmatched) > 0 {
		out = append(out, "no original for: "+strings.Join(unmatched, ", "))
	}
	return strings.Join(out, "\n") + "\n"
}

// --- check -----------------------------------------------------------------------------------

// Source is (block label, text).
type Source struct{ Block, Text string }

// Finding is a duplicate or mechanical conflict.
type Finding struct {
	Code    string
	Message string
	Sources []Source
	Agents  []string
}

// ToJSON is Finding.to_dict().
func (f *Finding) ToJSON() pyx.Obj {
	sources := []any{}
	for _, s := range f.Sources {
		sources = append(sources, pyx.Obj{{K: "block", V: s.Block}, {K: "text", V: s.Text}})
	}
	return pyx.Obj{{K: "code", V: f.Code}, {K: "message", V: f.Message}, {K: "sources", V: sources}, {K: "agents", V: f.Agents}}
}

var messages = map[string]string{
	"duplicate_text":    "Same sentence appears twice in the prompt",
	"numeric_conflict":  "Same sentence with different numbers",
	"negation_conflict": "One block says it, another says the opposite",
	"near_duplicate":    "Nearly identical sentences; a copy that drifted?",
}

// Check finds duplicated text and mechanical conflicts within each agent's prompt.
func Check(ws *core.Workspace) []*Finding {
	var order []string
	found := map[string]*Finding{}
	ids := make([]string, 0, len(ws.Agents))
	for id := range ws.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		agent := ws.Agents[id]
		values := model.Merge(ws.Config.Variables, agent.Variables)
		var sourced []Source
		for _, s := range agentUnits(ws, agent, false) {
			sourced = append(sourced, Source{s.Block, core.FillVariables(s.Text, values)})
		}
		for i, a := range sourced {
			for _, b := range sourced[i+1:] {
				code := conflict(a.Text, b.Text)
				if code == "" || (code == "near_duplicate" && a.Block == b.Block) {
					continue
				}
				key := strings.Join([]string{code, a.Block, Norm(a.Text), b.Block, Norm(b.Text)}, "\x00")
				f, ok := found[key]
				if !ok {
					f = &Finding{Code: code, Message: messages[code], Sources: []Source{a, b}, Agents: []string{}}
					found[key] = f
					order = append(order, key)
				}
				f.Agents = append(f.Agents, agent.ID)
			}
		}
		used := map[string]bool{}
		for _, s := range agentUnits(ws, agent, true) {
			for v := range core.FindVariables(s.Text) {
				used[v] = true
			}
		}
		var unused []string
		for _, name := range agent.Variables.Keys {
			if !used[name] {
				unused = append(unused, name)
			}
		}
		sort.Strings(unused)
		for _, name := range unused {
			key := "unused_variable\x00" + agent.ID + "\x00" + name
			if _, ok := found[key]; !ok {
				order = append(order, key)
			}
			found[key] = &Finding{
				Code:    "unused_variable",
				Message: "'" + name + "' is set but no block this agent uses mentions {{" + name + "}}",
				Sources: []Source{{"agent `" + agent.ID + "`", name}},
				Agents:  []string{agent.ID},
			}
		}
	}
	out := make([]*Finding, len(order))
	for i, k := range order {
		out[i] = found[k]
	}
	return out
}

func conflict(a, b string) string {
	na, nb := Norm(a), Norm(b)
	if na == nb {
		return "duplicate_text"
	}
	if numbersDifferInSameSentence(na, nb) {
		return "numeric_conflict"
	}
	wa, wb := Words(a), Words(b)
	negA, negB := anyNegation(wa), anyNegation(wb)
	strippedA, strippedB := stripNegations(wa), stripNegations(wb)
	if negA != negB && len(strippedA) > 0 && difflib.New(strippedA, strippedB, false).Ratio() >= 0.9 {
		return "negation_conflict"
	}
	if Similarity(a, b) >= 0.85 {
		return "near_duplicate"
	}
	return ""
}

func anyNegation(ws []string) bool {
	for _, w := range ws {
		if negations[w] {
			return true
		}
	}
	return false
}

func stripNegations(ws []string) []string {
	var out []string
	for _, w := range ws {
		if !negations[w] && w != "always" {
			out = append(out, w)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func numbersDifferInSameSentence(na, nb string) bool {
	numsA, numsB := numberRE.FindAllString(na, -1), numberRE.FindAllString(nb, -1)
	if len(numsA) == 0 || len(numsB) == 0 || equalStrings(numsA, numsB) {
		return false
	}
	ma, mb := numberRE.ReplaceAllString(na, "#"), numberRE.ReplaceAllString(nb, "#")
	short, long := ma, mb
	if utf8.RuneCountInString(mb) < utf8.RuneCountInString(ma) {
		short, long = mb, ma
	}
	if ma == mb || (len(pyx.Fields(short)) >= 3 && strings.Contains(" "+long+" ", " "+short+" ")) {
		return true
	}
	return difflib.New(pyx.Fields(ma), pyx.Fields(mb), false).Ratio() >= 0.8
}

func agentUnits(ws *core.Workspace, agent *model.Agent, raw bool) []Source {
	split := Units
	if raw {
		split = func(t string) []string { return []string{t} }
	}
	var out []Source
	for _, b := range core.ResolveBases(ws, agent) {
		for _, u := range split(b.Text) {
			out = append(out, Source{"base `" + b.ID + "`", u})
		}
	}
	for _, u := range split(agent.Instructions) {
		out = append(out, Source{"agent `" + agent.ID + "`", u})
	}
	for _, s := range core.ResolveSOPs(ws, agent) {
		for _, u := range split(sopText(s)) {
			out = append(out, Source{"SOP `" + s.ID + "`", u})
		}
	}
	return out
}

func sopText(s *model.SOP) string {
	parts := []string{s.Description, s.Scope, s.Guidance}
	for _, st := range s.ProcedureSteps {
		parts = append(parts, core.RenderStep(st, "step"))
	}
	for _, st := range s.ForbiddenActions {
		parts = append(parts, core.RenderStep(st, "forbidden"))
	}
	for _, st := range s.WarningSigns {
		parts = append(parts, core.RenderStep(st, "warning"))
	}
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}

// CheckText is the check report.
func CheckText(findings []*Finding) string {
	if len(findings) == 0 {
		return "No duplicates or mechanical conflicts found.\n"
	}
	out := []string{fmt.Sprintf("%d finding(s). These are advisory; decide which text is right.", len(findings)), ""}
	for _, f := range findings {
		out = append(out, "["+f.Code+"] "+f.Message+" (agents: "+strings.Join(f.Agents, ", ")+")")
		for _, s := range f.Sources {
			out = append(out, "    "+s.Block+": "+s.Text)
		}
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}
