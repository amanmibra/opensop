package cli

// Ports of the CLI tests in tests/test_plan_server_cli.py, tests/test_affected.py and
// tests/test_analyze.py, plus argument-parsing checks.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	opensop "github.com/amanmibra/opensop"
	tu "github.com/amanmibra/opensop/internal/testutil"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Main(args, &out, &errb)
	return code, out.String(), errb.String()
}

func mustRun(t *testing.T, want int, args ...string) (string, string) {
	t.Helper()
	code, out, errs := run(args...)
	if code != want {
		t.Fatalf("opensop %v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
	}
	return out, errs
}

func TestCLIRenderCheckAndPlan(t *testing.T) {
	repo := tu.Repo(t)
	mustRun(t, 0, "render", repo)
	mustRun(t, 0, "render", repo, "--check")
	tu.Edit(t, filepath.Join(repo, "bases", "closing.md"), "repeat the order total", "repeat the order and total")
	mustRun(t, 1, "render", repo, "--check")
	out, _ := mustRun(t, 0, "plan", repo, "--summary")
	if !strings.Contains(out, "base `closing` edited → 3 agents") {
		t.Fatal(out)
	}
}

func TestCLIPlanAgainstGitRef(t *testing.T) {
	root := tu.GitRepo(t)
	tu.Edit(t, filepath.Join(root, "sops", "procedures", "allergen-check.yaml"), "Name the specific allergen", "Repeat the specific allergen")
	out, _ := mustRun(t, 0, "plan", filepath.Join(root, "sops"), "--against", "main", "--summary")
	if !strings.Contains(out, "SOP `allergen-check` edited → 3 agents") {
		t.Fatal(out)
	}
	_, errs := mustRun(t, 1, "plan", filepath.Join(root, "sops"), "--against", "nope")
	if !strings.HasPrefix(errs, "git ls-tree -r --name-only nope -- sops/: ") {
		t.Fatal(errs)
	}
}

func TestCLIValidateReportsErrors(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, filepath.Join(repo, "agents", "tonys-pizza.yaml"), "inherits: [pizza-context]", "inherits: [nope]")
	_, errs := mustRun(t, 1, "validate", repo)
	if !strings.Contains(errs, "unknown_base") {
		t.Fatal(errs)
	}
}

func TestExampleIsValidAndItsBuildIsCurrent(t *testing.T) {
	out, errs := mustRun(t, 0, "validate", tu.Example())
	if out != "0 error(s), 0 warning(s)\n" || errs != "" {
		t.Fatal(out, errs)
	}
	mustRun(t, 0, "render", tu.Example(), "--check")
}

func TestGuidePrintsTheFormatReference(t *testing.T) {
	out, _ := mustRun(t, 0, "guide")
	if !strings.HasPrefix(out, "# The OpenSOP format") || !strings.Contains(out, "Checklist for coding agents") {
		t.Fatal("guide output")
	}
}

// The embedded copies must be the files at the repo root (go:embed reads them directly;
// this guards against someone replacing the embed with a stale copy).
func TestEmbeddedFilesMatchTheRepo(t *testing.T) {
	if opensop.FormatMD != tu.Read(t, filepath.Join(tu.RepoRoot(), "FORMAT.md")) {
		t.Fatal("embedded FORMAT.md is stale")
	}
	data, err := opensop.Skills.ReadFile("skills/opensop-import/SKILL.md")
	if err != nil || string(data) != tu.Read(t, filepath.Join(tu.RepoRoot(), "skills", "opensop-import", "SKILL.md")) {
		t.Fatal("embedded SKILL.md is stale")
	}
}

func TestFormatReferenceListsEveryValidationCode(t *testing.T) {
	re := regexp.MustCompile(`Code: "([a-z_]+)"`)
	guide := tu.Read(t, filepath.Join(tu.RepoRoot(), "FORMAT.md"))
	for _, file := range []string{"validate.go", "loader.go"} {
		src := tu.Read(t, filepath.Join(tu.RepoRoot(), "internal", "core", file))
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if !strings.Contains(guide, "`"+m[1]+"`") {
				t.Errorf("FORMAT.md doesn't document `%s`", m[1])
			}
		}
	}
}

func TestCLIPlanJSONAndAgentsJSON(t *testing.T) {
	repo := tu.Repo(t)
	mustRun(t, 0, "render", repo)
	tu.Edit(t, filepath.Join(repo, "procedures", "reservations.yaml"), "Never double-book a table", "Never double-book or overbook a table")
	out, _ := mustRun(t, 0, "plan", repo, "--json")
	var plan struct {
		Changes []struct {
			Agent       string `json:"agent"`
			PlatformRef string `json:"platform_ref"`
			Status      string `json:"status"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range plan.Changes {
		got = append(got, c.Agent+" "+c.PlatformRef+" "+c.Status)
	}
	if strings.Join(got, ";") != "luigis-trattoria livekit:luigis-trattoria changed;sakura-sushi livekit:sakura-sushi changed" {
		t.Fatal(got)
	}
	out, _ = mustRun(t, 0, "agents", repo, "--json")
	var agents []struct {
		ID         string   `json:"id"`
		PlatformID string   `json:"platform_id"`
		SOPs       []string `json:"sops"`
		Tools      []string `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &agents); err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, a := range agents {
		byID[a.ID] = i
	}
	sakura := agents[byID["sakura-sushi"]]
	if sakura.PlatformID != "sakura-sushi" || strings.Join(sakura.SOPs, ",") != "allergen-check,reservations" {
		t.Fatal(sakura)
	}
	if !strings.Contains(strings.Join(agents[byID["tonys-pizza"]].Tools, ","), "transfer_to_staff") {
		t.Fatal("tonys-pizza tools")
	}
}

func TestCLIAgainstGitRefWritesGitHubOutputs(t *testing.T) {
	root := tu.GitRepo(t)
	tu.Edit(t, filepath.Join(root, "sops", "procedures", "allergen-check.yaml"), "Name the specific allergen", "Repeat the specific allergen")
	dir := t.TempDir()
	outPath, summaryPath := filepath.Join(dir, "out"), filepath.Join(dir, "summary")
	t.Setenv("GITHUB_OUTPUT", outPath)
	t.Setenv("GITHUB_STEP_SUMMARY", summaryPath)
	out, _ := mustRun(t, 0, "affected", filepath.Join(root, "sops"), "--against", "main", "--all-if-none", "--ci")
	if strings.Join(strings.Fields(out), " ") != "luigis-trattoria sakura-sushi tonys-pizza" {
		t.Fatal(out)
	}
	outputs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(tu.Read(t, outPath)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		outputs[k] = v
	}
	if outputs["ids"] != "luigis-trattoria sakura-sushi tonys-pizza" || outputs["count"] != "3" || outputs["all"] != "false" {
		t.Fatal(outputs)
	}
	var matrix []struct {
		ChangedSOPs []string `json:"changed_sops"`
	}
	if err := json.Unmarshal([]byte(outputs["matrix"]), &matrix); err != nil || strings.Join(matrix[0].ChangedSOPs, ",") != "allergen-check" {
		t.Fatal(outputs["matrix"], err)
	}
	if !strings.Contains(tu.Read(t, summaryPath), "`tonys-pizza` (livekit `tonys-pizza`): sop:allergen-check") {
		t.Fatal(tu.Read(t, summaryPath))
	}
}

func TestCLIJSONFormat(t *testing.T) {
	out, _ := mustRun(t, 0, "affected", tu.Repo(t), "--agents", "tonys-pizza", "--format", "json")
	var data struct {
		Count  int
		Agents []struct {
			Reason string
			SOPs   []string `json:"sops"`
		}
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatal(err)
	}
	if data.Count != 1 || data.Agents[0].Reason != "requested" || strings.Join(data.Agents[0].SOPs, ",") != "allergen-check,delivery-handling,large-orders" {
		t.Fatal(out)
	}
}

func TestCLICompareExitCode(t *testing.T) {
	out, _ := mustRun(t, 1, "compare", filepath.Join(tu.Fixture(), "sops"), "--originals", filepath.Join(tu.Fixture(), "originals"))
	if !strings.Contains(out, "('twice' → 'once')") {
		t.Fatal(out)
	}
}

func TestSkillsInstallCoversClaudeCodeCodexAndOpenCode(t *testing.T) {
	dir := t.TempDir()
	tu.Chdir(t, dir)
	out, _ := mustRun(t, 0, "skills", "install")
	for _, folder := range []string{".claude/skills", ".agents/skills"} {
		skill := tu.Read(t, filepath.Join(dir, folder, "opensop-import", "SKILL.md"))
		if !strings.HasPrefix(skill, "---\nname: opensop-import\n") {
			t.Fatal("skill front matter")
		}
		for _, cmd := range []string{"opensop overlap", "opensop compare", "opensop check", "opensop guide"} {
			if !strings.Contains(skill, cmd) {
				t.Errorf("skill doesn't mention %s", cmd)
			}
		}
	}
	for _, s := range []string{"/opensop-import", "$opensop-import", "OpenCode"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q", s)
		}
	}
}

func TestSkillsInstallForOneAgent(t *testing.T) {
	dir := t.TempDir()
	tu.Chdir(t, dir)
	mustRun(t, 0, "skills", "install", "--agent", "codex")
	if _, err := os.Stat(filepath.Join(dir, ".agents/skills/opensop-import/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Fatal(".claude should not exist")
	}
	mustRun(t, 0, "skills", "install", "--dir", "custom")
	if _, err := os.Stat(filepath.Join(dir, "custom/opensop-import/SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestArgumentErrors(t *testing.T) {
	cases := []struct {
		args []string
		code int
		err  string
	}{
		{nil, 2, "opensop: error: the following arguments are required: command\n"},
		{[]string{"bogus"}, 2, "opensop: error: argument command: invalid choice: 'bogus' (choose from 'validate', 'render', 'plan', 'affected', 'agents', 'overlap', 'compare', 'check', 'skills', 'guide')\n"},
		{[]string{"render", "--foo"}, 2, "opensop: error: unrecognized arguments: --foo\n"},
		{[]string{"affected", "--format", "xx"}, 2, "opensop affected: error: argument --format: invalid choice: 'xx' (choose from 'ids', 'platform-ids', 'json')\n"},
		{[]string{"affected", "--a", "x"}, 2, "opensop affected: error: ambiguous option: --a could match --against, --agents, --all-if-none\n"},
		{[]string{"compare"}, 2, "opensop compare: error: the following arguments are required: --originals\n"},
		{[]string{"render", "--out"}, 2, "opensop render: error: argument --out: expected one argument\n"},
		{[]string{"skills", "foo"}, 2, "opensop skills: error: argument action: invalid choice: 'foo' (choose from 'install')\n"},
	}
	for _, c := range cases {
		code, _, errs := run(c.args...)
		if code != c.code || !strings.HasSuffix(errs, c.err) || !strings.HasPrefix(errs, "usage: opensop") {
			t.Errorf("opensop %v: exit %d, stderr %q", c.args, code, errs)
		}
	}
	code, out, _ := run("render", "-h")
	if code != 0 || !strings.HasPrefix(out, "usage: opensop render [-h] [--out OUT] [--check] [root]\n") {
		t.Errorf("render -h: %d %q", code, out)
	}
}
