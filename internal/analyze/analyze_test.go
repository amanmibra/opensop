package analyze_test

// Port of the non-CLI parts of tests/test_analyze.py.

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/amanmibra/opensop/internal/analyze"
	"github.com/amanmibra/opensop/internal/core"
	"github.com/amanmibra/opensop/internal/pyx"
	tu "github.com/amanmibra/opensop/internal/testutil"
)

func sops() string      { return filepath.Join(tu.Fixture(), "sops") }
func originals() string { return filepath.Join(tu.Fixture(), "originals") }

func readDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(matches)
	out := map[string]string{}
	for _, m := range matches {
		out[strings.TrimSuffix(filepath.Base(m), ".md")] = tu.Read(t, m)
	}
	return out
}

func load(t *testing.T, root string) *core.Workspace {
	t.Helper()
	ws, err := core.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func build(t *testing.T) *core.Build {
	t.Helper()
	b, err := core.RenderWorkspace(load(t, sops()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestOverlapFindsTextEveryPromptShares(t *testing.T) {
	result := analyze.ComputeOverlap(readDir(t, originals()), 0.75)
	everyone := map[string]bool{}
	for _, s := range result.Shared {
		if len(s.Agents) == 3 {
			everyone[s.Text] = true
		}
	}
	for _, want := range []string{"Speak warmly and briefly.", "Before hanging up, repeat the order total and the pickup or delivery time."} {
		if !everyone[want] {
			t.Errorf("%q not shared by all", want)
		}
	}
}

func TestOverlapFindsPlaceholderCandidatesAndDrift(t *testing.T) {
	result := analyze.ComputeOverlap(readDir(t, originals()), 0.75)
	find := func(word string) pyx.Obj {
		for _, nc := range result.NearCopies {
			if strings.Contains(nc.Variants[0].V.(string), word) {
				return nc.Differing
			}
		}
		t.Fatalf("no near-copy with %q", word)
		return nil
	}
	eq(t, find("phone host"), pyx.Obj{{K: "luigis-trattoria", V: "Luigi's Trattoria"}, {K: "sakura-sushi", V: "Sakura Sushi"}, {K: "tonys-pizza", V: "Tony's Pizza"}})
	upsell := find("upsell")
	v, _ := upsell.Get("luigis-trattoria")
	eq(t, v, any("twice"))
	for _, s := range result.Shared {
		if strings.Contains(s.Text, "upsell") {
			t.Fatal("drifted text is reported once, as a near-copy")
		}
	}
}

func TestComparePassesWhenNothingWasLost(t *testing.T) {
	originals := map[string]string{}
	for k, v := range readDir(t, filepath.Join(tu.Fixture(), "expected")) {
		originals[strings.TrimSuffix(k, ".prompt")] = v
	}
	for _, r := range analyze.Compare(build(t), originals, 0.9) {
		if !r.OK() || r.Coverage() != 1.0 || len(r.Added) > 0 {
			t.Errorf("%s: %+v", r.Agent, r)
		}
	}
}

func TestCompareReportsChangedMissingAndReworded(t *testing.T) {
	originals := readDir(t, originals())
	originals["sakura-sushi"] += "\nGift cards can be bought at the counter on weekends.\n"
	originals["sakura-sushi"] = strings.ReplaceAll(originals["sakura-sushi"], "Ask one question at a time.", "Ask up to two questions at a time.")
	results := map[string]*analyze.Comparison{}
	for _, r := range analyze.Compare(build(t), originals, 0.9) {
		results[r.Agent] = r
	}
	eq(t, results["luigis-trattoria"].Changed, []analyze.Pair{{"Never upsell more than twice per call.", "Never upsell more than once per call."}})
	eq(t, results["sakura-sushi"].Missing, []string{"Gift cards can be bought at the counter on weekends."})
	eq(t, results["sakura-sushi"].Changed, []analyze.Pair{{"Ask up to two questions at a time.", "Ask one question at a time."}})
	reworded := false
	for _, u := range results["tonys-pizza"].Reworded {
		reworded = reworded || strings.HasPrefix(u, "ALLERGIES:")
	}
	if !reworded {
		t.Fatal("ALLERGIES line not reworded")
	}
	for _, r := range results {
		if r.OK() && (r.Agent != "tonys-pizza" || len(r.Changed) > 0) {
			t.Errorf("%s should not be ok", r.Agent)
		}
		for _, u := range r.Added {
			if strings.Contains(u, "tool.") {
				t.Errorf("opensop's own tool lines are not 'added': %q", u)
			}
		}
	}
}

func TestCheckIsCleanOnTheFixture(t *testing.T) {
	eq(t, len(analyze.Check(load(t, sops()))), 0)
}

func TestCheckFindsMechanicalConflicts(t *testing.T) {
	repo := tu.Repo(t)
	tu.Edit(t, filepath.Join(repo, "agents", "tonys-pizza.yaml"), "Pickup only after 10pm. Cash and card.", "Pickup only after 11pm. Cash and card. Speak warmly and briefly. Always confirm the delivery address.")
	tu.Edit(t, filepath.Join(repo, "agents", "tonys-pizza.yaml"), "  menu_allergen_link: tonys.com/allergens", "  menu_allergen_link: tonys.com/allergens\n  old_phone: 555-0100")
	pizza := filepath.Join(repo, "bases", "pizza-context.md")
	tu.Write(t, pizza, strings.TrimRight(tu.Read(t, pizza), " \n")+" Pickup only after 10pm.\n")
	tu.Edit(t, filepath.Join(repo, "procedures", "delivery-handling.yaml"), "procedureSteps:", "forbiddenActions:\n  - Never confirm the delivery address\nprocedureSteps:")

	findings := map[string]*analyze.Finding{}
	for _, f := range analyze.Check(load(t, repo)) {
		findings[f.Code] = f
	}
	var codes []string
	for c := range findings {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	eq(t, codes, []string{"duplicate_text", "negation_conflict", "numeric_conflict", "unused_variable"})
	eq(t, findings["numeric_conflict"].Sources, []analyze.Source{{"base `pizza-context`", "Pickup only after 10pm."}, {"agent `tonys-pizza`", "Pickup only after 11pm."}})
	eq(t, findings["negation_conflict"].Agents, []string{"tonys-pizza"})
	eq(t, findings["duplicate_text"].Sources[0].Block, "base `brand-voice`")
}

func TestCheckReportsASharedConflictOnceForAllAgents(t *testing.T) {
	repo := tu.Repo(t)
	closing := filepath.Join(repo, "bases", "closing.md")
	tu.Write(t, closing, strings.TrimRight(tu.Read(t, closing), " \n")+" Before hanging up, never repeat the order total and the pickup or delivery time.\n")
	findings := analyze.Check(load(t, repo))
	eq(t, len(findings), 1)
	eq(t, findings[0].Code, "negation_conflict")
	eq(t, findings[0].Agents, []string{"luigis-trattoria", "sakura-sushi", "tonys-pizza"})
}

func TestCheckFindsANumberConflictInsideALongerSentence(t *testing.T) {
	repo := tu.Repo(t)
	agent := filepath.Join(repo, "agents", "tonys-pizza.yaml")
	tu.Edit(t, agent, "Pickup only after 10pm. Cash and card.", "Pickup and delivery until 11pm. Delivery until 10pm on Sundays. Cash and card.")
	eq(t, len(analyze.Check(load(t, repo))), 0) // different statements: no conflict
	tu.Edit(t, agent, "Delivery until 10pm on Sundays.", "Delivery until 10pm.")
	findings := analyze.Check(load(t, repo))
	eq(t, len(findings), 1)
	eq(t, findings[0].Code, "numeric_conflict")
}

func TestUnits(t *testing.T) {
	got := analyze.Units("## Heading\nSteps:\n1. Ask the caller. Then check! Okay? yes no\n- Goal: Keep it short. \"Quoted start\" here\n  two words\n")
	eq(t, got, []string{"Ask the caller.", "Okay? yes no", "Keep it short.", "\"Quoted start\" here"})
}
