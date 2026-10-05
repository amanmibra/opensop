//! Reading an OpenSOP folder into a [`Workspace`], and the checks that need all of it.

use crate::model::{self, Agent, Base, Config, Sop, Targeting};
use crate::render::{find_variables, resolve_bases, resolve_sops, sop_payload};
use serde_yaml_ng::{Mapping, Value};
use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::path::Path;

/// A validation error or warning.
#[derive(Clone, Debug, PartialEq)]
pub struct Issue {
    pub code: &'static str,
    pub message: String,
    pub path: String,
    pub warning: bool,
}

impl Issue {
    pub fn error(code: &'static str, path: &str, message: impl Into<String>) -> Self {
        Issue { code, message: message.into(), path: path.to_string(), warning: false }
    }
    fn warning(code: &'static str, path: &str, message: impl Into<String>) -> Self {
        Issue { warning: true, ..Issue::error(code, path, message) }
    }
}

impl fmt::Display for Issue {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        if !self.path.is_empty() {
            write!(f, "{}: ", self.path)?;
        }
        let severity = if self.warning { "warning" } else { "error" };
        write!(f, "{severity} [{}] {}", self.code, self.message)
    }
}

/// Issues that stop a command.
#[derive(Debug)]
pub struct Issues(pub Vec<Issue>);

impl fmt::Display for Issues {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let lines: Vec<String> = self.0.iter().map(|i| i.to_string()).collect();
        write!(f, "{}", lines.join("\n"))
    }
}

impl std::error::Error for Issues {}

/// A loaded folder. Each list is in file-name order.
#[derive(Debug, Default)]
pub struct Workspace {
    pub config: Config,
    pub bases: Vec<Base>,
    pub sops: Vec<Sop>,
    pub agents: Vec<Agent>,
}

impl Workspace {
    pub fn base(&self, id: &str) -> Option<&Base> {
        self.bases.iter().find(|b| b.id == id)
    }
    pub fn sop(&self, id: &str) -> Option<&Sop> {
        self.sops.iter().find(|s| s.id == id)
    }
}

pub fn base_path(id: &str) -> String {
    format!("bases/{id}.md")
}
pub fn sop_path(id: &str) -> String {
    format!("procedures/{id}.yaml")
}
pub fn agent_path(id: &str) -> String {
    format!("agents/{id}.yaml")
}

/// Whether a path (relative to the root, with `/`) is an OpenSOP source file.
pub fn is_source(rel: &str) -> bool {
    let (folder, name) = rel.rsplit_once('/').unwrap_or(("", rel));
    rel == "opensop.yaml"
        || (folder == "bases" && name.ends_with(".md"))
        || ((folder == "procedures" || folder == "agents") && name.ends_with(".yaml"))
}

/// Text with \r\n and \r turned into \n.
pub fn universal_newlines(s: String) -> String {
    if s.contains('\r') {
        s.replace("\r\n", "\n").replace('\r', "\n")
    } else {
        s
    }
}

pub fn read_text(path: &Path) -> std::io::Result<String> {
    std::fs::read_to_string(path).map(universal_newlines)
}

/// The source files of a folder, keyed by path relative to it.
pub fn read_files(root: &Path) -> anyhow::Result<BTreeMap<String, String>> {
    let mut files = BTreeMap::new();
    for folder in ["", "bases", "procedures", "agents"] {
        let Ok(entries) = std::fs::read_dir(root.join(folder)) else { continue };
        for entry in entries {
            let entry = entry?;
            let name = entry.file_name().to_string_lossy().into_owned();
            let rel = if folder.is_empty() { name } else { format!("{folder}/{name}") };
            if is_source(&rel) && entry.path().is_file() {
                files.insert(rel, read_text(&entry.path())?);
            }
        }
    }
    Ok(files)
}

pub fn load(root: &Path) -> anyhow::Result<Workspace> {
    Ok(load_files(&read_files(root)?)?)
}

/// The file name without its extension (`bases/brand-voice.md` → `brand-voice`).
pub fn stem(path: &str) -> &str {
    let name = path.rsplit('/').next().unwrap_or(path);
    match name.rfind('.') {
        Some(i) if i > 0 && i < name.len() - 1 => &name[..i],
        _ => name,
    }
}

/// Parses an in-memory file map (relative path → text).
pub fn load_files(files: &BTreeMap<String, String>) -> Result<Workspace, Issues> {
    let Some(config_text) = files.get("opensop.yaml") else {
        return Err(Issues(vec![Issue::error("missing_config", "", "opensop.yaml not found")]));
    };
    let mut issues = vec![];
    let mut ws = Workspace::default();
    if let Some(map) = load_yaml(config_text, "opensop.yaml", &mut issues) {
        match model::parse_config(&map) {
            Ok(config) => ws.config = config,
            Err(errs) => field_errors(errs, "opensop.yaml", &mut issues),
        }
    }
    let in_folder = |folder: &'static str| {
        files.iter().filter(move |(p, _)| p.starts_with(folder) && p[folder.len()..].starts_with('/'))
    };
    for (path, text) in in_folder("bases") {
        let (meta, body) = split_front_matter(text);
        let Some(mut map) = load_yaml(meta, path, &mut issues) else { continue };
        set_id(&mut map, path, &mut issues);
        map.insert("text".into(), body.trim().into());
        match model::parse_base(&map) {
            Ok(base) => ws.bases.push(base),
            Err(errs) => field_errors(errs, path, &mut issues),
        }
    }
    for (path, text) in in_folder("procedures") {
        let Some(mut map) = load_yaml(text, path, &mut issues) else { continue };
        set_id(&mut map, path, &mut issues);
        if !check_steps(&map, path, &mut issues) {
            continue;
        }
        match model::parse_sop(&map) {
            Ok(sop) => ws.sops.push(sop),
            Err(errs) => field_errors(errs, path, &mut issues),
        }
    }
    for (path, text) in in_folder("agents") {
        let Some(mut map) = load_yaml(text, path, &mut issues) else { continue };
        set_id(&mut map, path, &mut issues);
        match model::parse_agent(&map) {
            Ok(agent) => ws.agents.push(agent),
            Err(errs) => field_errors(errs, path, &mut issues),
        }
    }
    if issues.is_empty() {
        Ok(ws)
    } else {
        Err(Issues(issues))
    }
}

fn field_errors(errs: Vec<String>, path: &str, issues: &mut Vec<Issue>) {
    issues.extend(errs.into_iter().map(|e| Issue::error("invalid_field", path, e)));
}

/// Splits `---` front matter from the body.
fn split_front_matter(text: &str) -> (&str, &str) {
    if text.starts_with("---\n") {
        if let Some(i) = text[3..].find("\n---") {
            let end = i + 3;
            let meta = if end > 4 { &text[4..end] } else { "" };
            return (meta, text[end + 4..].trim_start_matches('\n'));
        }
    }
    ("", text)
}

const COLON_HINT: &str =
    " Usually a colon followed by a space inside unquoted text: put the text in quotes, or use a | block.";

/// Parses a YAML mapping, or reports the problem. An empty file is an empty mapping.
fn load_yaml(text: &str, path: &str, issues: &mut Vec<Issue>) -> Option<Mapping> {
    match serde_yaml_ng::from_str::<Value>(text) {
        Err(err) => {
            let mut msg = err.to_string().split_whitespace().collect::<Vec<_>>().join(" ");
            if msg.contains("mapping values are not allowed") {
                msg.push_str(COLON_HINT);
            }
            issues.push(Issue::error("invalid_yaml", path, msg));
            None
        }
        Ok(Value::Mapping(map)) => Some(map),
        Ok(Value::Null) => Some(Mapping::new()),
        Ok(_) => {
            issues.push(Issue::error("invalid_yaml", path, "expected a mapping"));
            None
        }
    }
}

/// Sets "id" from the file name; an explicit id must agree.
fn set_id(map: &mut Mapping, path: &str, issues: &mut Vec<Issue>) {
    let id = stem(path);
    if let Some(v) = map.get("id") {
        if v.as_str() != Some(id) {
            let msg = format!("id '{}' does not match file name '{id}'", model::scalar_text(v));
            issues.push(Issue::error("id_mismatch", path, msg));
        }
    }
    map.insert("id".into(), id.into());
}

/// Catches steps YAML turned into something other than text.
fn check_steps(map: &Mapping, path: &str, issues: &mut Vec<Issue>) -> bool {
    let before = issues.len();
    for field in ["procedureSteps", "forbiddenActions", "warningSigns"] {
        let Some(Value::Sequence(items)) = map.get(field) else { continue };
        for (i, item) in items.iter().enumerate() {
            let at = format!("{field}[{i}]");
            match item {
                Value::Mapping(m)
                    if m.len() == 1
                        && !m.keys().all(|k| k.as_str().is_some_and(|k| model::STEP_FIELDS.contains(&k))) =>
                {
                    let (k, v) = m.iter().next().unwrap();
                    let text = match v {
                        Value::Null => format!("{}:", model::scalar_text(k)),
                        v => format!("{}: {}", model::scalar_text(k), model::scalar_text(v)),
                    };
                    let quoted = crate::text::ascii_json(&serde_json::to_string(&text).unwrap());
                    let msg = format!("{at} was read as a key and value because of the colon. Put the whole step in quotes: - {quoted}");
                    issues.push(Issue::error("colon_in_step", path, msg));
                }
                Value::Null => issues.push(Issue::error("empty_step", path, format!("{at} is empty"))),
                Value::Bool(_) | Value::Number(_) => {
                    let msg =
                        format!("{at} was read as {}, not text. Put the step in quotes.", model::scalar_text(item));
                    issues.push(Issue::error("unquoted_value", path, msg));
                }
                _ => {}
            }
        }
    }
    issues.len() == before
}

// --- whole-workspace checks ----------------------------------------------------------------------

/// Whether a base or SOP applies to the agent through its own `agents:` field.
pub fn targets(t: &Targeting, agent: &Agent) -> bool {
    let names = [agent.id.clone(), agent.platform_ref()];
    if t.exclude.iter().any(|x| names.contains(x)) {
        return false;
    }
    t.all || t.agents.iter().any(|x| names.contains(x))
}

/// References, cycles, locks and variables.
pub fn validate(ws: &Workspace) -> Vec<Issue> {
    let mut issues = vec![];
    let agent_names: BTreeSet<String> = ws.agents.iter().flat_map(|a| [a.id.clone(), a.platform_ref()]).collect();

    let base_ids: BTreeSet<&str> = ws.bases.iter().map(|b| b.id.as_str()).collect();
    for sop in ws.sops.iter().filter(|s| base_ids.contains(s.id.as_str())).map(|s| &s.id).collect::<BTreeSet<_>>() {
        issues.push(Issue::error("duplicate_id", "", format!("'{sop}' is both a base and an SOP; ids must be unique")));
    }

    let mut seen_refs: BTreeMap<String, &str> = BTreeMap::new();
    for a in &ws.agents {
        let r = a.platform_ref();
        if let Some(prev) = seen_refs.get(&r) {
            issues.push(Issue::error(
                "duplicate_platform_ref",
                &agent_path(&a.id),
                format!("{r} is used by '{prev}' and '{}'", a.id),
            ));
        }
        seen_refs.insert(r, &a.id);
    }

    let blocks = ws
        .bases
        .iter()
        .map(|b| (base_path(&b.id), &b.targeting))
        .chain(ws.sops.iter().map(|s| (sop_path(&s.id), &s.targeting)));
    for (path, t) in blocks {
        let listed = if t.all { &[][..] } else { &t.agents[..] };
        for (field, names) in [("agents", listed), ("exclude", &t.exclude[..])] {
            for name in names.iter().filter(|n| !agent_names.contains(*n)) {
                issues.push(Issue::error(
                    "unknown_agent",
                    &path,
                    format!("{field} lists '{name}', which is not a known agent"),
                ));
            }
        }
    }

    for b in &ws.bases {
        for parent in b.inherits.iter().filter(|p| ws.base(p).is_none()) {
            issues.push(Issue::error(
                "unknown_base",
                &base_path(&b.id),
                format!("inherits '{parent}', which is not a base"),
            ));
        }
    }
    issues.extend(inheritance_cycles(ws));

    for s in ws.sops.iter().filter(|s| s.description.trim().is_empty()) {
        issues.push(Issue::warning(
            "missing_goal",
            &sop_path(&s.id),
            "no description (goal); QA can't judge whether the goal was met",
        ));
    }
    for id in ws.config.sop_order.iter().filter(|id| ws.sop(id).is_none()) {
        issues.push(Issue::error(
            "unknown_sop",
            "opensop.yaml",
            format!("sop_order lists '{id}', which is not an SOP"),
        ));
    }

    for agent in &ws.agents {
        let path = agent_path(&agent.id);
        for id in agent.inherits.iter().filter(|id| ws.base(id).is_none()) {
            issues.push(Issue::error("unknown_base", &path, format!("inherits '{id}', which is not a base")));
        }
        for id in &agent.exclude {
            // An SOP wins over a base with the same id.
            let block =
                ws.sop(id).map(|s| (&s.targeting, s.locked)).or_else(|| ws.base(id).map(|b| (&b.targeting, b.locked)));
            match block {
                None => issues.push(Issue::error(
                    "unknown_block",
                    &path,
                    format!("exclude lists '{id}', which is not a base or SOP"),
                )),
                Some((t, locked)) if targets(t, agent) => {
                    if locked {
                        issues.push(Issue::error("locked", &path, format!("can't exclude '{id}': it is locked")));
                    }
                }
                Some(_) => issues.push(Issue::warning(
                    "useless_exclude",
                    &path,
                    format!("exclude lists '{id}', which doesn't target this agent"),
                )),
            }
        }

        if issues.iter().any(|i| i.code == "unknown_base" || i.code == "inheritance_cycle") {
            continue; // resolving bases would be misleading
        }
        let mut used = BTreeSet::new();
        for b in resolve_bases(ws, agent) {
            used.extend(find_variables(&b.text));
        }
        used.extend(find_variables(&agent.instructions));
        for s in resolve_sops(ws, agent) {
            for_each_string(&sop_payload(s), &mut |text| used.extend(find_variables(text)));
        }
        let values = ws.config.variables.merged(&agent.variables);
        for name in used.iter().filter(|n| values.get(n).is_none()) {
            issues.push(Issue::error("unset_variable", &path, format!("'{{{{{name}}}}}' is used but has no value")));
        }
    }
    issues
}

pub fn for_each_string(v: &serde_json::Value, f: &mut dyn FnMut(&str)) {
    match v {
        serde_json::Value::String(s) => f(s),
        serde_json::Value::Array(items) => items.iter().for_each(|x| for_each_string(x, f)),
        serde_json::Value::Object(map) => map.values().for_each(|x| for_each_string(x, f)),
        _ => {}
    }
}

fn inheritance_cycles(ws: &Workspace) -> Vec<Issue> {
    fn walk(
        ws: &Workspace,
        id: &str,
        stack: &mut Vec<String>,
        reported: &mut BTreeSet<BTreeSet<String>>,
        out: &mut Vec<Issue>,
    ) {
        if let Some(i) = stack.iter().position(|s| s == id) {
            let cycle = &stack[i..];
            if reported.insert(cycle.iter().cloned().collect()) {
                let message = format!("{} → {id}", cycle.join(" → "));
                out.push(Issue::error("inheritance_cycle", &base_path(&cycle[0]), message));
            }
            return;
        }
        if let Some(base) = ws.base(id) {
            stack.push(id.to_string());
            for parent in &base.inherits {
                walk(ws, parent, stack, reported, out);
            }
            stack.pop();
        }
    }
    let mut ids: Vec<&str> = ws.bases.iter().map(|b| b.id.as_str()).collect();
    ids.sort();
    let (mut reported, mut out) = (BTreeSet::new(), vec![]);
    for id in ids {
        walk(ws, id, &mut vec![], &mut reported, &mut out);
    }
    out
}
