package core_test

// Ports of tests/test_validate.py, tests/test_render.py and the non-CLI parts of
// tests/test_plan_server_cli.py and tests/test_affected.py.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/amanmibra/opensop/internal/core"
	"github.com/amanmibra/opensop/internal/pyx"
	tu "github.com/amanmibra/opensop/internal/testutil"
)

func fixtureSOPs() string { return filepath.Join(tu.Fixture(), "sops") }

func load(t *testing.T, root string) *core.Workspace {
	t.Helper()
	ws, err := core.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func build(t *testing.T, root string) *core.Build {
	t.Helper()
	b, err := core.RenderWorkspace(load(t, root))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func issuesOf(t *testing.T, err error) []core.Issue {
	t.Helper()
	var oe *core.Error
	if !errors.As(err, &oe) {
		t.Fatalf("expected an OpenSOP error, got %v", err)
	}
	return oe.Issues
}

func codesOf(issues []core.Issue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

// renderCodes is codes(repo) in test_validate.py: the issues that stop rendering.
func renderCodes(t *testing.T, root string) []string {
	t.Helper()
	_, err := core.RenderWorkspace(load(t, root))
	return codesOf(issuesOf(t, err))
}

func loadIssues(t *testing.T, root string) []core.Issue {
	t.Helper()
	_, err := core.LoadWorkspace(root)
	return issuesOf(t, err)
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func path(root string, parts ...string) string {
	return filepath.Join(append([]string{root}, parts...)...)
}

// --- test_validate.py --------------------------------------------------------------------

func TestFixtureIsValid(t *testing.T) {
	eq(t, len(core.Validate(load(t, fixtureSOPs()))), 0)
}

func TestLockedBaseCannotBeExcluded(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "agents", "sakura-sushi.yaml"), "exclude: [delivery-handling]", "exclude: [delivery-handling, brand-voice]")
	eq(t, renderCodes(t, repo), []string{"locked"})
}

func TestUnlockedBaseCanBeExcluded(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "agents", "sakura-sushi.yaml"), "exclude: [delivery-handling]", "exclude: [delivery-handling, closing]")
	for _, b := range build(t, repo).Agents["sakura-sushi"].Bases {
		if b.ID == "closing" {
			t.Fatal("closing still applies")
		}
	}
}

func TestInheritanceCycle(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "bases", "restaurant-host.md"), "---\n---", "---\ninherits: [pizza-context]\n---")
	eq(t, renderCodes(t, repo), []string{"inheritance_cycle"})
}

func TestUnknownBase(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "agents", "tonys-pizza.yaml"), "inherits: [pizza-context]", "inherits: [pasta-context]")
	eq(t, renderCodes(t, repo), []string{"unknown_base"})
}

func TestUnknownAgentInTargeting(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "[sakura-sushi, luigis-trattoria]", "[sakura-sushi, luigis]")
	eq(t, renderCodes(t, repo), []string{"unknown_agent"})
}

func TestPlatformRefCanBeUsedInTargeting(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "[sakura-sushi, luigis-trattoria]", `[sakura-sushi, "livekit:tonys-pizza"]`)
	b := build(t, repo)
	if !strings.Contains(b.Agents["tonys-pizza"].Prompt, "Reservations") || strings.Contains(b.Agents["luigis-trattoria"].Prompt, "Reservations") {
		t.Fatal("platform ref targeting not applied")
	}
}

func TestUnsetVariable(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "agents", "luigis-trattoria.yaml"), "  menu_allergen_link: luigis.com/menu#allergens\n", "")
	eq(t, renderCodes(t, repo), []string{"unset_variable"})
}

func TestDuplicatePlatformRef(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "agents", "sakura-sushi.yaml"), "livekit: sakura-sushi", "livekit: tonys-pizza")
	eq(t, renderCodes(t, repo), []string{"duplicate_platform_ref"})
}

func TestAgentNeedsExactlyOnePlatform(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "agents", "sakura-sushi.yaml"), "livekit: sakura-sushi", "livekit: sakura-sushi\nvapi: asst_123")
	issues := loadIssues(t, repo)
	eq(t, codesOf(issues), []string{"invalid_field"})
	eq(t, issues[0].Path, "agents/sakura-sushi.yaml")
}

func TestExplicitIDMustMatchFileName(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "name: Reservations", "id: bookings\nname: Reservations")
	eq(t, codesOf(loadIssues(t, repo)), []string{"id_mismatch"})
}

func TestUnknownFieldIsRejected(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "name: Reservations", "name: Reservations\nsteps: []")
	eq(t, codesOf(loadIssues(t, repo)), []string{"invalid_field"})
}

func TestMissingGoalIsAWarning(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "description: The customer has a confirmed table, or knows exactly why one isn't available.\n", "")
	b := build(t, repo)
	eq(t, len(b.Warnings), 1)
	eq(t, []string{b.Warnings[0].Code, b.Warnings[0].Severity}, []string{"missing_goal", "warning"})
}

func TestColonInStepGetsAClearFix(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "  - Never double-book a table", "  - Never say: we're fully booked")
	issues := loadIssues(t, repo)
	eq(t, len(issues), 1)
	i := issues[0]
	eq(t, i.Code, "colon_in_step")
	eq(t, i.Path, "procedures/reservations.yaml")
	if !strings.Contains(i.Message, "forbiddenActions[0]") || !strings.Contains(i.Message, `- "Never say: we're fully booked"`) {
		t.Fatal(i.Message)
	}
}

func TestQuotedColonStepIsFine(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "  - Never double-book a table", `  - "Never say: fully booked"`)
	if !strings.Contains(build(t, repo).Agents["sakura-sushi"].Prompt, "- Never say: fully booked") {
		t.Fatal("quoted step missing")
	}
}

func TestUnquotedBooleanAndEmptySteps(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "  - Never double-book a table", "  - no\n  -")
	eq(t, codesOf(loadIssues(t, repo)), []string{"unquoted_value", "empty_step"})
}

func TestColonInUnquotedFieldGetsAHint(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "scope: The customer wants", "scope: Note: the customer wants")
	issues := loadIssues(t, repo)
	eq(t, len(issues), 1)
	eq(t, issues[0].Code, "invalid_yaml")
	if !strings.Contains(issues[0].Message, "put the text in quotes") {
		t.Fatal(issues[0].Message)
	}
}

// --- test_render.py ------------------------------------------------------------------------

func headings(prompt string) []string {
	out := []string{}
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "### ") {
			out = append(out, line[4:])
		}
	}
	return out
}

func TestBuildMatchesGoldenOutput(t *testing.T) {
	out := t.TempDir()
	if _, err := core.WriteBuild(build(t, fixtureSOPs()), out); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(tu.Fixture(), "expected")
	names := func(dir string) []string {
		entries, _ := os.ReadDir(dir)
		var n []string
		for _, e := range entries {
			n = append(n, e.Name())
		}
		sort.Strings(n)
		return n
	}
	eq(t, names(out), names(expected))
	for _, n := range names(expected) {
		if tu.Read(t, filepath.Join(out, n)) != tu.Read(t, filepath.Join(expected, n)) {
			t.Errorf("%s differs from the golden output", n)
		}
	}
}

func TestExampleBuildMatches(t *testing.T) {
	out := t.TempDir()
	if _, err := core.WriteBuild(build(t, tu.Example()), out); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"lock.json", "la-casita.prompt.md", "sakura-sushi.prompt.md"} {
		if tu.Read(t, filepath.Join(out, n)) != tu.Read(t, filepath.Join(tu.Example(), "build", n)) {
			t.Errorf("%s differs from the example build", n)
		}
	}
}

func TestBasesRenderParentsFirstThenTargetedThenBottom(t *testing.T) {
	tonys := build(t, fixtureSOPs()).Agents["tonys-pizza"]
	var ids []string
	for _, b := range tonys.Bases {
		ids = append(ids, b.ID)
	}
	eq(t, ids, []string{"restaurant-host", "pizza-context", "brand-voice", "closing"})
	p := tonys.Prompt
	idx := strings.Index
	if !(idx(p, "phone host") < idx(p, `12" and 16"`) && idx(p, `12" and 16"`) < idx(p, "Speak warmly")) {
		t.Fatal("base order")
	}
	if !(idx(p, "Speak warmly") < idx(p, "wood-fired") && idx(p, "wood-fired") < idx(p, "## Procedures")) {
		t.Fatal("instructions order")
	}
	if !strings.HasSuffix(strings.TrimRight(p, "\n "), "pickup or delivery time.") {
		t.Fatal("bottom base not last")
	}
}

func TestSOPTargetingOrderAndExclude(t *testing.T) {
	b := build(t, fixtureSOPs())
	eq(t, headings(b.Agents["tonys-pizza"].Prompt), []string{"Allergen check", "Delivery", "Large orders"})
	eq(t, headings(b.Agents["luigis-trattoria"].Prompt), []string{"Allergen check", "Delivery", "Large orders", "Reservations"})
	eq(t, headings(b.Agents["sakura-sushi"].Prompt), []string{"Allergen check", "Reservations"})
}

func TestVariablesUseAgentValuesOverWorkspaceDefaults(t *testing.T) {
	b := build(t, fixtureSOPs())
	for agent, text := range map[string]string{"tonys-pizza": "tonys.com/allergens", "sakura-sushi": "transfer to the head chef"} {
		if !strings.Contains(b.Agents[agent].Prompt, text) {
			t.Fatalf("%s lacks %q", agent, text)
		}
	}
	if !strings.Contains(b.Agents["tonys-pizza"].Prompt, "transfer to the manager on duty") {
		t.Fatal("default variable not used")
	}
	for _, r := range b.Agents {
		if strings.Contains(r.Prompt, "{{") {
			t.Fatal("unfilled placeholder")
		}
	}
}

func TestAutoDeliveryKeepsGuardsInPromptAndStepsInTool(t *testing.T) {
	b := build(t, fixtureSOPs())
	luigis := b.Agents["luigis-trattoria"]
	if !strings.Contains(luigis.Prompt, "Never promise a pickup time") || strings.Contains(luigis.Prompt, "Check kitchen capacity") || !strings.Contains(luigis.Prompt, "id `large-orders`") {
		t.Fatal("auto delivery prompt")
	}
	payload, _ := luigis.ToolPayload.Get("large-orders")
	p := payload.(pyx.Obj)
	steps, _ := p.Get("procedureSteps")
	eq(t, steps.([]any)[1], any(pyx.Obj{{K: "text", V: "Check kitchen capacity for the requested time"}, {K: "tool", V: "check_capacity"}}))
	text, _ := p.Get("text")
	if !strings.Contains(text.(string), "transfer to the manager on duty") {
		t.Fatal("payload variables not filled")
	}
	if len(b.Agents["sakura-sushi"].ToolPayload) != 0 {
		t.Fatal("sakura-sushi should have no tool payload")
	}
}

func TestToolStepsRenderAsInstructionsAndAreListed(t *testing.T) {
	tonys := build(t, fixtureSOPs()).Agents["tonys-pizza"]
	if !strings.Contains(tonys.Prompt, "Use the `lookup_allergens` tool.") || !strings.Contains(tonys.Prompt, "This applies to the `place_order` tool.") {
		t.Fatal("tool lines")
	}
	eq(t, tonys.Tools, []string{"check_capacity", "check_delivery_zone", "lookup_allergens", "place_order", "transfer_to_staff"})
}

func changedAgents(a, b *core.Build) []string {
	var out []string
	for id := range a.Agents {
		if a.Agents[id].Hash() != b.Agents[id].Hash() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func TestEditingASharedBaseChangesEveryAgentThatUsesIt(t *testing.T) {
	repo := tu.Repo(t)
	before := build(t, repo)
	tu.Edit(t, path(repo, "bases", "pizza-context.md"), `12" and 16"`, `10", 12" and 16"`)
	after := build(t, repo)
	eq(t, changedAgents(before, after), []string{"tonys-pizza"})
	tu.Edit(t, path(repo, "bases", "brand-voice.md"), "briefly", "concisely")
	final := build(t, repo)
	eq(t, changedAgents(after, final), before.IDs())
}

func TestLockListsBlocksAndTools(t *testing.T) {
	var lock struct {
		Agents map[string]struct {
			PlatformRef string `json:"platform_ref"`
			Blocks      []struct{ Kind, ID string }
			Tools       []string
		}
	}
	if err := json.Unmarshal([]byte(pyx.DumpsIndent2Unicode(build(t, fixtureSOPs()).Lock())), &lock); err != nil {
		t.Fatal(err)
	}
	sakura := lock.Agents["sakura-sushi"]
	eq(t, sakura.PlatformRef, "livekit:sakura-sushi")
	var blocks []string
	for _, b := range sakura.Blocks {
		blocks = append(blocks, b.Kind+":"+b.ID)
	}
	eq(t, blocks, []string{"agent:sakura-sushi", "base:restaurant-host", "base:brand-voice", "base:closing", "sop:allergen-check", "sop:reservations"})
	for _, tool := range sakura.Tools {
		if tool == "check_delivery_zone" {
			t.Fatal("sakura-sushi should not list check_delivery_zone")
		}
	}
}

func TestWriteBuildRemovesStaleFiles(t *testing.T) {
	out := t.TempDir()
	tu.Write(t, filepath.Join(out, "old-agent.prompt.md"), "stale")
	if _, err := core.WriteBuild(build(t, fixtureSOPs()), out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "old-agent.prompt.md")); err == nil {
		t.Fatal("stale file kept")
	}
	if !strings.Contains(tu.Read(t, filepath.Join(out, "lock.json")), `"version": 1`) {
		t.Fatal("lock version")
	}
}

// --- plan (test_plan_server_cli.py) ------------------------------------------------------------

func snap(t *testing.T, root string) core.Snapshot { return core.SnapshotOf(build(t, root)) }

func TestPlanAttributesChangesToBlocks(t *testing.T) {
	repo := tu.Repo(t)
	before := snap(t, repo)
	tu.Edit(t, path(repo, "bases", "brand-voice.md"), "briefly", "concisely")
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "Never double-book a table", "Never double-book or overbook a table")
	plan := core.MakePlan(before, snap(t, repo))
	var ids []string
	for _, c := range plan.Changes {
		ids = append(ids, c.AgentID)
	}
	eq(t, ids, []string{"luigis-trattoria", "sakura-sushi", "tonys-pizza"})
	eq(t, plan.ByBlock(), []core.BlockGroup{
		{Block: "base:brand-voice", Change: "edited", Agents: []string{"luigis-trattoria", "sakura-sushi", "tonys-pizza"}},
		{Block: "sop:reservations", Change: "edited", Agents: []string{"luigis-trattoria", "sakura-sushi"}},
	})
	if !strings.Contains(plan.Text(true), "base `brand-voice` edited → 3 agents") {
		t.Fatal(plan.Text(true))
	}
	if !strings.Contains(plan.Changes[0].Diff, "-Speak warmly and briefly.") || !strings.Contains(plan.Changes[0].Diff, "+Speak warmly and concisely.") {
		t.Fatal(plan.Changes[0].Diff)
	}
}

func TestPlanReportsTargetingChanges(t *testing.T) {
	repo := tu.Repo(t)
	before := snap(t, repo)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "[sakura-sushi, luigis-trattoria]", "[sakura-sushi]")
	plan := core.MakePlan(before, snap(t, repo))
	eq(t, plan.ByBlock(), []core.BlockGroup{{Block: "sop:reservations", Change: "removed", Agents: []string{"luigis-trattoria"}}})
	found := false
	for _, line := range plan.Summary() {
		found = found || line == "SOP `reservations` no longer applies → 1 agent: luigis-trattoria"
	}
	if !found {
		t.Fatal(plan.Summary())
	}
}

func TestPlanAttributesDefaultVariableChangesToWorkspace(t *testing.T) {
	repo := tu.Repo(t)
	before := snap(t, repo)
	tu.Edit(t, path(repo, "opensop.yaml"), "the manager on duty", "the shift lead")
	eq(t, core.MakePlan(before, snap(t, repo)).ByBlock(), []core.BlockGroup{
		{Block: "workspace:opensop.yaml", Change: "edited", Agents: []string{"luigis-trattoria", "tonys-pizza"}},
	})
}

func TestPlanNewAndRemovedAgents(t *testing.T) {
	repo := tu.Repo(t)
	before := snap(t, repo)
	os.Remove(path(repo, "agents", "sakura-sushi.yaml"))
	tu.Write(t, path(repo, "agents", "luigis-brooklyn.yaml"), tu.Read(t, path(repo, "agents", "luigis-trattoria.yaml")))
	tu.Edit(t, path(repo, "agents", "luigis-brooklyn.yaml"), "livekit: luigis-trattoria", "livekit: luigis-brooklyn")
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "[sakura-sushi, luigis-trattoria]", "[luigis-trattoria]")
	var got []string
	for _, c := range core.MakePlan(before, snap(t, repo)).Changes {
		got = append(got, c.AgentID+" "+c.Status)
	}
	eq(t, got, []string{"luigis-brooklyn added", "sakura-sushi removed"})
}

func TestNoChangeMeansEmptyPlan(t *testing.T) {
	expected, err := core.ReadSnapshot(filepath.Join(tu.Fixture(), "expected"))
	if err != nil {
		t.Fatal(err)
	}
	if !core.MakePlan(snap(t, fixtureSOPs()), expected).Empty() {
		t.Fatal("plan should be empty")
	}
}

// --- affected (test_affected.py) ------------------------------------------------------------------

func TestOnlyAgentsWhosePromptChangedWithTheBlocksThatChangedIt(t *testing.T) {
	repo := tu.Repo(t)
	base := build(t, repo)
	tu.Edit(t, path(repo, "procedures", "reservations.yaml"), "Never double-book a table", "Never double-book or overbook a table")
	tu.Edit(t, path(repo, "bases", "pizza-context.md"), `12" and 16"`, `10", 12" and 16"`)
	result, err := core.ComputeAffected(build(t, repo), base, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.All {
		t.Fatal("all should be false")
	}
	got := map[string][]any{}
	for _, a := range result.Agents {
		got[a.ID] = []any{a.Reason, a.Changed, a.ChangedSOPs}
	}
	eq(t, got, map[string][]any{
		"luigis-trattoria": {"changed", []string{"sop:reservations"}, []string{"reservations"}},
		"sakura-sushi":     {"changed", []string{"sop:reservations"}, []string{"reservations"}},
		"tonys-pizza":      {"changed", []string{"base:pizza-context"}, []string{}},
	})
}

func TestNothingChanged(t *testing.T) {
	head := build(t, tu.Repo(t))
	r, _ := core.ComputeAffected(head, head, nil, false)
	eq(t, len(r.Agents), 0)
	everyone, _ := core.ComputeAffected(head, head, nil, true)
	if !everyone.All {
		t.Fatal("expected all")
	}
	eq(t, everyone.IDs(), head.IDs())
}

func TestNoBaseMeansEveryAgent(t *testing.T) {
	r, _ := core.ComputeAffected(build(t, tu.Repo(t)), nil, nil, false)
	if !r.All || len(r.Agents) != 3 {
		t.Fatal("expected every agent")
	}
}

func TestRequestedByOpenSOPIDOrPlatformID(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, path(repo, "agents", "luigis-trattoria.yaml"), "livekit: luigis-trattoria", "vapi: asst_9f3e")
	head := build(t, repo)
	r, err := core.ComputeAffected(head, head, []string{"asst_9f3e", "sakura-sushi", "vapi:asst_9f3e"}, false)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, r.IDs(), []string{"luigis-trattoria", "sakura-sushi"})
	eq(t, r.Agents[0].PlatformID, "asst_9f3e")
	v, _ := r.GitHubOutputs().Get("platform_ids")
	eq(t, v, any("asst_9f3e sakura-sushi"))
	_, err = core.ComputeAffected(head, head, []string{"la-casa"}, false)
	if err == nil || !strings.Contains(err.Error(), "unknown agent(s): la-casa") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}
