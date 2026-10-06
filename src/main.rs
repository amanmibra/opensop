//! The opensop command line.

mod analyze;
mod model;
mod plan;
mod render;
mod sopfile;
mod text;
mod workspace;

#[cfg(test)]
mod tests;

use anyhow::{bail, Context};
use clap::{Parser, Subcommand, ValueEnum};
use render::{render_workspace, write_build, Build};
use sopfile::Kind;
use std::collections::BTreeMap;
use std::fmt;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use text::{pretty_json, tidy};
use workspace::{is_source, load, load_files, read_files, read_text, stem, Issue, Issues};

/// The format reference, printed by `opensop guide`.
pub const FORMAT_MD: &str = include_str!("../FORMAT.md");
/// Skills installed by `opensop skills install`: (name, SKILL.md).
pub const SKILLS: &[(&str, &str)] = &[("opensop-import", include_str!("../skills/opensop-import/SKILL.md"))];

#[derive(Parser)]
#[command(
    name = "opensop",
    version,
    about = "Modular, git-versioned instructions for teams managing multiple task-driven agents."
)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Check the files and print problems
    Validate {
        #[arg(default_value = ".")]
        root: PathBuf,
    },
    /// Write one full prompt per agent into build/
    Render {
        #[arg(default_value = ".")]
        root: PathBuf,
        /// Output folder [default: ROOT/build]
        #[arg(long)]
        out: Option<PathBuf>,
        /// Fail if build/ is out of date instead of writing it
        #[arg(long)]
        check: bool,
    },
    /// Show which agents change and why
    Plan {
        #[arg(default_value = ".")]
        root: PathBuf,
        /// Git ref to compare with [default: the committed build/ folder]
        #[arg(long, value_name = "REF")]
        against: Option<String>,
        /// Omit the diffs
        #[arg(long)]
        summary: bool,
        /// Machine-readable output (for CI)
        #[arg(long)]
        json: bool,
    },
    /// Which agents a change affects (for tests and CI)
    Affected {
        #[arg(default_value = ".")]
        root: PathBuf,
        /// Git ref to compare with; agents whose prompt changed are selected
        #[arg(long, value_name = "REF")]
        against: Option<String>,
        /// Select these agents instead (OpenSOP ids or platform ids, space or comma separated)
        #[arg(long)]
        agents: Option<String>,
        /// Select every agent when nothing else is selected
        #[arg(long)]
        all_if_none: bool,
        /// ids: OpenSOP ids (file names); platform-ids: the platform's own ids; json: everything
        #[arg(long, value_enum, default_value = "ids")]
        format: Format,
        /// Also write GitHub Actions outputs and a step summary
        #[arg(long)]
        ci: bool,
    },
    /// List agents with their platform ids, SOPs and tools
    Agents {
        #[arg(default_value = ".")]
        root: PathBuf,
        #[arg(long)]
        json: bool,
    },
    /// Show text shared across existing prompts (for importing)
    Overlap {
        /// Folder with one existing prompt per agent, named <agent-id>.md or .txt
        dir: PathBuf,
    },
    /// Check rendered prompts still contain everything the originals said
    Compare {
        #[arg(default_value = ".")]
        root: PathBuf,
        /// Folder with <agent-id>.md or .txt originals
        #[arg(long)]
        originals: PathBuf,
    },
    /// Find duplicated text and mechanical conflicts in each agent's prompt
    Check {
        #[arg(default_value = ".")]
        root: PathBuf,
        #[arg(long)]
        json: bool,
    },
    /// Rewrite SOP files (Markdown and YAML) in canonical style
    Fmt {
        #[arg(default_value = ".")]
        root: PathBuf,
        /// List files that would change and fail if any, without writing
        #[arg(long)]
        check: bool,
    },
    /// Rewrite SOPs as Markdown or YAML (all of them, or the ids given)
    Convert {
        root: PathBuf,
        #[arg(long, value_enum)]
        to: SopFormat,
        /// SOP ids [default: every SOP not already in that format]
        ids: Vec<String>,
    },
    /// Install the opensop skills for coding agents
    Skills {
        action: SkillsAction,
        /// Install for this coding agent only (repeatable) [default: Claude Code, Codex and OpenCode]
        #[arg(long, value_enum)]
        agent: Vec<CodingAgent>,
        /// Install into this folder instead
        #[arg(long)]
        dir: Option<PathBuf>,
    },
    /// Print the format reference (FORMAT.md)
    Guide,
}

#[derive(Clone, Copy, ValueEnum)]
enum Format {
    Ids,
    PlatformIds,
    Json,
}

#[derive(Clone, Copy, ValueEnum)]
enum SopFormat {
    Md,
    Yaml,
}

#[derive(Clone, Copy, ValueEnum)]
enum SkillsAction {
    Install,
}

#[derive(Clone, Copy, PartialEq, ValueEnum)]
enum CodingAgent {
    Claude,
    Codex,
    Opencode,
}

impl CodingAgent {
    fn skills_dir(self) -> &'static str {
        match self {
            CodingAgent::Claude => ".claude/skills",
            CodingAgent::Codex | CodingAgent::Opencode => ".agents/skills",
        }
    }
}

/// A failed git command; printed as is.
#[derive(Debug)]
struct GitError(String);

impl fmt::Display for GitError {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for GitError {}

fn main() -> ExitCode {
    let cli = Cli::parse();
    match run(cli.command) {
        Ok(code) => code,
        Err(err) => {
            if let Some(issues) = err.downcast_ref::<Issues>() {
                eprintln!("{issues}");
            } else if let Some(git) = err.downcast_ref::<GitError>() {
                eprintln!("{git}");
            } else {
                eprintln!("opensop: error: {err:#}");
            }
            ExitCode::FAILURE
        }
    }
}

fn build(root: &Path) -> anyhow::Result<Build> {
    Ok(render_workspace(&load(root)?)?)
}

/// Builds the workspace as it was at a git ref; None when the ref has no OpenSOP files.
fn build_at(root: &Path, git_ref: &str) -> anyhow::Result<Option<Build>> {
    let files = files_at_ref(root, git_ref)?;
    if files.is_empty() {
        return Ok(None);
    }
    Ok(Some(render_workspace(&load_files(&files)?)?))
}

fn run(command: Command) -> anyhow::Result<ExitCode> {
    match command {
        Command::Validate { root } => {
            let issues = workspace::validate(&load(&root)?);
            for issue in &issues {
                eprintln!("{issue}");
            }
            let errors = issues.iter().filter(|i| !i.warning).count();
            println!("{errors} error(s), {} warning(s)", issues.len() - errors);
            return Ok(if errors > 0 { ExitCode::FAILURE } else { ExitCode::SUCCESS });
        }
        Command::Render { root, out, check } => {
            let out = tidy(&out.unwrap_or_else(|| root.join("build")));
            let build = build(&root)?;
            for w in &build.warnings {
                eprintln!("{w}");
            }
            if check {
                let plan = plan::make_plan(&plan::read_snapshot(&out)?, &plan::snapshot(&build));
                if plan.is_empty() {
                    println!("{} is up to date", out.display());
                    return Ok(ExitCode::SUCCESS);
                }
                eprintln!("{} is out of date; run `opensop render`\n", out.display());
                eprintln!("{}", plan.text(false));
                return Ok(ExitCode::FAILURE);
            }
            let written = write_build(&build, &out)?;
            println!("wrote {written} files to {}", out.display());
        }
        Command::Plan { root, against, summary, json } => {
            let after = plan::snapshot(&build(&root)?);
            let before = match against {
                Some(r) => build_at(&root, &r)?.map(|b| plan::snapshot(&b)).unwrap_or_default(),
                None => plan::read_snapshot(&root.join("build"))?,
            };
            let plan = plan::make_plan(&before, &after);
            if json {
                println!("{}", pretty_json(&plan.to_json(), true));
            } else {
                print!("{}", plan.text(!summary));
            }
        }
        Command::Affected { root, against, agents, all_if_none, format, ci } => {
            let head = build(&root)?;
            let mut base = None;
            if let Some(r) = &against {
                base = build_at(&root, r)?;
                if base.is_none() {
                    eprintln!("no OpenSOP files at {r}; treating every agent as new");
                }
            }
            let requested: Vec<String> =
                agents.unwrap_or_default().replace(',', " ").split_whitespace().map(String::from).collect();
            let result = plan::affected(&head, base.as_ref(), &requested, all_if_none)?;
            match format {
                Format::Json => println!("{}", pretty_json(&result.to_json(), true)),
                Format::PlatformIds => result.agents.iter().for_each(|a| println!("{}", a.platform_id)),
                Format::Ids => result.agents.iter().for_each(|a| println!("{}", a.id)),
            }
            if ci {
                write_github(&result)?;
            }
        }
        Command::Agents { root, json } => {
            let build = build(&root)?;
            let mut list = vec![];
            for (id, r) in &build.agents {
                let sops: Vec<&str> = r.sops.iter().map(|s| s.id.as_str()).collect();
                if json {
                    let bases: Vec<&str> = r.bases.iter().map(|b| b.id.as_str()).collect();
                    list.push(serde_json::json!({
                        "id": id, "platform_ref": r.agent.platform_ref(), "platform": r.agent.platform(),
                        "platform_id": r.agent.platform_id(), "bases": bases, "sops": sops, "tools": r.tools, "hash": r.hash(),
                    }));
                } else {
                    let sops = if sops.is_empty() { "-".to_string() } else { sops.join(", ") };
                    println!("{id:<24} {:<11} {:<28} sops: {sops}", r.agent.platform(), r.agent.platform_id());
                }
            }
            if json {
                println!("{}", pretty_json(&serde_json::Value::Array(list), true));
            }
        }
        Command::Overlap { dir } => print!("{}", analyze::overlap(&read_prompts(&dir)?, 0.75).text()),
        Command::Compare { root, originals } => {
            let build = build(&root)?;
            let results = analyze::compare(&build, &read_prompts(&originals)?, 0.9);
            print!("{}", analyze::compare_text(&results, &build));
            if results.iter().any(|r| !r.ok()) {
                return Ok(ExitCode::FAILURE);
            }
        }
        Command::Check { root, json } => {
            let findings = analyze::check(&load(&root)?);
            if json {
                let list = findings.iter().map(|f| f.to_json()).collect();
                println!("{}", pretty_json(&serde_json::Value::Array(list), true));
            } else {
                print!("{}", analyze::check_text(&findings));
            }
        }
        Command::Fmt { root, check } => return fmt(&root, check),
        Command::Convert { root, to, ids } => {
            let to = match to {
                SopFormat::Md => Kind::Markdown,
                SopFormat::Yaml => Kind::Yaml,
            };
            convert(&root, to, &ids)?;
        }
        Command::Skills { action: SkillsAction::Install, agent, dir } => install_skills(&agent, dir)?,
        Command::Guide => print!("{FORMAT_MD}"),
    }
    Ok(ExitCode::SUCCESS)
}

/// The SOP files of a folder: (relative path, text).
fn sop_files(root: &Path) -> anyhow::Result<Vec<(String, String)>> {
    let files = read_files(root)?;
    if !files.contains_key("opensop.yaml") {
        return Err(Issues(vec![Issue::error("missing_config", "", "opensop.yaml not found")]).into());
    }
    Ok(files.into_iter().filter(|(p, _)| p.starts_with("procedures/")).collect())
}

fn fmt(root: &Path, check: bool) -> anyhow::Result<ExitCode> {
    let (mut changed, mut errors, mut total) = (vec![], vec![], 0);
    for (path, text) in sop_files(root)? {
        total += 1;
        match sopfile::rewrite(&path, &text, Kind::of(&path)) {
            Ok((_, new)) if new != text => changed.push((path, new)),
            Ok(_) => {}
            Err(e) => errors.extend(e),
        }
    }
    if !errors.is_empty() {
        return Err(Issues(errors).into());
    }
    for (path, new) in &changed {
        if check {
            println!("would reformat {path}");
        } else {
            std::fs::write(root.join(path), new)?;
            println!("formatted {path}");
        }
    }
    let unchanged = total - changed.len();
    if check && !changed.is_empty() {
        eprintln!("{} file(s) need `opensop fmt`; {unchanged} already formatted", changed.len());
        return Ok(ExitCode::FAILURE);
    }
    let done = if check { "would be reformatted" } else { "reformatted" };
    println!("{} SOP file(s) {done}, {unchanged} already formatted", changed.len());
    Ok(ExitCode::SUCCESS)
}

fn convert(root: &Path, to: Kind, ids: &[String]) -> anyhow::Result<()> {
    let files = sop_files(root)?;
    let mut errors = vec![];
    for id in ids.iter().filter(|id| !files.iter().any(|(p, _)| stem(p) == id.as_str())) {
        errors.push(Issue::error("unknown_sop", "", format!("'{id}' is not an SOP in {}", root.display())));
    }
    let mut out = vec![];
    for (path, text) in &files {
        if Kind::of(path) == to || !(ids.is_empty() || ids.iter().any(|id| id == stem(path))) {
            continue;
        }
        match sopfile::rewrite(path, text, to) {
            Ok((new_path, _)) if files.iter().any(|(p, _)| *p == new_path) => {
                errors.push(Issue::error("duplicate_file", path, format!("{new_path} already exists")));
            }
            Ok((new_path, new)) => out.push((path, new_path, new)),
            Err(e) => errors.extend(e),
        }
    }
    if !errors.is_empty() {
        return Err(Issues(errors).into());
    }
    for (old, new_path, new) in &out {
        std::fs::write(root.join(new_path), new)?;
        std::fs::remove_file(root.join(old))?;
        println!("converted {old} -> {new_path}");
    }
    if out.is_empty() {
        println!("nothing to convert");
    }
    Ok(())
}

fn append(path: &str, text: &str) -> anyhow::Result<()> {
    let mut f = std::fs::OpenOptions::new()
        .append(true)
        .create(true)
        .open(path)
        .with_context(|| format!("can't write {path}"))?;
    Ok(f.write_all(text.as_bytes())?)
}

/// Writes $GITHUB_OUTPUT and $GITHUB_STEP_SUMMARY (or shows what would be written).
fn write_github(result: &plan::Affected) -> anyhow::Result<()> {
    let outputs = result.github_outputs();
    match std::env::var("GITHUB_OUTPUT") {
        Ok(path) if !path.is_empty() => {
            append(&path, &outputs.iter().map(|(k, v)| format!("{k}={v}\n")).collect::<String>())?
        }
        _ => {
            eprintln!("\n# GITHUB_OUTPUT not set; these would be written:");
            outputs.iter().for_each(|(k, v)| eprintln!("#   {k}={v}"));
        }
    }
    match std::env::var("GITHUB_STEP_SUMMARY") {
        Ok(path) if !path.is_empty() => append(&path, &result.markdown()),
        _ => Ok(()),
    }
}

/// <name>.md and <name>.txt files in a folder, by name.
fn read_prompts(dir: &Path) -> anyhow::Result<BTreeMap<String, String>> {
    let mut out = BTreeMap::new();
    for entry in std::fs::read_dir(dir).with_context(|| format!("can't read {}", dir.display()))? {
        let path = entry?.path();
        let ext = path.extension().and_then(|e| e.to_str());
        let stem = path.file_stem().and_then(|s| s.to_str()).unwrap_or_default();
        if matches!(ext, Some("md" | "txt")) && path.is_file() && !stem.is_empty() {
            out.insert(stem.to_string(), read_text(&path)?);
        }
    }
    if out.is_empty() {
        let msg = format!("no .md or .txt files in {}", dir.display());
        return Err(Issues(vec![Issue::error("no_prompts", "", msg)]).into());
    }
    Ok(out)
}

fn install_skills(agents: &[CodingAgent], dir: Option<PathBuf>) -> anyhow::Result<()> {
    let dests: Vec<PathBuf> = match dir {
        Some(d) => vec![d],
        None => {
            let agents = if agents.is_empty() {
                &[CodingAgent::Claude, CodingAgent::Codex, CodingAgent::Opencode][..]
            } else {
                agents
            };
            let mut dirs: Vec<&str> = agents.iter().map(|a| a.skills_dir()).collect();
            dirs.sort();
            dirs.dedup();
            dirs.into_iter().map(PathBuf::from).collect()
        }
    };
    for dest in &dests {
        for (name, skill) in SKILLS {
            let target = tidy(&dest.join(name));
            std::fs::create_dir_all(&target)?;
            std::fs::write(target.join("SKILL.md"), skill)?;
            println!("installed {}/SKILL.md", target.display());
        }
    }
    println!();
    for dest in &dests {
        match dest.to_str() {
            Some(".claude/skills") => println!("Claude Code: /opensop-import"),
            Some(".agents/skills") => {
                println!("Codex: $opensop-import   OpenCode: ask it to use the opensop-import skill")
            }
            _ => {}
        }
    }
    Ok(())
}

// --- git -------------------------------------------------------------------------------------------

fn git(cwd: &Path, args: &[&str]) -> anyhow::Result<String> {
    let output = std::process::Command::new("git").args(args).current_dir(cwd).output();
    let output = output.map_err(|e| GitError(format!("git {}: {e}", args.join(" "))))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(GitError(format!("git {}: {}", args.join(" "), stderr.trim())).into());
    }
    Ok(workspace::universal_newlines(String::from_utf8_lossy(&output.stdout).into_owned()))
}

/// The OpenSOP source files under `root` as they were at a git ref.
fn files_at_ref(root: &Path, git_ref: &str) -> anyhow::Result<BTreeMap<String, String>> {
    let root = std::fs::canonicalize(root).with_context(|| format!("can't find {}", root.display()))?;
    let top = PathBuf::from(git(&root, &["rev-parse", "--show-toplevel"])?.trim());
    let top = std::fs::canonicalize(&top).unwrap_or(top);
    let Ok(rel) = root.strip_prefix(&top) else {
        bail!("{} is not in the subpath of {}", root.display(), top.display())
    };
    let rel = rel.to_string_lossy().replace('\\', "/");
    let prefix = if rel.is_empty() { String::new() } else { format!("{rel}/") };
    let path_arg = if prefix.is_empty() { "." } else { &prefix };
    let listing = git(&top, &["ls-tree", "-r", "--name-only", git_ref, "--", path_arg])?;
    let mut files = BTreeMap::new();
    for name in listing.lines() {
        let Some(rel) = name.strip_prefix(&prefix) else { continue };
        if is_source(rel) {
            files.insert(rel.to_string(), git(&top, &["show", &format!("{git_ref}:{name}")])?);
        }
    }
    Ok(files)
}
