//! Tests of the format, rendering, plans and analysis, on tests/fixtures/restaurants.
//! Command-line tests are in tests/cli.rs.

use crate::analyze::{check, compare, overlap, units};
use crate::model::*;
use crate::plan::{affected, make_plan, read_snapshot, snapshot};
use crate::render::{render_workspace, write_build, Build};
use crate::text::pretty_json;
use crate::workspace::{load, validate, Issue, Issues, Workspace};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

pub fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
}
fn fixture() -> PathBuf {
    repo_root().join("tests/fixtures/restaurants")
}
fn sops() -> PathBuf {
    fixture().join("sops")
}
fn example() -> PathBuf {
    repo_root().join("examples/livekit-restaurant/sops")
}

fn read(p: &Path) -> String {
    std::fs::read_to_string(p).unwrap()
}

fn copy_dir(src: &Path, dst: &Path) {
    std::fs::create_dir_all(dst).unwrap();
    for e in std::fs::read_dir(src).unwrap() {
        let p = e.unwrap().path();
        let target = dst.join(p.file_name().unwrap());
        if p.is_dir() {
            copy_dir(&p, &target);
        } else {
            std::fs::copy(&p, &target).unwrap();
        }
    }
}

/// A writable copy of the fixture's sops/ folder.
struct Repo(tempfile::TempDir);

impl Repo {
    fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        copy_dir(&sops(), dir.path());
        Repo(dir)
    }
    fn path(&self, rel: &str) -> PathBuf {
        self.0.path().join(rel)
    }
    fn root(&self) -> &Path {
        self.0.path()
    }
    fn edit(&self, rel: &str, old: &str, new: &str) {
        let text = read(&self.path(rel));
        assert!(text.contains(old), "{old:?} not in {rel}");
        std::fs::write(self.path(rel), text.replace(old, new)).unwrap();
    }
    fn append(&self, rel: &str, text: &str) {
        let old = read(&self.path(rel));
        std::fs::write(self.path(rel), format!("{}{text}", old.trim_end())).unwrap();
    }
}

fn ws(root: &Path) -> Workspace {
    load(root).unwrap()
}
fn build(root: &Path) -> Build {
    render_workspace(&ws(root)).unwrap()
}
fn load_issues(root: &Path) -> Vec<Issue> {
    load(root).unwrap_err().downcast::<Issues>().unwrap().0
}
fn codes(issues: &[Issue]) -> Vec<&str> {
    issues.iter().map(|i| i.code).collect()
}
/// The issues that stop rendering.
fn render_codes(root: &Path) -> Vec<&'static str> {
    render_workspace(&ws(root)).err().unwrap().0.iter().map(|i| i.code).collect()
}

// --- validation ------------------------------------------------------------------------------------

#[test]
fn fixture_is_valid() {
    assert!(validate(&ws(&sops())).is_empty());
}

#[test]
fn locked_base_cannot_be_excluded() {
    let r = Repo::new();
    r.edit("agents/sakura-sushi.yaml", "exclude: [delivery-handling]", "exclude: [delivery-handling, brand-voice]");
    assert_eq!(render_codes(r.root()), ["locked"]);
}

#[test]
fn unlocked_base_can_be_excluded() {
    let r = Repo::new();
    r.edit("agents/sakura-sushi.yaml", "exclude: [delivery-handling]", "exclude: [delivery-handling, closing]");
    assert!(!build(r.root()).agents["sakura-sushi"].bases.iter().any(|b| b.id == "closing"));
}

#[test]
fn inheritance_cycle() {
    let r = Repo::new();
    r.edit("bases/restaurant-host.md", "---\n---", "---\ninherits: [pizza-context]\n---");
    assert_eq!(render_codes(r.root()), ["inheritance_cycle"]);
}

#[test]
fn unknown_base() {
    let r = Repo::new();
    r.edit("agents/tonys-pizza.yaml", "inherits: [pizza-context]", "inherits: [pasta-context]");
    assert_eq!(render_codes(r.root()), ["unknown_base"]);
}

#[test]
fn unknown_agent_in_targeting() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "[sakura-sushi, luigis-trattoria]", "[sakura-sushi, luigis]");
    assert_eq!(render_codes(r.root()), ["unknown_agent"]);
}

#[test]
fn platform_ref_can_be_used_in_targeting() {
    let r = Repo::new();
    r.edit(
        "procedures/reservations.yaml",
        "[sakura-sushi, luigis-trattoria]",
        r#"[sakura-sushi, "livekit:tonys-pizza"]"#,
    );
    let b = build(r.root());
    assert!(b.agents["tonys-pizza"].prompt.contains("Reservations"));
    assert!(!b.agents["luigis-trattoria"].prompt.contains("Reservations"));
}

#[test]
fn unset_variable() {
    let r = Repo::new();
    r.edit("agents/luigis-trattoria.yaml", "  menu_allergen_link: luigis.com/menu#allergens\n", "");
    assert_eq!(render_codes(r.root()), ["unset_variable"]);
}

#[test]
fn duplicate_platform_ref() {
    let r = Repo::new();
    r.edit("agents/sakura-sushi.yaml", "livekit: sakura-sushi", "livekit: tonys-pizza");
    assert_eq!(render_codes(r.root()), ["duplicate_platform_ref"]);
}

#[test]
fn agent_needs_exactly_one_platform() {
    let r = Repo::new();
    r.edit("agents/sakura-sushi.yaml", "livekit: sakura-sushi", "livekit: sakura-sushi\nvapi: asst_123");
    let issues = load_issues(r.root());
    assert_eq!(codes(&issues), ["invalid_field"]);
    assert_eq!(issues[0].path, "agents/sakura-sushi.yaml");
}

#[test]
fn explicit_id_must_match_file_name() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "name: Reservations", "id: bookings\nname: Reservations");
    assert_eq!(codes(&load_issues(r.root())), ["id_mismatch"]);
}

#[test]
fn unknown_field_is_rejected() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "name: Reservations", "name: Reservations\nsteps: []");
    assert_eq!(codes(&load_issues(r.root())), ["invalid_field"]);
}

#[test]
fn missing_goal_is_a_warning() {
    let r = Repo::new();
    r.edit(
        "procedures/reservations.yaml",
        "description: The customer has a confirmed table, or knows exactly why one isn't available.\n",
        "",
    );
    let b = build(r.root());
    assert_eq!(b.warnings.len(), 1);
    assert_eq!((b.warnings[0].code, b.warnings[0].warning), ("missing_goal", true));
}

#[test]
fn colon_in_step_gets_a_clear_fix() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "  - Never double-book a table", "  - Never say: we're fully booked");
    let issues = load_issues(r.root());
    assert_eq!(codes(&issues), ["colon_in_step"]);
    assert_eq!(issues[0].path, "procedures/reservations.yaml");
    assert!(issues[0].message.contains("forbiddenActions[0]"));
    assert!(issues[0].message.contains(r#"- "Never say: we're fully booked""#));
}

#[test]
fn quoted_colon_step_is_fine() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "  - Never double-book a table", r#"  - "Never say: fully booked""#);
    assert!(build(r.root()).agents["sakura-sushi"].prompt.contains("- Never say: fully booked"));
}

#[test]
fn unquoted_number_or_boolean_and_empty_steps() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "  - Never double-book a table", "  - 10\n  - true\n  -");
    assert_eq!(codes(&load_issues(r.root())), ["unquoted_value", "unquoted_value", "empty_step"]);
}

#[test]
fn yaml_1_2_reads_yes_no_and_times_as_text() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "  - Never double-book a table", "  - no\n  - 1:30\n  - On");
    let prompt = build(r.root()).agents["sakura-sushi"].prompt.clone();
    assert!(prompt.contains("- no\n- 1:30\n- On\n"), "{prompt}");
}

#[test]
fn colon_in_unquoted_field_gets_a_hint() {
    let r = Repo::new();
    r.edit("procedures/reservations.yaml", "scope: The customer wants", "scope: Note: the customer wants");
    let issues = load_issues(r.root());
    assert_eq!(codes(&issues), ["invalid_yaml"]);
    assert!(issues[0].message.contains("put the text in quotes"), "{}", issues[0].message);
}

#[test]
fn missing_config() {
    let dir = tempfile::tempdir().unwrap();
    assert_eq!(codes(&load_issues(dir.path())), ["missing_config"]);
}

// --- rendering -------------------------------------------------------------------------------------

fn file_names(dir: &Path) -> Vec<String> {
    let mut names: Vec<String> =
        std::fs::read_dir(dir).unwrap().map(|e| e.unwrap().file_name().to_string_lossy().into_owned()).collect();
    names.sort();
    names
}

#[test]
fn build_matches_golden_output() {
    let out = tempfile::tempdir().unwrap();
    write_build(&build(&sops()), out.path()).unwrap();
    let expected = fixture().join("expected");
    assert_eq!(file_names(out.path()), file_names(&expected));
    for name in file_names(&expected) {
        assert_eq!(read(&out.path().join(&name)), read(&expected.join(&name)), "{name} differs from the golden output");
    }
}

#[test]
fn example_build_is_current() {
    let out = tempfile::tempdir().unwrap();
    write_build(&build(&example()), out.path()).unwrap();
    let committed = example().join("build");
    assert_eq!(file_names(out.path()), file_names(&committed));
    for name in file_names(&committed) {
        assert_eq!(
            read(&out.path().join(&name)),
            read(&committed.join(&name)),
            "{name} differs from the example build"
        );
    }
}

fn headings(prompt: &str) -> Vec<&str> {
    prompt.lines().filter_map(|l| l.strip_prefix("### ")).collect()
}

#[test]
fn bases_render_parents_first_then_targeted_then_bottom() {
    let b = build(&sops());
    let tonys = &b.agents["tonys-pizza"];
    let ids: Vec<&str> = tonys.bases.iter().map(|b| b.id.as_str()).collect();
    assert_eq!(ids, ["restaurant-host", "pizza-context", "brand-voice", "closing"]);
    let at = |s: &str| tonys.prompt.find(s).unwrap();
    assert!(at("phone host") < at("12\" and 16\"") && at("12\" and 16\"") < at("Speak warmly"));
    assert!(at("Speak warmly") < at("wood-fired") && at("wood-fired") < at("## Procedures"));
    assert!(tonys.prompt.trim_end().ends_with("pickup or delivery time."));
}

#[test]
fn sop_targeting_order_and_exclude() {
    let b = build(&sops());
    assert_eq!(headings(&b.agents["tonys-pizza"].prompt), ["Allergen check", "Delivery", "Large orders"]);
    assert_eq!(
        headings(&b.agents["luigis-trattoria"].prompt),
        ["Allergen check", "Delivery", "Large orders", "Reservations"]
    );
    assert_eq!(headings(&b.agents["sakura-sushi"].prompt), ["Allergen check", "Reservations"]);
}

#[test]
fn variables_use_agent_values_over_workspace_defaults() {
    let b = build(&sops());
    assert!(b.agents["tonys-pizza"].prompt.contains("tonys.com/allergens"));
    assert!(b.agents["sakura-sushi"].prompt.contains("transfer to the head chef"));
    assert!(b.agents["tonys-pizza"].prompt.contains("transfer to the manager on duty"));
    assert!(b.agents.values().all(|r| !r.prompt.contains("{{")));
}

#[test]
fn auto_delivery_keeps_guards_in_prompt_and_steps_in_tool() {
    let b = build(&sops());
    let luigis = &b.agents["luigis-trattoria"];
    assert!(luigis.prompt.contains("Never promise a pickup time"));
    assert!(!luigis.prompt.contains("Check kitchen capacity"));
    assert!(luigis.prompt.contains("id `large-orders`"));
    let payload = &luigis.tool_payload["large-orders"];
    assert_eq!(
        payload["procedureSteps"][1],
        serde_json::json!({"text": "Check kitchen capacity for the requested time", "tool": "check_capacity"})
    );
    assert!(payload["text"].as_str().unwrap().contains("transfer to the manager on duty"));
    assert!(b.agents["sakura-sushi"].tool_payload.is_empty());
}

#[test]
fn tool_steps_render_as_instructions_and_are_listed() {
    let b = build(&sops());
    let tonys = &b.agents["tonys-pizza"];
    assert!(tonys.prompt.contains("Use the `lookup_allergens` tool."));
    assert!(tonys.prompt.contains("This applies to the `place_order` tool."));
    assert_eq!(
        tonys.tools,
        ["check_capacity", "check_delivery_zone", "lookup_allergens", "place_order", "transfer_to_staff"]
    );
}

fn changed_agents(a: &Build, b: &Build) -> Vec<String> {
    a.agents.keys().filter(|id| a.agents[*id].hash() != b.agents[*id].hash()).cloned().collect()
}

#[test]
fn editing_a_shared_base_changes_every_agent_that_uses_it() {
    let r = Repo::new();
    let before = build(r.root());
    r.edit("bases/pizza-context.md", "12\" and 16\"", "10\", 12\" and 16\"");
    let after = build(r.root());
    assert_eq!(changed_agents(&before, &after), ["tonys-pizza"]);
    r.edit("bases/brand-voice.md", "briefly", "concisely");
    let last = build(r.root());
    assert_eq!(changed_agents(&after, &last), before.agents.keys().cloned().collect::<Vec<_>>());
}

#[test]
fn lock_lists_blocks_and_tools() {
    let lock = build(&sops()).lock();
    let sakura = &lock["agents"]["sakura-sushi"];
    assert_eq!(sakura["platform_ref"], "livekit:sakura-sushi");
    let blocks: Vec<String> = sakura["blocks"]
        .as_array()
        .unwrap()
        .iter()
        .map(|b| format!("{}:{}", b["kind"].as_str().unwrap(), b["id"].as_str().unwrap()))
        .collect();
    assert_eq!(
        blocks,
        [
            "agent:sakura-sushi",
            "base:restaurant-host",
            "base:brand-voice",
            "base:closing",
            "sop:allergen-check",
            "sop:reservations"
        ]
    );
    assert!(!sakura["tools"].as_array().unwrap().contains(&serde_json::json!("check_delivery_zone")));
}

#[test]
fn write_build_removes_stale_files() {
    let out = tempfile::tempdir().unwrap();
    std::fs::write(out.path().join("old-agent.prompt.md"), "stale").unwrap();
    write_build(&build(&sops()), out.path()).unwrap();
    assert!(!out.path().join("old-agent.prompt.md").exists());
    assert!(read(&out.path().join("lock.json")).contains("\"version\": 1"));
}

#[test]
fn canonical_json_follows_field_order_and_keeps_non_ascii() {
    let base = Base { id: "b".into(), position: "top".into(), text: "Olá \"x\"\n".into(), ..Base::default() };
    assert_eq!(
        base.canonical_json(),
        r#"{"agents":[],"exclude":[],"id":"b","inherits":[],"locked":false,"position":"top","text":"Olá \"x\"\n"}"#
    );
    let keys = |json: String| -> Vec<String> {
        serde_json::from_str::<serde_json::Map<String, serde_json::Value>>(&json).unwrap().keys().cloned().collect()
    };
    assert_eq!(keys(base.canonical_json()), BASE_FIELDS);
    assert_eq!(keys(Sop::default().canonical_json()), SOP_FIELDS);
    assert_eq!(keys(Agent::default().canonical_json()), AGENT_FIELDS);
}

// --- plan and affected ------------------------------------------------------------------------------

fn groups(plan: &crate::plan::Plan) -> Vec<(String, &'static str, Vec<String>)> {
    plan.by_block().into_iter().map(|((b, c), a)| (b, c, a)).collect()
}

fn strings(items: &[&str]) -> Vec<String> {
    items.iter().map(|s| s.to_string()).collect()
}

#[test]
fn plan_attributes_changes_to_blocks() {
    let r = Repo::new();
    let before = snapshot(&build(r.root()));
    r.edit("bases/brand-voice.md", "briefly", "concisely");
    r.edit("procedures/reservations.yaml", "Never double-book a table", "Never double-book or overbook a table");
    let plan = make_plan(&before, &snapshot(&build(r.root())));
    let ids: Vec<&str> = plan.changes.iter().map(|c| c.agent.as_str()).collect();
    assert_eq!(ids, ["luigis-trattoria", "sakura-sushi", "tonys-pizza"]);
    assert_eq!(
        groups(&plan),
        [
            ("base:brand-voice".into(), "edited", strings(&["luigis-trattoria", "sakura-sushi", "tonys-pizza"])),
            ("sop:reservations".into(), "edited", strings(&["luigis-trattoria", "sakura-sushi"])),
        ]
    );
    assert!(plan.text(true).contains("base `brand-voice` edited → 3 agents"));
    assert!(plan.changes[0].diff.contains("-Speak warmly and briefly."));
    assert!(plan.changes[0].diff.contains("+Speak warmly and concisely."));
}

#[test]
fn plan_reports_targeting_changes() {
    let r = Repo::new();
    let before = snapshot(&build(r.root()));
    r.edit("procedures/reservations.yaml", "[sakura-sushi, luigis-trattoria]", "[sakura-sushi]");
    let plan = make_plan(&before, &snapshot(&build(r.root())));
    assert_eq!(groups(&plan), [("sop:reservations".into(), "removed", strings(&["luigis-trattoria"]))]);
    assert!(plan.summary().contains(&"SOP `reservations` no longer applies → 1 agent: luigis-trattoria".to_string()));
}

#[test]
fn plan_attributes_default_variable_changes_to_workspace() {
    let r = Repo::new();
    let before = snapshot(&build(r.root()));
    r.edit("opensop.yaml", "the manager on duty", "the shift lead");
    let plan = make_plan(&before, &snapshot(&build(r.root())));
    assert_eq!(
        groups(&plan),
        [("workspace:opensop.yaml".into(), "edited", strings(&["luigis-trattoria", "tonys-pizza"]))]
    );
}

#[test]
fn plan_new_and_removed_agents() {
    let r = Repo::new();
    let before = snapshot(&build(r.root()));
    std::fs::remove_file(r.path("agents/sakura-sushi.yaml")).unwrap();
    std::fs::copy(r.path("agents/luigis-trattoria.yaml"), r.path("agents/luigis-brooklyn.yaml")).unwrap();
    r.edit("agents/luigis-brooklyn.yaml", "livekit: luigis-trattoria", "livekit: luigis-brooklyn");
    r.edit("procedures/reservations.yaml", "[sakura-sushi, luigis-trattoria]", "[luigis-trattoria]");
    let plan = make_plan(&before, &snapshot(&build(r.root())));
    let got: Vec<String> = plan.changes.iter().map(|c| format!("{} {}", c.agent, c.status)).collect();
    assert_eq!(got, ["luigis-brooklyn added", "sakura-sushi removed"]);
    assert!(plan.changes[0].diff.contains("@@ -0,0 +1,"));
}

#[test]
fn no_change_means_empty_plan() {
    let expected = read_snapshot(&fixture().join("expected")).unwrap();
    assert!(make_plan(&snapshot(&build(&sops())), &expected).is_empty());
}

#[test]
fn affected_picks_agents_whose_prompt_changed_with_the_blocks_that_changed_it() {
    let r = Repo::new();
    let base = build(r.root());
    r.edit("procedures/reservations.yaml", "Never double-book a table", "Never double-book or overbook a table");
    r.edit("bases/pizza-context.md", "12\" and 16\"", "10\", 12\" and 16\"");
    let result = affected(&build(r.root()), Some(&base), &[], false).unwrap();
    assert!(!result.all);
    let got: BTreeMap<&str, (&str, Vec<String>, Vec<String>)> =
        result.agents.iter().map(|a| (a.id.as_str(), (a.reason, a.changed.clone(), a.changed_sops.clone()))).collect();
    let want: BTreeMap<&str, (&str, Vec<String>, Vec<String>)> = [
        ("luigis-trattoria", ("changed", strings(&["sop:reservations"]), strings(&["reservations"]))),
        ("sakura-sushi", ("changed", strings(&["sop:reservations"]), strings(&["reservations"]))),
        ("tonys-pizza", ("changed", strings(&["base:pizza-context"]), vec![])),
    ]
    .into_iter()
    .collect();
    assert_eq!(got, want);
}

#[test]
fn affected_when_nothing_changed() {
    let head = build(&sops());
    assert!(affected(&head, Some(&head), &[], false).unwrap().agents.is_empty());
    let everyone = affected(&head, Some(&head), &[], true).unwrap();
    assert!(everyone.all);
    assert_eq!(everyone.ids(), head.agents.keys().cloned().collect::<Vec<_>>());
}

#[test]
fn affected_without_a_base_is_every_agent() {
    let result = affected(&build(&sops()), None, &[], false).unwrap();
    assert!(result.all && result.agents.len() == 3);
}

#[test]
fn affected_requested_by_opensop_id_or_platform_id() {
    let r = Repo::new();
    r.edit("agents/luigis-trattoria.yaml", "livekit: luigis-trattoria", "vapi: asst_9f3e");
    let head = build(r.root());
    let result =
        affected(&head, Some(&head), &strings(&["asst_9f3e", "sakura-sushi", "vapi:asst_9f3e"]), false).unwrap();
    assert_eq!(result.ids(), ["luigis-trattoria", "sakura-sushi"]);
    assert_eq!(result.agents[0].platform_id, "asst_9f3e");
    assert!(result.github_outputs().contains(&("platform_ids", "asst_9f3e sakura-sushi".to_string())));
    let err = affected(&head, Some(&head), &strings(&["la-casa"]), false).err().unwrap();
    assert!(err.to_string().contains("unknown agent(s): la-casa"));
}

// --- analysis --------------------------------------------------------------------------------------

fn read_dir(dir: &Path, suffix: &str) -> BTreeMap<String, String> {
    let names = file_names(dir).into_iter().filter(|n| n.ends_with(suffix));
    names.map(|n| (n.trim_end_matches(suffix).to_string(), read(&dir.join(&n)))).collect()
}

fn originals() -> BTreeMap<String, String> {
    read_dir(&fixture().join("originals"), ".md")
}

#[test]
fn overlap_finds_text_every_prompt_shares() {
    let result = overlap(&originals(), 0.75);
    let everyone: Vec<&str> = result.shared.iter().filter(|(a, _)| a.len() == 3).map(|(_, t)| t.as_str()).collect();
    assert!(everyone.contains(&"Speak warmly and briefly."));
    assert!(everyone.contains(&"Before hanging up, repeat the order total and the pickup or delivery time."));
}

#[test]
fn overlap_finds_placeholder_candidates_and_drift() {
    let result = overlap(&originals(), 0.75);
    let find = |word: &str| {
        &result.near_copies.iter().find(|nc| nc.variants.values().next().unwrap().contains(word)).unwrap().differing
    };
    let host: Vec<(&str, &str)> = find("phone host").iter().map(|(a, d)| (a.as_str(), d.as_str())).collect();
    assert_eq!(
        host,
        [("luigis-trattoria", "Luigi's Trattoria"), ("sakura-sushi", "Sakura Sushi"), ("tonys-pizza", "Tony's Pizza")]
    );
    assert_eq!(find("upsell")["luigis-trattoria"], "twice");
    assert!(!result.shared.iter().any(|(_, t)| t.contains("upsell")), "drifted text is reported once, as a near-copy");
}

#[test]
fn compare_passes_when_nothing_was_lost() {
    let originals = read_dir(&fixture().join("expected"), ".prompt.md");
    for r in compare(&build(&sops()), &originals, 0.9) {
        assert!(r.ok() && r.coverage() == 1.0 && r.added.is_empty(), "{r:?}");
    }
}

#[test]
fn compare_reports_changed_missing_and_reworded() {
    let mut originals = originals();
    let sakura = originals.get_mut("sakura-sushi").unwrap();
    *sakura = format!("{sakura}\nGift cards can be bought at the counter on weekends.\n")
        .replace("Ask one question at a time.", "Ask up to two questions at a time.");
    let results: BTreeMap<String, _> =
        compare(&build(&sops()), &originals, 0.9).into_iter().map(|r| (r.agent.clone(), r)).collect();
    let pair = |a: &str, b: &str| (a.to_string(), b.to_string());
    assert_eq!(
        results["luigis-trattoria"].changed,
        [pair("Never upsell more than twice per call.", "Never upsell more than once per call.")]
    );
    assert_eq!(results["sakura-sushi"].missing, ["Gift cards can be bought at the counter on weekends."]);
    assert_eq!(
        results["sakura-sushi"].changed,
        [pair("Ask up to two questions at a time.", "Ask one question at a time.")]
    );
    assert!(results["tonys-pizza"].reworded.iter().any(|u| u.starts_with("ALLERGIES:")));
    for r in results.values() {
        assert!(!r.ok() || (r.agent == "tonys-pizza" && r.changed.is_empty()), "{} should not be ok", r.agent);
        assert!(!r.added.iter().any(|u| u.contains("tool.")), "opensop's own tool lines are not 'added'");
    }
}

#[test]
fn check_is_clean_on_the_fixture() {
    assert!(check(&ws(&sops())).is_empty());
}

#[test]
fn check_finds_mechanical_conflicts() {
    let r = Repo::new();
    r.edit(
        "agents/tonys-pizza.yaml",
        "Pickup only after 10pm. Cash and card.",
        "Pickup only after 11pm. Cash and card. Speak warmly and briefly. Always confirm the delivery address.",
    );
    r.edit(
        "agents/tonys-pizza.yaml",
        "  menu_allergen_link: tonys.com/allergens",
        "  menu_allergen_link: tonys.com/allergens\n  old_phone: 555-0100",
    );
    r.append("bases/pizza-context.md", " Pickup only after 10pm.\n");
    r.edit(
        "procedures/delivery-handling.yaml",
        "procedureSteps:",
        "forbiddenActions:\n  - Never confirm the delivery address\nprocedureSteps:",
    );
    let findings = check(&ws(r.root()));
    let by_code: BTreeMap<&str, &crate::analyze::Finding> = findings.iter().map(|f| (f.code, f)).collect();
    assert_eq!(
        by_code.keys().copied().collect::<Vec<_>>(),
        ["duplicate_text", "negation_conflict", "numeric_conflict", "unused_variable"]
    );
    let pair = |a: &str, b: &str| (a.to_string(), b.to_string());
    assert_eq!(
        by_code["numeric_conflict"].sources,
        [
            pair("base `pizza-context`", "Pickup only after 10pm."),
            pair("agent `tonys-pizza`", "Pickup only after 11pm.")
        ]
    );
    assert_eq!(by_code["negation_conflict"].agents, ["tonys-pizza"]);
    assert_eq!(by_code["duplicate_text"].sources[0].0, "base `brand-voice`");
}

#[test]
fn check_reports_a_shared_conflict_once_for_all_agents() {
    let r = Repo::new();
    r.append("bases/closing.md", " Before hanging up, never repeat the order total and the pickup or delivery time.\n");
    let findings = check(&ws(r.root()));
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].code, "negation_conflict");
    assert_eq!(findings[0].agents, ["luigis-trattoria", "sakura-sushi", "tonys-pizza"]);
}

#[test]
fn check_finds_a_number_conflict_inside_a_longer_sentence() {
    let r = Repo::new();
    r.edit(
        "agents/tonys-pizza.yaml",
        "Pickup only after 10pm. Cash and card.",
        "Pickup and delivery until 11pm. Delivery until 10pm on Sundays. Cash and card.",
    );
    assert!(check(&ws(r.root())).is_empty(), "different statements: no conflict");
    r.edit("agents/tonys-pizza.yaml", "Delivery until 10pm on Sundays.", "Delivery until 10pm.");
    let findings = check(&ws(r.root()));
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].code, "numeric_conflict");
}

#[test]
fn units_split_sentences_and_drop_labels() {
    let got = units("## Heading\nSteps:\n1. Ask the caller. Then check! Okay? yes no\n- Goal: Keep it short. \"Quoted start\" here\n  two words\n");
    assert_eq!(got, ["Ask the caller.", "Okay? yes no", "Keep it short.", "\"Quoted start\" here"]);
}

#[test]
fn ascii_json_escapes_like_python() {
    let v = serde_json::json!({"a": "é😀\u{7f}"});
    assert_eq!(pretty_json(&v, true), "{\n  \"a\": \"\\u00e9\\ud83d\\ude00\\u007f\"\n}");
}

// --- spec/*.schema.json ------------------------------------------------------------------------------

fn schema(name: &str) -> serde_json::Value {
    serde_json::from_str(&read(&repo_root().join("spec").join(name))).unwrap()
}

fn keys(v: &serde_json::Value) -> Vec<String> {
    v.as_object().unwrap().keys().cloned().collect()
}

fn list(v: &serde_json::Value) -> Vec<String> {
    v.as_array().map(|a| a.iter().map(|x| x.as_str().unwrap().to_string()).collect()).unwrap_or_default()
}

#[test]
fn fields_match_the_published_schemas() {
    // (schema, fields in canonical order, required fields a file must set)
    for (file, fields, required) in [
        ("base.schema.json", &BASE_FIELDS[..BASE_FIELDS.len() - 1], &[][..]), // "text" is the body, not front matter
        ("sop.schema.json", SOP_FIELDS, SOP_REQUIRED),
        ("agent.schema.json", AGENT_FIELDS, &[][..]),
        ("opensop.schema.json", CONFIG_FIELDS, &[][..]),
    ] {
        let s = schema(file);
        assert_eq!(keys(&s["properties"]), fields, "{file} properties");
        assert_eq!(list(&s["required"]), required, "{file} required");
    }
    assert_eq!(list(&schema("base.schema.json")["properties"]["position"]["enum"]), POSITIONS);
    let sop = schema("sop.schema.json");
    assert_eq!(list(&sop["properties"]["delivery"]["enum"]), DELIVERIES);
    assert_eq!(keys(&sop["$defs"]["Step"]["properties"]), STEP_FIELDS);
    assert_eq!(list(&sop["$defs"]["Step"]["required"]), STEP_REQUIRED);
    assert_eq!(schema("opensop.schema.json")["properties"]["version"]["const"], 1);
    let agent = schema("agent.schema.json");
    assert!(PLATFORMS.iter().all(|p| agent["properties"].get(*p).is_some()));
}
