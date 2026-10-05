// Package cli is the opensop command line (Python's cli.py).
package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	opensop "github.com/amanmibra/opensop"
	"github.com/amanmibra/opensop/internal/analyze"
	"github.com/amanmibra/opensop/internal/core"
	"github.com/amanmibra/opensop/internal/pyx"
)

var skillDirs = map[string]string{"claude": ".claude/skills", "codex": ".agents/skills", "opencode": ".agents/skills"}

var howToRun = map[string]string{
	".claude/skills": "Claude Code: /opensop-import",
	".agents/skills": "Codex: $opensop-import   OpenCode: ask it to use the opensop-import skill",
}

func rootPos() posSpec { return posSpec{name: "root", optional: true, def: "."} }

var commands = []*command{
	{name: "validate", pos: []posSpec{rootPos()}},
	{name: "render", pos: []posSpec{rootPos()}, opts: []optSpec{
		{flag: "--out", dest: "out", hasValue: true},
		{flag: "--check", dest: "check"},
	}},
	{name: "plan", pos: []posSpec{rootPos()}, opts: []optSpec{
		{flag: "--against", dest: "against", hasValue: true},
		{flag: "--summary", dest: "summary"},
		{flag: "--json", dest: "json"},
	}},
	{name: "affected", pos: []posSpec{rootPos()}, opts: []optSpec{
		{flag: "--against", dest: "against", hasValue: true},
		{flag: "--agents", dest: "agents", hasValue: true},
		{flag: "--all-if-none", dest: "all_if_none"},
		{flag: "--format", dest: "format", hasValue: true, choices: []string{"ids", "platform-ids", "json"}},
		{flag: "--ci", dest: "ci"},
	}},
	{name: "agents", pos: []posSpec{rootPos()}, opts: []optSpec{{flag: "--json", dest: "json"}}},
	{name: "overlap", pos: []posSpec{{name: "dir"}}},
	{name: "compare", pos: []posSpec{rootPos()}, opts: []optSpec{{flag: "--originals", dest: "originals", hasValue: true, required: true}}},
	{name: "check", pos: []posSpec{rootPos()}, opts: []optSpec{{flag: "--json", dest: "json"}}},
	{name: "skills", pos: []posSpec{{name: "action", choices: []string{"install"}}}, opts: []optSpec{
		{flag: "--agent", dest: "agent", hasValue: true, repeat: true, choices: []string{"claude", "codex", "opencode"}},
		{flag: "--dir", dest: "dir", hasValue: true},
	}},
	{name: "guide"},
}

func init() {
	for _, c := range commands {
		c.help = helpTexts[c.name]
		c.usage = usageTexts[c.name]
	}
}

type env struct {
	stdout, stderr io.Writer
}

func (e *env) out(s string) { io.WriteString(e.stdout, s) }
func (e *env) err(s string) { io.WriteString(e.stderr, s) }

// gitError is a failed git command.
type gitError struct{ msg string }

func (g *gitError) Error() string { return g.msg }

// Main runs the CLI and returns the exit code.
func Main(argv []string, stdout, stderr io.Writer) int {
	cmd, args, perr := parseArgs(argv)
	if perr != nil {
		return writeExit(perr, stdout, stderr)
	}
	e := &env{stdout, stderr}
	handlers := map[string]func(*env, *parsed) (int, error){
		"validate": cmdValidate, "render": cmdRender, "plan": cmdPlan, "guide": cmdGuide,
		"agents": cmdAgents, "affected": cmdAffected, "overlap": cmdOverlap, "compare": cmdCompare,
		"check": cmdCheck, "skills": cmdSkills,
	}
	code, err := handlers[cmd.name](e, args)
	if err != nil {
		var oe *core.Error
		var ge *gitError
		switch {
		case errors.As(err, &oe):
			for _, issue := range oe.Issues {
				e.err(issue.String() + "\n")
			}
		case errors.As(err, &ge):
			e.err(ge.msg + "\n")
		default:
			e.err("opensop: error: " + err.Error() + "\n")
		}
		return 1
	}
	return code
}

func load(root string) (*core.Workspace, error) { return core.LoadWorkspace(root) }

func renderRoot(root string) (*core.Build, error) {
	ws, err := load(root)
	if err != nil {
		return nil, err
	}
	return core.RenderWorkspace(ws)
}

func cmdValidate(e *env, a *parsed) (int, error) {
	ws, err := load(a.str("root"))
	if err != nil {
		return 0, err
	}
	issues := core.Validate(ws)
	errs := 0
	for _, i := range issues {
		e.err(i.String() + "\n")
		if i.IsError() {
			errs++
		}
	}
	e.out(fmt.Sprintf("%d error(s), %d warning(s)\n", errs, len(issues)-errs))
	if errs > 0 {
		return 1, nil
	}
	return 0, nil
}

func cmdRender(e *env, a *parsed) (int, error) {
	root := a.str("root")
	out := pyx.Join(root, "build")
	if a.str("out") != "" {
		out = pyx.Path(a.str("out"))
	}
	build, err := renderRoot(root)
	if err != nil {
		return 0, err
	}
	for _, w := range build.Warnings {
		e.err(w.String() + "\n")
	}
	if a.flag("check") {
		before, err := core.ReadSnapshot(out)
		if err != nil {
			return 0, err
		}
		plan := core.MakePlan(before, core.SnapshotOf(build))
		if plan.Empty() {
			e.out(out + " is up to date\n")
			return 0, nil
		}
		e.err(out + " is out of date; run `opensop render`\n\n")
		e.err(plan.Text(false) + "\n")
		return 1, nil
	}
	written, err := core.WriteBuild(build, out)
	if err != nil {
		return 0, err
	}
	e.out(fmt.Sprintf("wrote %d files to %s\n", len(written), out))
	return 0, nil
}

func cmdPlan(e *env, a *parsed) (int, error) {
	root := a.str("root")
	head, err := renderRoot(root)
	if err != nil {
		return 0, err
	}
	after := core.SnapshotOf(head)
	var before core.Snapshot
	if against := a.str("against"); against != "" {
		files, err := filesAtRef(root, against)
		if err != nil {
			return 0, err
		}
		before = core.Snapshot{}
		if len(files) > 0 {
			ws, err := core.LoadWorkspaceFiles(files)
			if err != nil {
				return 0, err
			}
			b, err := core.RenderWorkspace(ws)
			if err != nil {
				return 0, err
			}
			before = core.SnapshotOf(b)
		}
	} else {
		before, err = core.ReadSnapshot(pyx.Join(root, "build"))
		if err != nil {
			return 0, err
		}
	}
	plan := core.MakePlan(before, after)
	if a.flag("json") {
		e.out(pyx.DumpsIndent2(plan.ToJSON()) + "\n")
	} else {
		e.out(plan.Text(!a.flag("summary")))
	}
	return 0, nil
}

func cmdAffected(e *env, a *parsed) (int, error) {
	root := a.str("root")
	head, err := renderRoot(root)
	if err != nil {
		return 0, err
	}
	var base *core.Build
	if against := a.str("against"); against != "" {
		files, err := filesAtRef(root, against)
		if err != nil {
			return 0, err
		}
		if len(files) > 0 {
			ws, err := core.LoadWorkspaceFiles(files)
			if err != nil {
				return 0, err
			}
			if base, err = core.RenderWorkspace(ws); err != nil {
				return 0, err
			}
		}
		if base == nil {
			e.err("no OpenSOP files at " + against + "; treating every agent as new\n")
		}
	}
	requested := pyx.Fields(strings.ReplaceAll(a.str("agents"), ",", " "))
	result, err := core.ComputeAffected(head, base, requested, a.flag("all_if_none"))
	if err != nil {
		return 0, err
	}
	switch a.str("format") {
	case "json":
		e.out(pyx.DumpsIndent2(result.ToJSON()) + "\n")
	case "platform-ids":
		for _, ag := range result.Agents {
			e.out(ag.PlatformID + "\n")
		}
	default:
		for _, ag := range result.Agents {
			e.out(ag.ID + "\n")
		}
	}
	if a.flag("ci") {
		if err := writeGitHub(e, result); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func appendFile(p, text string) error {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

func writeGitHub(e *env, result *core.Affected) error {
	outputs := result.GitHubOutputs()
	if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
		var b strings.Builder
		for _, kv := range outputs {
			b.WriteString(kv.K + "=" + kv.V.(string) + "\n")
		}
		if err := appendFile(p, b.String()); err != nil {
			return err
		}
	} else {
		e.err("\n# GITHUB_OUTPUT not set; these would be written:\n")
		for _, kv := range outputs {
			e.err("#   " + kv.K + "=" + kv.V.(string) + "\n")
		}
	}
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		return appendFile(p, result.Markdown())
	}
	return nil
}

func cmdAgents(e *env, a *parsed) (int, error) {
	build, err := renderRoot(a.str("root"))
	if err != nil {
		return 0, err
	}
	list := []any{}
	for _, id := range build.IDs() {
		r := build.Agents[id]
		var bases, sops []string
		for _, b := range r.Bases {
			bases = append(bases, b.ID)
		}
		for _, s := range r.SOPs {
			sops = append(sops, s.ID)
		}
		if a.flag("json") {
			list = append(list, pyx.Obj{
				{K: "id", V: id}, {K: "platform_ref", V: r.Agent.PlatformRef()}, {K: "platform", V: r.Agent.Platform()},
				{K: "platform_id", V: r.Agent.PlatformID()}, {K: "bases", V: bases}, {K: "sops", V: sops},
				{K: "tools", V: r.Tools}, {K: "hash", V: r.Hash()},
			})
			continue
		}
		sopText := strings.Join(sops, ", ")
		if sopText == "" {
			sopText = "-"
		}
		e.out(fmt.Sprintf("%-24s %-11s %-28s sops: %s\n", id, r.Agent.Platform(), r.Agent.PlatformID(), sopText))
	}
	if a.flag("json") {
		e.out(pyx.DumpsIndent2(list) + "\n")
	}
	return 0, nil
}

// suffix and stem are PurePath.suffix / .stem.
func suffix(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

func stem(name string) string { return strings.TrimSuffix(name, suffix(name)) }

func readPrompts(folder string) (map[string]string, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range entries {
		s := suffix(ent.Name())
		if s != ".md" && s != ".txt" {
			continue
		}
		if info, err := os.Stat(filepath.Join(folder, ent.Name())); err != nil || !info.Mode().IsRegular() {
			continue
		}
		names = append(names, ent.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, &core.Error{Issues: []core.Issue{{Code: "no_prompts", Message: "no .md or .txt files in " + folder}}}
	}
	out := map[string]string{}
	for _, n := range names {
		text, err := pyx.ReadText(filepath.Join(folder, n))
		if err != nil {
			return nil, err
		}
		out[stem(n)] = text
	}
	return out, nil
}

func cmdOverlap(e *env, a *parsed) (int, error) {
	prompts, err := readPrompts(a.str("dir"))
	if err != nil {
		return 0, err
	}
	e.out(analyze.ComputeOverlap(prompts, 0.75).Text())
	return 0, nil
}

func cmdCompare(e *env, a *parsed) (int, error) {
	build, err := renderRoot(a.str("root"))
	if err != nil {
		return 0, err
	}
	originals, err := readPrompts(a.str("originals"))
	if err != nil {
		return 0, err
	}
	results := analyze.Compare(build, originals, 0.9)
	e.out(analyze.CompareText(results, build))
	for _, r := range results {
		if !r.OK() {
			return 1, nil
		}
	}
	return 0, nil
}

func cmdCheck(e *env, a *parsed) (int, error) {
	ws, err := load(a.str("root"))
	if err != nil {
		return 0, err
	}
	findings := analyze.Check(ws)
	if a.flag("json") {
		list := []any{}
		for _, f := range findings {
			list = append(list, f.ToJSON())
		}
		e.out(pyx.DumpsIndent2(list) + "\n")
	} else {
		e.out(analyze.CheckText(findings))
	}
	return 0, nil
}

func cmdSkills(e *env, a *parsed) (int, error) {
	var dests []string
	if d := a.str("dir"); d != "" {
		dests = []string{d}
	} else {
		agents := a.list("agent")
		if len(agents) == 0 {
			agents = []string{"claude", "codex", "opencode"}
		}
		set := map[string]bool{}
		for _, ag := range agents {
			set[skillDirs[ag]] = true
		}
		for d := range set {
			dests = append(dests, d)
		}
		sort.Strings(dests)
	}
	skills, err := fs.ReadDir(opensop.Skills, "skills")
	if err != nil {
		return 0, err
	}
	var names []string
	for _, s := range skills {
		if s.IsDir() {
			names = append(names, s.Name())
		}
	}
	sort.Strings(names)
	for _, dest := range dests {
		for _, name := range names {
			target := pyx.Join(dest, name)
			if err := copyEmbedded(path.Join("skills", name), target); err != nil {
				return 0, err
			}
			e.out("installed " + target + "/SKILL.md\n")
		}
	}
	e.out("\n")
	for _, dest := range dests {
		if how, ok := howToRun[dest]; ok {
			e.out(how + "\n")
		}
	}
	return 0, nil
}

func copyEmbedded(src, dest string) error {
	return fs.WalkDir(opensop.Skills, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(opensop.Skills, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func cmdGuide(e *env, _ *parsed) (int, error) {
	e.out(opensop.FormatMD)
	return 0, nil
}

// --- git ------------------------------------------------------------------------------------

func runGit(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", &gitError{"git " + strings.Join(args, " ") + ": " + err.Error()}
		}
		return "", &gitError{"git " + strings.Join(args, " ") + ": " + pyx.Strip(pyx.UniversalNewlines(stderr.String()))}
	}
	return pyx.UniversalNewlines(stdout.String()), nil
}

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// filesAtRef returns the OpenSOP source files under root as they were at a git ref.
func filesAtRef(root, ref string) (map[string]string, error) {
	root = resolvePath(root)
	out, err := runGit(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top := pyx.Strip(out)
	rel, err := filepath.Rel(resolvePath(top), root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("%s is not in the subpath of %s", root, top)
	}
	prefix := filepath.ToSlash(rel)
	if prefix == "." {
		prefix = ""
	} else {
		prefix += "/"
	}
	pathArg := prefix
	if pathArg == "" {
		pathArg = "."
	}
	listing, err := runGit(top, "ls-tree", "-r", "--name-only", ref, "--", pathArg)
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, name := range pyx.SplitLines(listing, false) {
		if len(name) < len(prefix) {
			continue
		}
		relName := name[len(prefix):]
		if !isSource(relName) {
			continue
		}
		text, err := runGit(top, "show", ref+":"+name)
		if err != nil {
			return nil, err
		}
		files[relName] = text
	}
	return files, nil
}

func isSource(rel string) bool {
	folder, name := "", rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		folder, name = rel[:i], rel[i+1:]
	}
	return rel == "opensop.yaml" || (folder == "bases" && strings.HasSuffix(name, ".md")) ||
		((folder == "procedures" || folder == "agents") && strings.HasSuffix(name, ".yaml"))
}
