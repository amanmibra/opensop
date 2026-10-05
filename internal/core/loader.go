// Package core reads, validates, renders and compares OpenSOP folders. It mirrors the
// Python modules loader.py, validate.py, render.py, build.py, plan.py and affected.py.
package core

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amanmibra/opensop/internal/model"
	"github.com/amanmibra/opensop/internal/pyx"
	y "github.com/amanmibra/opensop/internal/yamlpy"
)

// Issue is a validation error or warning.
type Issue struct {
	Code     string
	Message  string
	Path     string
	Severity string // "error" | "warning"
}

func (i Issue) String() string {
	where := ""
	if i.Path != "" {
		where = i.Path + ": "
	}
	return where + i.severity() + " [" + i.Code + "] " + i.Message
}

func (i Issue) severity() string {
	if i.Severity == "" {
		return "error"
	}
	return i.Severity
}

// IsError reports whether the issue is an error (not a warning).
func (i Issue) IsError() bool { return i.severity() == "error" }

// Error carries issues that stop a command (Python's OpenSOPError).
type Error struct{ Issues []Issue }

func (e *Error) Error() string {
	lines := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		lines[i] = issue.String()
	}
	return strings.Join(lines, "\n")
}

// Workspace is a loaded folder. The *Order slices keep file (path) order, which is the
// iteration order of the Python version's dicts.
type Workspace struct {
	model.Workspace
	BaseOrder  []string
	SOPOrder   []string
	AgentOrder []string
}

var sourceGlobs = []string{"opensop.yaml", "bases/*.md", "procedures/*.yaml", "agents/*.yaml"}

// ReadFiles returns the source files of a folder, keyed by path relative to root.
func ReadFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	for _, pattern := range sourceGlobs {
		matches, _ := filepath.Glob(filepath.Join(globEscape(root), pattern))
		sort.Strings(matches)
		for _, m := range matches {
			if info, err := os.Stat(m); err != nil || info.IsDir() {
				continue
			}
			text, err := pyx.ReadText(m)
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			files[filepath.ToSlash(rel)] = text
		}
	}
	return files, nil
}

func globEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)
	return r.Replace(s)
}

// LoadWorkspace reads and parses a folder.
func LoadWorkspace(root string) (*Workspace, error) {
	files, err := ReadFiles(root)
	if err != nil {
		return nil, err
	}
	return LoadWorkspaceFiles(files)
}

func matching(files map[string]string, folder, suffix string) []string {
	var out []string
	for p := range files {
		if path.Dir(p) == folder && strings.HasSuffix(p, suffix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// stem is PurePosixPath(p).stem.
func stem(p string) string {
	name := path.Base(p)
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[:i]
	}
	return name
}

// LoadWorkspaceFiles parses an in-memory file map (relative path → text).
func LoadWorkspaceFiles(files map[string]string) (*Workspace, error) {
	var issues []Issue
	text, ok := files["opensop.yaml"]
	if !ok {
		return nil, &Error{[]Issue{{Code: "missing_config", Message: "opensop.yaml not found"}}}
	}
	ws := &Workspace{Workspace: model.Workspace{
		Bases: map[string]*model.Base{}, SOPs: map[string]*model.SOP{}, Agents: map[string]*model.Agent{},
	}}
	if data := loadYAML(text, "opensop.yaml", &issues); data != nil {
		if cfg, errs := model.ParseConfig(data); errs != nil {
			addFieldErrors(errs, "opensop.yaml", &issues)
		} else {
			ws.Config = *cfg
		}
	}

	for _, p := range matching(files, "bases", ".md") {
		meta, body := splitFrontMatter(files[p])
		data := loadYAML(meta, p, &issues)
		if data == nil {
			continue
		}
		data = withID(data, p, &issues)
		data.SetStr("text", &y.Value{Kind: y.KStr, Str: pyx.Strip(body)})
		if b, errs := model.ParseBase(data); errs != nil {
			addFieldErrors(errs, p, &issues)
		} else {
			if _, dup := ws.Bases[b.ID]; !dup {
				ws.BaseOrder = append(ws.BaseOrder, b.ID)
			}
			ws.Bases[b.ID] = b
		}
	}

	for _, p := range matching(files, "procedures", ".yaml") {
		data := loadYAML(files[p], p, &issues)
		if data == nil {
			continue
		}
		data = withID(data, p, &issues)
		if !checkSteps(data, p, &issues) {
			continue
		}
		if s, errs := model.ParseSOP(data); errs != nil {
			addFieldErrors(errs, p, &issues)
		} else {
			if _, dup := ws.SOPs[s.ID]; !dup {
				ws.SOPOrder = append(ws.SOPOrder, s.ID)
			}
			ws.SOPs[s.ID] = s
		}
	}

	for _, p := range matching(files, "agents", ".yaml") {
		data := loadYAML(files[p], p, &issues)
		if data == nil {
			continue
		}
		data = withID(data, p, &issues)
		if a, errs := model.ParseAgent(data); errs != nil {
			addFieldErrors(errs, p, &issues)
		} else {
			if _, dup := ws.Agents[a.ID]; !dup {
				ws.AgentOrder = append(ws.AgentOrder, a.ID)
			}
			ws.Agents[a.ID] = a
		}
	}

	if len(issues) > 0 {
		return nil, &Error{issues}
	}
	return ws, nil
}

func addFieldErrors(errs []model.FieldError, p string, issues *[]Issue) {
	for _, e := range errs {
		*issues = append(*issues, Issue{Code: "invalid_field", Message: e.String(), Path: p})
	}
}

func splitFrontMatter(text string) (string, string) {
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[3:], "\n---"); end != -1 {
			end += 3
			meta := ""
			if end > 4 {
				meta = text[4:end]
			}
			return meta, strings.TrimLeft(text[end+4:], "\n")
		}
	}
	return "", text
}

const colonHint = " Usually a colon followed by a space inside unquoted text: put the text in quotes, or use a | block."

// loadYAML parses a YAML mapping, or reports the problem and returns nil.
func loadYAML(text, p string, issues *[]Issue) *y.Dict {
	data, err := y.SafeLoad(text)
	if err != nil {
		msg := strings.Join(strings.Fields(err.Error()), " ")
		if strings.Contains(err.Error(), "mapping values are not allowed") {
			msg += colonHint
		}
		*issues = append(*issues, Issue{Code: "invalid_yaml", Message: msg, Path: p})
		return nil
	}
	if data == nil || !data.Truthy() {
		return &y.Dict{}
	}
	if data.Kind != y.KDict {
		*issues = append(*issues, Issue{Code: "invalid_yaml", Message: "expected a mapping", Path: p})
		return nil
	}
	return data.Dict.Copy()
}

var stepFields = []string{"procedureSteps", "forbiddenActions", "warningSigns"}

// checkSteps catches steps YAML silently turned into something other than text.
func checkSteps(data *y.Dict, p string, issues *[]Issue) bool {
	ok := true
	for _, f := range stepFields {
		items, found := data.Get(f)
		if !found || items.Kind != y.KList {
			continue
		}
		for i, item := range items.List {
			where := f + "[" + itoa(i) + "]"
			switch {
			case item.Kind == y.KDict && item.Dict.Len() == 1 && !stepKeysOnly(item.Dict):
				key, value := item.Dict.Keys[0], item.Dict.Values[0]
				text := key.PyStr() + ":"
				if value.Kind != y.KNull {
					text = key.PyStr() + ": " + value.PyStr()
				}
				*issues = append(*issues, Issue{Code: "colon_in_step", Path: p, Message: where +
					" was read as a key and value because of the colon. Put the whole step in quotes: - " + pyx.QuoteJSON(text, true)})
				ok = false
			case item.Kind == y.KNull:
				*issues = append(*issues, Issue{Code: "empty_step", Message: where + " is empty", Path: p})
				ok = false
			case item.Kind == y.KBool || item.Kind == y.KInt || item.Kind == y.KFloat:
				*issues = append(*issues, Issue{Code: "unquoted_value", Path: p, Message: where + " was read as " + item.Repr() + ", not text. Put the step in quotes."})
				ok = false
			}
		}
	}
	return ok
}

func stepKeysOnly(d *y.Dict) bool {
	for _, k := range d.Keys {
		if k.Kind != y.KStr || (k.Str != "text" && k.Str != "tool" && k.Str != "required") {
			return false
		}
	}
	return true
}

// withID sets "id" from the file name; an explicit id must agree.
func withID(data *y.Dict, p string, issues *[]Issue) *y.Dict {
	s := stem(p)
	if v, ok := data.Get("id"); ok && !(v.Kind == y.KStr && v.Str == s) {
		*issues = append(*issues, Issue{Code: "id_mismatch", Message: "id '" + v.PyStr() + "' does not match file name '" + s + "'", Path: p})
	}
	data.SetStr("id", &y.Value{Kind: y.KStr, Str: s})
	return data
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }
