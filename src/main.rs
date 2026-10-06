//! The sopc command line.

/// `print!` and `println!` that end the program quietly when stdout is closed (`sopc guide | head`)
/// instead of panicking. They shadow the std macros for the whole crate.
macro_rules! print {
    ($($arg:tt)*) => { $crate::write_stdout(format_args!($($arg)*)) };
}
macro_rules! println {
    () => { print!("\n") };
    ($($arg:tt)*) => { print!("{}\n", format_args!($($arg)*)) };
}

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
use workspace::{is_source, load, load_files, read_files, read_text, stem, Issue, Issues, CONFIG, LEGACY_CONFIG};

/// The format reference, printed by `sopc guide`.
pub const FORMAT_MD: &str = include_str!("../FORMAT.md");
/// Skills installed by `sopc skills install`: (name, SKILL.md).
pub const SKILLS: &[(&str, &str)] = &[("sopc-import", include_str!("../skills/sopc-import/SKILL.md"))];

const ABOUT: &str =
    "The SOP compiler: modular, git-versioned instructions for teams managing multiple task-driven agents.

Run `sopc` to compile every agent's prompt into build/ (with lock.json).
Every command works on the sopc folder (the one with sopc.yaml): ./sops if
sops/sopc.yaml exists, else ./ if sopc.yaml does. Use --dir for another one.";

const LINKS: &str = "Docs: https://github.com/amanmibra/sopc/blob/main/CLI.md
Issues: https://github.com/amanmibra/sopc/issues";

const EXAMPLES: &str = "Examples:
  sopc                         Compile ./sops into sops/build
  sopc --check                 Exit 1 if sops/build is out of date (for CI)
  sopc validate                Find errors in the files
  sopc plan                    Show which agents a change affects, with prompt diffs
  sopc -C path/to/sops -o out  Compile another folder into out/

Docs: https://github.com/amanmibra/sopc/blob/main/CLI.md
Issues: https://github.com/amanmibra/sopc/issues";

/// A command's docs link, shown by `-h`.
macro_rules! docs {
    ($cmd:literal) => {
        concat!("Docs: https://github.com/amanmibra/sopc/blob/main/CLI.md#sopc-", $cmd)
    };
}

/// A command's examples and docs link, shown by `--help`.
macro_rules! examples {
    ($cmd:literal, $examples:literal) => {
        concat!("Examples:\n", $examples, "\n\n", docs!($cmd))
    };
}

#[derive(Parser)]
#[command(
    name = "sopc",
    version,
    about = ABOUT,
    override_usage = "sopc [--dir DIR] [-o DIR] [--check]\n       sopc [--dir DIR] <COMMAND>",
    after_help = LINKS,
    after_long_help = EXAMPLES
)]
struct Cli {
    #[command(subcommand)]
    command: Option<Command>,
    /// The sopc folder, for every command [default: ./sops, or ./ if sopc.yaml is there]
    #[arg(short = 'C', long, global = true, value_name = "DIR")]
    dir: Option<PathBuf>,
    /// Output folder [default: DIR/build]
    #[arg(short = 'o', long, value_name = "DIR")]
    out: Option<PathBuf>,
    /// Fail if build/ is out of date instead of writing it
    #[arg(long)]
    check: bool,
}

#[derive(Subcommand)]
enum Command {
    /// Find errors in the files and print every problem
    #[command(
        after_help = docs!("validate"),
        after_long_help = examples!("validate", "  sopc validate                  Find errors in ./sops
  sopc validate -C path/to/sops  Find errors in another folder")
    )]
    Validate,
    /// Find duplicated text and conflicting instructions in each agent's prompt
    #[command(
        after_help = docs!("lint"),
        after_long_help = examples!("lint", "  sopc lint         List duplicates and conflicts to review
  sopc lint --json  The same findings as JSON")
    )]
    Lint {
        /// Machine-readable output
        #[arg(long)]
        json: bool,
    },
    /// Rewrite Markdown SOP files in one canonical style (YAML ones too with --yaml)
    #[command(
        after_help = docs!("fmt"),
        after_long_help = examples!("fmt", "  sopc fmt          Rewrite Markdown SOPs in the canonical style
  sopc fmt --check  List files that would change; exit 1 if any (for CI)
  sopc fmt --yaml   Format YAML SOPs too")
    )]
    Fmt {
        /// List files that would change and fail if any, without writing
        #[arg(long)]
        check: bool,
        /// Also rewrite YAML SOP files
        #[arg(long)]
        yaml: bool,
        /// Allow changes that remove comments (otherwise those files are listed and left as is)
        #[arg(short = 'y', long)]
        yes: bool,
    },
    /// Show which agents a change affects, with prompt diffs (compares with your default branch; --against REF to choose)
    #[command(
        after_help = docs!("plan"),
        after_long_help = examples!("plan", "  sopc plan                Compare with the default branch, with prompt diffs
  sopc plan --summary      Only which agents change, and because of which block
  sopc plan --against v1   Compare with a tag, branch or commit
  sopc plan --json         Machine-readable output (for CI)")
    )]
    Plan {
        /// Git ref to compare with [default: origin/HEAD, else main or master]
        #[arg(long, value_name = "REF")]
        against: Option<String>,
        /// Omit the diffs
        #[arg(long)]
        summary: bool,
        /// Machine-readable output (for CI)
        #[arg(long)]
        json: bool,
    },
    /// List the agents to test for a change, for CI (compares with your default branch; --against REF to choose)
    #[command(
        after_help = docs!("affected"),
        after_long_help = examples!("affected", "  sopc affected                        Ids of agents whose prompt changed
  sopc affected --format platform-ids  The platforms' own ids instead
  sopc affected --ci --all-if-none     In GitHub Actions: write outputs; every agent if none changed
  sopc affected --agents tonys-pizza   Select agents by hand")
    )]
    Affected {
        /// Git ref to compare with; agents whose prompt changed are selected [default: origin/HEAD,
        /// else main or master; every agent if there is none]
        #[arg(long, value_name = "REF")]
        against: Option<String>,
        /// Select these agents instead (sopc ids or platform ids, space or comma separated)
        #[arg(long)]
        agents: Option<String>,
        /// Select every agent when nothing else is selected
        #[arg(long)]
        all_if_none: bool,
        /// ids: sopc ids (file names); platform-ids: the platform's own ids; json: everything
        #[arg(long, value_enum, default_value = "ids")]
        format: Format,
        /// Also write GitHub Actions outputs and a step summary
        #[arg(long)]
        ci: bool,
    },
    /// List every agent with its platform id, SOPs and tools
    #[command(
        after_help = docs!("agents"),
        after_long_help = examples!("agents", "  sopc agents         One line per agent: id, platform, platform id, SOPs
  sopc agents --json  Everything, with bases, tools and prompt hashes")
    )]
    Agents {
        /// Machine-readable output
        #[arg(long)]
        json: bool,
    },
    /// Rewrite SOPs as Markdown or YAML without changing any prompt
    #[command(
        after_help = docs!("convert"),
        after_long_help = examples!("convert", "  sopc convert --to md                 Every YAML SOP to Markdown
  sopc convert --to yaml reservations  One SOP to YAML")
    )]
    Convert {
        /// The format to write
        #[arg(long, value_enum)]
        to: SopFormat,
        /// SOP ids to convert [default: every SOP not already in that format]
        ids: Vec<String>,
        /// Allow changes that remove comments (otherwise those files are listed and left as is)
        #[arg(short = 'y', long)]
        yes: bool,
    },
    /// Show text that existing prompts share (for importing them)
    #[command(
        after_help = docs!("overlap"),
        after_long_help = examples!("overlap", "  sopc overlap prompts/  Shared and near-copied text across prompts/*.md")
    )]
    Overlap {
        /// Folder with one existing prompt per agent, named <agent-id>.md or .txt
        #[arg(value_name = "PROMPTS")]
        prompts: PathBuf,
    },
    /// Check the compiled prompts still say everything the original prompts did
    #[command(
        after_help = docs!("compare"),
        after_long_help = examples!("compare", "  sopc compare --originals prompts/  Exit 1 if a compiled prompt lost or changed a sentence")
    )]
    Compare {
        /// Folder with <agent-id>.md or .txt originals
        #[arg(long)]
        originals: PathBuf,
    },
    /// Install the sopc skills for coding agents
    #[command(
        after_help = docs!("skills"),
        after_long_help = examples!("skills", "  sopc skills install                 For Claude Code, Codex and OpenCode
  sopc skills install --agent claude  For Claude Code only
  sopc skills install --into DIR      Into another folder")
    )]
    Skills {
        /// What to do
        action: SkillsAction,
        /// Install for this coding agent only (repeatable) [default: Claude Code, Codex and OpenCode]
        #[arg(long, value_enum)]
        agent: Vec<CodingAgent>,
        /// Install into this folder instead
        #[arg(long, value_name = "FOLDER")]
        into: Option<PathBuf>,
    },
    /// Print the format reference (FORMAT.md)
    #[command(
        after_help = docs!("guide"),
        after_long_help = examples!("guide", "  sopc guide         Print the format reference
  sopc guide | less  Page through it")
    )]
    Guide,
}

/// The folder a command works on: `dir` if given, else `cwd/sops` if it holds sopc.yaml, else
/// `cwd` if it does. A folder with only the legacy opensop.yaml is chosen the same way, so loading
/// it explains the rename.
fn resolve_root(dir: Option<PathBuf>, cwd: &Path) -> anyhow::Result<PathBuf> {
    if let Some(dir) = dir {
        if !cwd.join(&dir).is_dir() {
            let msg = format!("{} is not a folder", dir.display());
            return Err(Issues(vec![Issue::error("missing_config", "", msg)]).into());
        }
        return Ok(dir);
    }
    let sops = PathBuf::from("sops");
    let here = PathBuf::from(".");
    for config in [CONFIG, LEGACY_CONFIG] {
        for dir in [&sops, &here] {
            if cwd.join(dir).join(config).is_file() {
                return Ok(dir.clone());
            }
        }
    }
    let msg = format!("no {CONFIG} in ./sops or ./ (pass the folder, e.g. `sopc --dir path/to/sops`)");
    Err(Issues(vec![Issue::error("missing_config", "", msg)]).into())
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
    if cli.command.is_some() && (cli.out.is_some() || cli.check) {
        use clap::CommandFactory;
        Cli::command()
            .error(
                clap::error::ErrorKind::ArgumentConflict,
                "-o/--out and --check apply to compiling (`sopc`), not to a command; put command options after it",
            )
            .exit();
    }
    let result = match cli.command {
        Some(command) => run(command, cli.dir),
        None => compile(cli.dir, cli.out, cli.check),
    };
    match result {
        Ok(code) => code,
        Err(err) => {
            if let Some(issues) = err.downcast_ref::<Issues>() {
                eprintln!("{issues}");
            } else if let Some(git) = err.downcast_ref::<GitError>() {
                eprintln!("{git}");
            } else {
                eprintln!("sopc: error: {err:#}");
            }
            ExitCode::FAILURE
        }
    }
}

/// Writes to stdout; a closed pipe ends the program with success (the reader has what it wanted).
fn write_stdout(args: fmt::Arguments) {
    if let Err(e) = std::io::stdout().lock().write_fmt(args) {
        if e.kind() != std::io::ErrorKind::BrokenPipe {
            eprintln!("sopc: error: can't write output: {e}");
            std::process::exit(1);
        }
        std::process::exit(0);
    }
}

fn build(root: &Path) -> anyhow::Result<Build> {
    Ok(render_workspace(&load(root)?)?)
}

/// Builds the workspace as it was at a git ref; None when the ref has no sopc files.
fn build_at(root: &Path, git_ref: &str) -> anyhow::Result<Option<Build>> {
    let files = files_at_ref(root, git_ref)?;
    if files.is_empty() {
        return Ok(None);
    }
    Ok(Some(render_workspace(&load_files(&files)?)?))
}

/// `sopc`: compiles every agent's prompt into DIR/build (or `out`), or with `check`,
/// fails if that folder is out of date.
fn compile(dir: Option<PathBuf>, out: Option<PathBuf>, check: bool) -> anyhow::Result<ExitCode> {
    let root = resolve_root(dir, Path::new("."))?;
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
        eprintln!("{} is out of date; run `sopc`\n", out.display());
        eprintln!("{}", plan.text(false));
        return Ok(ExitCode::FAILURE);
    }
    let written = write_build(&build, &out)?;
    println!("wrote {written} files to {}", out.display());
    Ok(ExitCode::SUCCESS)
}

fn run(command: Command, dir: Option<PathBuf>) -> anyhow::Result<ExitCode> {
    let root = || resolve_root(dir.clone(), Path::new("."));
    match command {
        Command::Validate => {
            let root = root()?;
            let issues = workspace::validate(&load(&root)?);
            for issue in &issues {
                eprintln!("{issue}");
            }
            let errors = issues.iter().filter(|i| !i.warning).count();
            println!("{errors} error(s), {} warning(s)", issues.len() - errors);
            return Ok(if errors > 0 { ExitCode::FAILURE } else { ExitCode::SUCCESS });
        }
        Command::Plan { against, summary, json } => {
            let root = root()?;
            let after = plan::snapshot(&build(&root)?);
            let Some(against) = against.or_else(|| default_ref(&root)) else {
                bail!("nothing to compare against; pass --against <ref>")
            };
            let before = build_at(&root, &against)?.map(|b| plan::snapshot(&b)).unwrap_or_default();
            let plan = plan::make_plan(&before, &after);
            if json {
                println!("{}", pretty_json(&plan.to_json(), true));
            } else {
                print!("{}", plan.text(!summary));
            }
        }
        Command::Affected { against, agents, all_if_none, format, ci } => {
            let root = root()?;
            let head = build(&root)?;
            let requested: Vec<String> =
                agents.unwrap_or_default().replace(',', " ").split_whitespace().map(String::from).collect();
            let mut base = None;
            let against = if requested.is_empty() { against.or_else(|| default_ref(&root)) } else { against };
            if let Some(r) = &against {
                base = build_at(&root, r)?;
                if base.is_none() {
                    eprintln!("no sopc files at {r}; treating every agent as new");
                }
            }
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
        Command::Agents { json } => {
            let root = root()?;
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
        Command::Overlap { prompts } => print!("{}", analyze::overlap(&read_prompts(&prompts)?, 0.75).text()),
        Command::Compare { originals } => {
            let root = root()?;
            let build = build(&root)?;
            let results = analyze::compare(&build, &read_prompts(&originals)?, 0.9);
            print!("{}", analyze::compare_text(&results, &build));
            if results.iter().any(|r| !r.ok()) {
                return Ok(ExitCode::FAILURE);
            }
        }
        Command::Lint { json } => {
            let root = root()?;
            let findings = analyze::lint(&load(&root)?);
            if json {
                let list = findings.iter().map(|f| f.to_json()).collect();
                println!("{}", pretty_json(&serde_json::Value::Array(list), true));
            } else {
                print!("{}", analyze::lint_text(&findings));
            }
        }
        Command::Fmt { check, yaml, yes } => return fmt(&root()?, check, yaml, yes),
        Command::Convert { to, ids, yes } => {
            let root = root()?;
            let to = match to {
                SopFormat::Md => Kind::Markdown,
                SopFormat::Yaml => Kind::Yaml,
            };
            return convert(&root, to, &ids, yes);
        }
        Command::Skills { action: SkillsAction::Install, agent, into } => install_skills(&agent, into)?,
        Command::Guide => print!("{FORMAT_MD}"),
    }
    Ok(ExitCode::SUCCESS)
}

/// The SOP files of a folder: (relative path, text).
fn sop_files(root: &Path) -> anyhow::Result<Vec<(String, String)>> {
    let files = read_files(root)?;
    if !files.contains_key(CONFIG) {
        return Err(workspace::missing_config(&files).into());
    }
    Ok(files.into_iter().filter(|(p, _)| p.starts_with("procedures/")).collect())
}

/// Comments a rewrite of a file would remove, as "line N: # text".
fn lost_comment_lines(path: &str, text: &str, new: &str) -> Vec<String> {
    sopfile::lost_comments(path, text, new).iter().map(|(n, c)| format!("line {n}: # {c}")).collect()
}

/// Files left as they are because rewriting them would remove comments (destructive changes
/// need --yes). They're reported like check failures, with the comments that would go.
fn report_held(held: &[(String, Vec<String>)], action: &str) {
    for (path, lost) in held {
        eprintln!(
            "{path}: not {action}: it would remove {} comment(s); edit it by hand, or rerun with --yes to remove them",
            lost.len()
        );
        for line in lost {
            eprintln!("  {line}");
        }
    }
}

fn fmt(root: &Path, check: bool, yaml: bool, yes: bool) -> anyhow::Result<ExitCode> {
    let (mut changed, mut held, mut errors, mut total, mut skipped) = (vec![], vec![], vec![], 0, 0);
    for (path, text) in sop_files(root)? {
        if Kind::of(&path) == Kind::Yaml && !yaml {
            skipped += 1; // YAML is only rewritten on request: --yaml
            continue;
        }
        total += 1;
        match sopfile::rewrite(&path, &text, Kind::of(&path)) {
            Ok((_, new)) if new != text => {
                let lost = lost_comment_lines(&path, &text, &new);
                if lost.is_empty() || yes {
                    changed.push((path, new));
                } else {
                    held.push((path, lost));
                }
            }
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
    report_held(&held, "formatted");
    if skipped > 0 {
        println!("{skipped} YAML SOP file(s) not checked; add --yaml to format them too");
    }
    let unchanged = total - changed.len() - held.len();
    if check {
        let need = changed.len() + held.len();
        if need > 0 {
            eprintln!("{need} file(s) need `sopc fmt`; {unchanged} already formatted");
            return Ok(ExitCode::FAILURE);
        }
        println!("0 SOP file(s) would be reformatted, {unchanged} already formatted");
        return Ok(ExitCode::SUCCESS);
    }
    println!("{} SOP file(s) reformatted, {unchanged} already formatted", changed.len());
    if !held.is_empty() {
        eprintln!("{} file(s) not formatted (they'd lose comments)", held.len());
        return Ok(ExitCode::FAILURE);
    }
    Ok(ExitCode::SUCCESS)
}

fn convert(root: &Path, to: Kind, ids: &[String], yes: bool) -> anyhow::Result<ExitCode> {
    let files = sop_files(root)?;
    let mut errors = vec![];
    for id in ids.iter().filter(|id| !files.iter().any(|(p, _)| stem(p) == id.as_str())) {
        errors.push(Issue::error("unknown_sop", "", format!("'{id}' is not an SOP in {}", root.display())));
    }
    let (mut out, mut held) = (vec![], vec![]);
    for (path, text) in &files {
        if Kind::of(path) == to || !(ids.is_empty() || ids.iter().any(|id| id == stem(path))) {
            continue;
        }
        match sopfile::rewrite(path, text, to) {
            Ok((new_path, _)) if files.iter().any(|(p, _)| *p == new_path) => {
                errors.push(Issue::error("duplicate_file", path, format!("{new_path} already exists")));
            }
            Ok((new_path, new)) => {
                let lost = lost_comment_lines(path, text, &new);
                if lost.is_empty() || yes {
                    out.push((path, new_path, new));
                } else {
                    held.push((path.clone(), lost));
                }
            }
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
    report_held(&held, "converted");
    if out.is_empty() && held.is_empty() {
        println!("nothing to convert");
    }
    Ok(if held.is_empty() { ExitCode::SUCCESS } else { ExitCode::FAILURE })
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
            Some(".claude/skills") => println!("Claude Code: /sopc-import"),
            Some(".agents/skills") => {
                println!("Codex: $sopc-import   OpenCode: ask it to use the sopc-import skill")
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

/// The ref `plan` and `affected` compare with when no --against is given: the remote's default
/// branch (origin/HEAD), else the first of origin/main, main, origin/master and master that
/// exists. None outside a git repo or when none of them exist.
fn default_ref(root: &Path) -> Option<String> {
    let quiet = |args: &[&str]| {
        let out = std::process::Command::new("git").args(args).current_dir(root).output().ok()?;
        out.status.success().then(|| String::from_utf8_lossy(&out.stdout).trim().to_string())
    };
    let head = quiet(&["symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"]);
    let candidates = head.into_iter().chain(["origin/main", "main", "origin/master", "master"].map(String::from));
    candidates.into_iter().find(|r| quiet(&["rev-parse", "--verify", "--quiet", &format!("{r}^{{commit}}")]).is_some())
}

/// The sopc source files under `root` as they were at a git ref.
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
    // A ref from before the rename: read its opensop.yaml as the config.
    if !files.contains_key(CONFIG) {
        if let Some(text) = files.remove(LEGACY_CONFIG) {
            files.insert(CONFIG.to_string(), text);
        }
    }
    Ok(files)
}
