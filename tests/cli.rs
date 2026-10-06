//! Tests of the sopc binary: commands, flags, exit codes and files it writes.

use std::path::{Path, PathBuf};
use std::process::Command;

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
}
fn fixture() -> PathBuf {
    repo_root().join("tests/fixtures/restaurants")
}
fn example() -> PathBuf {
    repo_root().join("examples/livekit-restaurant/sops")
}
fn read(p: &Path) -> String {
    std::fs::read_to_string(p).unwrap()
}

struct Output {
    code: i32,
    stdout: String,
    stderr: String,
}

fn sopc_in(dir: &Path, args: &[&str], env: &[(&str, &Path)]) -> Output {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_sopc"));
    cmd.args(args).current_dir(dir).env_remove("GITHUB_OUTPUT").env_remove("GITHUB_STEP_SUMMARY");
    for (k, v) in env {
        cmd.env(k, v);
    }
    let out = cmd.output().unwrap();
    Output {
        code: out.status.code().unwrap(),
        stdout: String::from_utf8(out.stdout).unwrap(),
        stderr: String::from_utf8(out.stderr).unwrap(),
    }
}

/// Runs sopc and checks the exit code.
fn run(want: i32, args: &[&str]) -> Output {
    let out = sopc_in(&repo_root(), args, &[]);
    assert_eq!(out.code, want, "sopc {args:?}\nstdout: {}\nstderr: {}", out.stdout, out.stderr);
    out
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

fn edit(path: &Path, old: &str, new: &str) {
    let text = read(path);
    assert!(text.contains(old), "{old:?} not in {}", path.display());
    std::fs::write(path, text.replace(old, new)).unwrap();
}

/// A temp folder holding a copy of the fixture's sops/.
fn repo() -> (tempfile::TempDir, String) {
    let dir = tempfile::tempdir().unwrap();
    copy_dir(&fixture().join("sops"), &dir.path().join("sops"));
    let root = dir.path().join("sops").to_string_lossy().into_owned();
    (dir, root)
}

fn git(dir: &Path, args: &[&str]) {
    let out = Command::new("git").args(args).current_dir(dir).output().unwrap();
    assert!(out.status.success(), "git {args:?}: {}", String::from_utf8_lossy(&out.stderr));
}

/// A git repo with the fixture committed at sops/ on main.
fn git_repo() -> (tempfile::TempDir, String) {
    let (dir, root) = repo();
    git(dir.path(), &["init", "-q", "-b", "main"]);
    git(dir.path(), &["add", "."]);
    git(dir.path(), &["-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"]);
    (dir, root)
}

#[test]
fn render_check_and_plan() {
    let (_dir, root) = repo();
    run(0, &["render", &root]);
    run(0, &["render", &root, "--check"]);
    edit(&Path::new(&root).join("bases/closing.md"), "repeat the order total", "repeat the order and total");
    let out = run(1, &["render", &root, "--check"]);
    assert!(out.stderr.contains("is out of date; run `sopc render`"));
    let out = run(0, &["plan", &root, "--summary"]);
    assert!(out.stdout.contains("base `closing` edited → 3 agents"), "{}", out.stdout);
}

#[test]
fn render_prints_a_tidy_output_path() {
    let (dir, _) = repo();
    let out = sopc_in(dir.path(), &["render", "./sops/"], &[]);
    assert_eq!(out.stdout, "wrote 6 files to sops/build\n");
}

#[test]
fn plan_against_a_git_ref() {
    let (_dir, root) = git_repo();
    edit(
        &Path::new(&root).join("procedures/allergen-check.yaml"),
        "Name the specific allergen",
        "Repeat the specific allergen",
    );
    let out = run(0, &["plan", &root, "--against", "main", "--summary"]);
    assert!(out.stdout.contains("SOP `allergen-check` edited → 3 agents"), "{}", out.stdout);
    let out = run(1, &["plan", &root, "--against", "nope"]);
    assert!(out.stderr.starts_with("git ls-tree -r --name-only nope -- sops/: "), "{}", out.stderr);
}

#[test]
fn plan_against_a_ref_from_before_the_rename() {
    let (dir, root) = repo();
    std::fs::rename(Path::new(&root).join("sopc.yaml"), Path::new(&root).join("opensop.yaml")).unwrap();
    git(dir.path(), &["init", "-q", "-b", "main"]);
    git(dir.path(), &["add", "."]);
    git(dir.path(), &["-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"]);
    let out = run(1, &["validate", &root]);
    assert!(out.stderr.contains("rename opensop.yaml to sopc.yaml"), "{}", out.stderr);
    git(dir.path(), &["mv", "sops/opensop.yaml", "sops/sopc.yaml"]);
    let out = run(0, &["plan", &root, "--against", "main", "--summary"]);
    assert!(out.stdout.contains("No agent prompts change."), "{}", out.stdout);
}

#[test]
fn validate_reports_errors() {
    let (_dir, root) = repo();
    edit(&Path::new(&root).join("agents/tonys-pizza.yaml"), "inherits: [pizza-context]", "inherits: [nope]");
    let out = run(1, &["validate", &root]);
    assert!(out.stderr.contains("agents/tonys-pizza.yaml: error [unknown_base]"), "{}", out.stderr);
    assert_eq!(out.stdout, "1 error(s), 0 warning(s)\n");
}

#[test]
fn example_is_valid_and_its_build_is_current() {
    let ex = example().to_string_lossy().into_owned();
    let out = run(0, &["validate", &ex]);
    assert_eq!((out.stdout.as_str(), out.stderr.as_str()), ("0 error(s), 0 warning(s)\n", ""));
    run(0, &["render", &ex, "--check"]);
    run(0, &["check", &ex]);
    run(0, &["fmt", &ex, "--check"]);
}

fn built(root: &str, out: &Path) -> std::collections::BTreeMap<String, String> {
    run(0, &["render", root, "--out", &out.to_string_lossy()]);
    let files = std::fs::read_dir(out).unwrap().map(|e| e.unwrap().path());
    files
        .filter(|p| !p.ends_with("lock.json"))
        .map(|p| (p.file_name().unwrap().to_string_lossy().into_owned(), read(&p)))
        .collect()
}

fn sop_files(root: &str) -> Vec<String> {
    let mut names: Vec<String> = std::fs::read_dir(Path::new(root).join("procedures"))
        .unwrap()
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .collect();
    names.sort();
    names
}

#[test]
fn convert_round_trip_keeps_every_prompt_and_tool_json() {
    for src in [fixture().join("sops"), example()] {
        let dir = tempfile::tempdir().unwrap();
        copy_dir(&src, &dir.path().join("s"));
        let root = dir.path().join("s").to_string_lossy().into_owned();
        let before = built(&root, &dir.path().join("b0"));
        let originals: Vec<(String, String)> = sop_files(&root)
            .into_iter()
            .map(|n| (n.clone(), read(&Path::new(&root).join("procedures").join(&n))))
            .collect();
        let out = run(0, &["convert", &root, "--to", "md"]);
        assert!(out.stdout.contains(".yaml -> procedures/"), "{}", out.stdout);
        assert!(sop_files(&root).iter().all(|n| n.ends_with(".md")));
        assert_eq!(built(&root, &dir.path().join("b1")), before);
        run(0, &["fmt", &root, "--check"]);
        if src == example() {
            // The example's front matter has trailing comments that YAML output can't keep:
            // converting back refuses until approved, and lists them.
            let out = run(1, &["convert", &root, "--to", "yaml"]);
            assert!(out.stderr.contains("not converted: it would remove"), "{}", out.stderr);
            run(0, &["convert", &root, "--to", "yaml", "--yes"]);
        } else {
            run(0, &["convert", &root, "--to", "yaml"]);
        }
        assert_eq!(built(&root, &dir.path().join("b2")), before);
        if src == fixture().join("sops") {
            let now: Vec<(String, String)> = sop_files(&root)
                .into_iter()
                .map(|n| (n.clone(), read(&Path::new(&root).join("procedures").join(&n))))
                .collect();
            assert_eq!(now, originals, "the fixture's YAML comes back byte for byte");
        }
    }
}

#[test]
fn convert_some_sops_and_unknown_ids() {
    let (_dir, root) = repo();
    let out = run(0, &["convert", &root, "--to", "md", "reservations"]);
    assert_eq!(out.stdout, "converted procedures/reservations.yaml -> procedures/reservations.md\n");
    assert!(sop_files(&root).contains(&"allergen-check.yaml".to_string()));
    let out = run(1, &["convert", &root, "--to", "md", "nope"]);
    assert!(out.stderr.contains("error [unknown_sop] 'nope' is not an SOP"), "{}", out.stderr);
    let out = run(0, &["convert", &root, "--to", "md", "reservations"]);
    assert_eq!(out.stdout, "nothing to convert\n");
}

#[test]
fn fmt_check_exit_codes() {
    let (_dir, root) = repo();
    let out = run(0, &["fmt", &root, "--check"]);
    assert!(out.stdout.contains("4 YAML SOP file(s) not checked; add --yaml"), "{}", out.stdout);
    let out = run(0, &["fmt", &root, "--check", "--yaml"]);
    assert_eq!(out.stdout, "0 SOP file(s) would be reformatted, 4 already formatted\n");
    let file = Path::new(&root).join("procedures/reservations.yaml");
    edit(&file, "name: Reservations\n", "\nname:   Reservations\ndelivery: prompt\n");
    let messy = read(&file);
    run(0, &["fmt", &root]); // YAML isn't touched without --yaml
    assert_eq!(read(&file), messy);
    let out = run(1, &["fmt", &root, "--check", "--yaml"]);
    assert_eq!(out.stdout, "would reformat procedures/reservations.yaml\n");
    assert_eq!(read(&file), messy, "--check writes nothing");
    let out = run(0, &["fmt", &root, "--yaml"]);
    assert!(out.stdout.starts_with("formatted procedures/reservations.yaml\n"), "{}", out.stdout);
    assert!(read(&file).starts_with("name: Reservations\nagents:"));
    run(0, &["fmt", &root, "--check", "--yaml"]);
    std::fs::write(Path::new(&root).join("procedures/broken.md"), "# Broken\n## Notes\n").unwrap();
    let out = run(1, &["fmt", &root]);
    assert!(out.stderr.contains("procedures/broken.md: error [md_unknown_section] line 2:"), "{}", out.stderr);
}

#[test]
fn guide_prints_the_format_reference() {
    let out = run(0, &["guide"]);
    assert_eq!(out.stdout, read(&repo_root().join("FORMAT.md")));
}

#[test]
fn format_reference_lists_every_validation_code() {
    let guide = read(&repo_root().join("FORMAT.md"));
    let source = ["workspace.rs", "sopfile.rs"].map(|f| read(&repo_root().join("src").join(f))).join("\n");
    let re = regex::Regex::new(r#"(?:Issue::(?:error|warning)\(|r\.(?:error|warning)\([^"]*?)\s*"([a-z_]+)""#).unwrap();
    let codes: std::collections::BTreeSet<&str> =
        re.captures_iter(&source).map(|c| c.get(1).unwrap().as_str()).collect();
    assert_eq!(codes.len(), 34, "{codes:?}");
    for code in codes {
        assert!(guide.contains(&format!("`{code}`")), "FORMAT.md doesn't document `{code}`");
    }
}

#[test]
fn plan_json_and_agents_json() {
    let (_dir, root) = repo();
    run(0, &["render", &root]);
    edit(
        &Path::new(&root).join("procedures/reservations.yaml"),
        "Never double-book a table",
        "Never double-book or overbook a table",
    );
    let plan: serde_json::Value = serde_json::from_str(&run(0, &["plan", &root, "--json"]).stdout).unwrap();
    let got: Vec<String> = plan["changes"]
        .as_array()
        .unwrap()
        .iter()
        .map(|c| format!("{} {} {}", c["agent"], c["platform_ref"], c["status"]))
        .collect();
    assert_eq!(
        got,
        [
            r#""luigis-trattoria" "livekit:luigis-trattoria" "changed""#,
            r#""sakura-sushi" "livekit:sakura-sushi" "changed""#
        ]
    );
    let agents: serde_json::Value = serde_json::from_str(&run(0, &["agents", &root, "--json"]).stdout).unwrap();
    let sakura = agents.as_array().unwrap().iter().find(|a| a["id"] == "sakura-sushi").unwrap();
    assert_eq!(sakura["platform_id"], "sakura-sushi");
    assert_eq!(sakura["sops"], serde_json::json!(["allergen-check", "reservations"]));
    let tonys = agents.as_array().unwrap().iter().find(|a| a["id"] == "tonys-pizza").unwrap();
    assert!(tonys["tools"].as_array().unwrap().contains(&serde_json::json!("transfer_to_staff")));
}

#[test]
fn affected_against_a_git_ref_writes_github_outputs() {
    let (dir, root) = git_repo();
    edit(
        &Path::new(&root).join("procedures/allergen-check.yaml"),
        "Name the specific allergen",
        "Repeat the specific allergen",
    );
    let (output, summary) = (dir.path().join("out"), dir.path().join("summary"));
    let out = sopc_in(
        dir.path(),
        &["affected", &root, "--against", "main", "--all-if-none", "--ci"],
        &[("GITHUB_OUTPUT", &output), ("GITHUB_STEP_SUMMARY", &summary)],
    );
    assert_eq!(out.code, 0, "{}", out.stderr);
    assert_eq!(out.stdout, "luigis-trattoria\nsakura-sushi\ntonys-pizza\n");
    let outputs: std::collections::BTreeMap<String, String> = read(&output)
        .lines()
        .map(|l| l.split_once('=').unwrap())
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect();
    assert_eq!(outputs["ids"], "luigis-trattoria sakura-sushi tonys-pizza");
    assert_eq!((outputs["count"].as_str(), outputs["all"].as_str()), ("3", "false"));
    let matrix: serde_json::Value = serde_json::from_str(&outputs["matrix"]).unwrap();
    assert_eq!(matrix[0]["changed_sops"], serde_json::json!(["allergen-check"]));
    assert!(read(&summary).contains("`tonys-pizza` (livekit `tonys-pizza`): sop:allergen-check"));
}

#[test]
fn affected_without_github_output_shows_what_it_would_write() {
    let (_dir, root) = repo();
    let out = run(0, &["affected", &root, "--agents", "tonys-pizza", "--ci"]);
    assert!(
        out.stderr.contains("# GITHUB_OUTPUT not set; these would be written:\n#   ids=tonys-pizza\n"),
        "{}",
        out.stderr
    );
}

#[test]
fn affected_json_format_and_unknown_agents() {
    let (_dir, root) = repo();
    let data: serde_json::Value =
        serde_json::from_str(&run(0, &["affected", &root, "--agents", "tonys-pizza", "--format", "json"]).stdout)
            .unwrap();
    assert_eq!(data["count"], 1);
    assert_eq!(data["agents"][0]["reason"], "requested");
    assert_eq!(data["agents"][0]["sops"], serde_json::json!(["allergen-check", "delivery-handling", "large-orders"]));
    let out = run(1, &["affected", &root, "--agents", "nope,tonys-pizza"]);
    assert!(out.stderr.starts_with("error [unknown_agent] unknown agent(s): nope. Known: "), "{}", out.stderr);
}

#[test]
fn compare_exit_code() {
    let sops = fixture().join("sops");
    let originals = fixture().join("originals");
    let out = run(1, &["compare", sops.to_str().unwrap(), "--originals", originals.to_str().unwrap()]);
    assert!(out.stdout.contains("('twice' → 'once')"), "{}", out.stdout);
    let out = run(1, &["compare", sops.to_str().unwrap(), "--originals", repo_root().join("spec").to_str().unwrap()]);
    assert!(out.stderr.contains("error [no_prompts]"), "{}", out.stderr);
}

#[test]
fn overlap_and_check_run() {
    let out = run(0, &["overlap", fixture().join("originals").to_str().unwrap()]);
    assert!(out.stdout.starts_with("3 prompts: luigis-trattoria, sakura-sushi, tonys-pizza\n"));
    assert!(out.stdout.contains("Near-copies"));
    let out = run(0, &["check", fixture().join("sops").to_str().unwrap(), "--json"]);
    assert_eq!(out.stdout, "[]\n");
}

#[test]
fn skills_install_covers_claude_code_codex_and_opencode() {
    let dir = tempfile::tempdir().unwrap();
    let out = sopc_in(dir.path(), &["skills", "install"], &[]);
    assert_eq!(out.code, 0);
    let skill = read(&repo_root().join("skills/sopc-import/SKILL.md"));
    for folder in [".claude/skills", ".agents/skills"] {
        assert_eq!(read(&dir.path().join(folder).join("sopc-import/SKILL.md")), skill);
    }
    for cmd in ["sopc overlap", "sopc compare", "sopc check", "sopc guide"] {
        assert!(skill.contains(cmd), "skill doesn't mention {cmd}");
    }
    for s in ["/sopc-import", "$sopc-import", "OpenCode"] {
        assert!(out.stdout.contains(s), "output lacks {s}");
    }
}

#[test]
fn skills_install_for_one_agent_or_folder() {
    let dir = tempfile::tempdir().unwrap();
    assert_eq!(sopc_in(dir.path(), &["skills", "install", "--agent", "codex"], &[]).code, 0);
    assert!(dir.path().join(".agents/skills/sopc-import/SKILL.md").exists());
    assert!(!dir.path().join(".claude").exists());
    let out = sopc_in(dir.path(), &["skills", "install", "--dir", "custom"], &[]);
    assert_eq!(out.stdout, "installed custom/sopc-import/SKILL.md\n\n");
}

#[test]
fn argument_errors_exit_2() {
    assert!(run(2, &[]).stderr.contains("Usage: sopc <COMMAND>"));
    for args in [
        &["bogus"][..],
        &["render", "--foo"],
        &["affected", "--format", "xx"],
        &["compare"],
        &["render", "--out"],
        &["skills", "foo"],
    ] {
        let out = run(2, args);
        assert!(out.stderr.contains("error:"), "{args:?}: {}", out.stderr);
    }
    let out = run(0, &["render", "-h"]);
    assert!(out.stdout.contains("Usage: sopc render [OPTIONS] [ROOT]"), "{}", out.stdout);
}

#[test]
fn rewrites_that_would_remove_comments_need_yes() {
    let (_dir, root) = repo();
    let file = Path::new(&root).join("procedures/reservations.yaml");
    edit(&file, "name: Reservations\n", "name:   Reservations   # the heading\n");
    edit(&file, "procedureSteps:\n", "procedureSteps:\n  # ask first\n");
    let original = read(&file);

    let out = run(1, &["fmt", &root, "--yaml"]);
    assert!(
        out.stderr.contains("procedures/reservations.yaml: not formatted: it would remove 2 comment(s)"),
        "{}",
        out.stderr
    );
    assert!(out.stderr.contains("# the heading") && out.stderr.contains("# ask first"), "{}", out.stderr);
    assert_eq!(read(&file), original, "nothing is written without --yes");
    run(1, &["fmt", &root, "--yaml", "--check"]);

    let out = run(1, &["convert", &root, "--to", "md", "reservations"]);
    assert!(out.stderr.contains("not converted: it would remove 2 comment(s)"), "{}", out.stderr);
    assert_eq!(read(&file), original);

    run(0, &["fmt", &root, "--yaml", "-y"]);
    assert!(!read(&file).contains("# ask first"));
    run(0, &["fmt", &root, "--yaml", "--check"]);
}
