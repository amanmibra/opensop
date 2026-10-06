//! Assembling each agent's prompt, the get_sop payloads, and build/ (prompts and lock.json).

use crate::model::{Agent, Base, Sop, Step, Vars};
use crate::text::{pretty_json, sha256_hex};
use crate::workspace::{targets, validate, Issue, Issues, Workspace};
use regex::{Captures, Regex};
use serde_json::{json, Map, Value as Json};
use std::collections::{BTreeMap, BTreeSet};
use std::path::Path;
use std::sync::LazyLock;

static VARIABLE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\{\{\s*([A-Za-z_][\w.-]*)\s*\}\}").unwrap());

/// The placeholder names used in text.
pub fn find_variables(text: &str) -> BTreeSet<String> {
    VARIABLE.captures_iter(text).map(|c| c[1].to_string()).collect()
}

/// Replaces `{{name}}` with its value; names without a value are left as they are.
pub fn fill_variables(text: &str, values: &Vars) -> String {
    VARIABLE.replace_all(text, |c: &Captures| values.get(&c[1]).unwrap_or(&c[0]).to_string()).into_owned()
}

// --- resolution ----------------------------------------------------------------------------------

fn opted_out(id: &str, locked: bool, agent: &Agent) -> bool {
    !locked && agent.exclude.iter().any(|x| x == id)
}

/// The bases an agent gets, in order: its `inherits` (parents first), then bases that target it.
pub fn resolve_bases<'a>(ws: &'a Workspace, agent: &Agent) -> Vec<&'a Base> {
    fn visit<'a>(ws: &'a Workspace, id: &str, stack: &mut Vec<String>, out: &mut Vec<&'a Base>) {
        if out.iter().any(|b| b.id == id) || stack.iter().any(|s| s == id) {
            return;
        }
        let Some(base) = ws.base(id) else { return };
        stack.push(id.to_string());
        for parent in &base.inherits {
            visit(ws, parent, stack, out);
        }
        stack.pop();
        out.push(base);
    }
    let mut targeted: Vec<&Base> = ws.bases.iter().filter(|b| targets(&b.targeting, agent)).collect();
    targeted.sort_by(|a, b| a.id.cmp(&b.id));
    let mut out = vec![];
    for id in &agent.inherits {
        visit(ws, id, &mut vec![], &mut out);
    }
    for b in targeted.into_iter().filter(|b| !opted_out(&b.id, b.locked, agent)) {
        visit(ws, &b.id, &mut vec![], &mut out);
    }
    out
}

/// The SOPs an agent gets: those in `sop_order` first, then the rest by id.
pub fn resolve_sops<'a>(ws: &'a Workspace, agent: &Agent) -> Vec<&'a Sop> {
    let mut matching: Vec<&Sop> =
        ws.sops.iter().filter(|s| targets(&s.targeting, agent) && !opted_out(&s.id, s.locked, agent)).collect();
    matching.sort_by(|a, b| a.id.cmp(&b.id));
    let order = &ws.config.sop_order;
    let ordered = order.iter().filter_map(|id| matching.iter().find(|s| &s.id == id).copied());
    let rest = matching.iter().filter(|s| !order.contains(&s.id)).copied();
    ordered.chain(rest).collect()
}

// --- SOP text ----------------------------------------------------------------------------------

#[derive(Clone, Copy, PartialEq)]
pub enum StepKind {
    Step,
    Forbidden,
    Warning,
}

pub fn render_step(s: &Step, kind: StepKind) -> String {
    let Some(tool) = s.tool() else { return s.text.trim().to_string() };
    let text = s.text.trim().trim_end_matches('.');
    if kind == StepKind::Forbidden {
        format!("{text}. This applies to the `{tool}` tool.")
    } else {
        format!("{text}. Use the `{tool}` tool.")
    }
}

/// An SOP's text; `steps`, `guards` (never/warning signs) and `details` (goal, guidance) can be left out.
pub fn render_sop(s: &Sop, steps: bool, guards: bool, details: bool) -> String {
    let mut lines = vec![format!("### {}", s.name)];
    if details && !s.description.is_empty() {
        lines.push(format!("Goal: {}", s.description.trim()));
    }
    if !s.scope.is_empty() {
        lines.push(format!("When this applies: {}", s.scope.trim()));
    }
    if details && !s.guidance.is_empty() {
        lines.extend([String::new(), s.guidance.trim().to_string()]);
    }
    if steps && !s.procedure_steps.is_empty() {
        lines.extend([String::new(), "Steps:".into()]);
        for (n, st) in s.procedure_steps.iter().enumerate() {
            lines.push(format!("{}. {}", n + 1, render_step(st, StepKind::Step)));
        }
    }
    for (show, title, list, kind) in [
        (guards, "Never:", &s.forbidden_actions, StepKind::Forbidden),
        (guards, "Warning signs:", &s.warning_signs, StepKind::Warning),
    ] {
        if show && !list.is_empty() {
            lines.extend([String::new(), title.into()]);
            lines.extend(list.iter().map(|st| format!("- {}", render_step(st, kind))));
        }
    }
    lines.join("\n")
}

/// What the prompt carries for an SOP, by delivery mode.
pub fn render_sop_in_prompt(s: &Sop) -> String {
    let fetch =
        format!("Before following this procedure, call the `get_sop` tool with id `{}` for the full steps.", s.id);
    match s.delivery.as_str() {
        "auto" => format!("{}\n\n{fetch}", render_sop(s, false, true, false)),
        "tool" => format!("{}\n\n{fetch}", render_sop(s, false, false, false)),
        _ => render_sop(s, true, true, true),
    }
}

fn steps_payload(steps: &[Step]) -> Json {
    let items = steps.iter().map(|s| {
        let mut o = Map::new();
        o.insert("text".into(), json!(s.text.trim()));
        if let Some(tool) = &s.tool {
            o.insert("tool".into(), json!(tool));
        }
        if s.required {
            o.insert("required".into(), json!(true));
        }
        Json::Object(o)
    });
    Json::Array(items.collect())
}

/// What get_sop serves for an SOP (before placeholders are filled). Text is trimmed, so
/// whitespace that YAML or Markdown adds around it doesn't reach the agent.
pub fn sop_payload(s: &Sop) -> Json {
    json!({
        "id": s.id, "name": s.name, "text": render_sop(s, true, true, true),
        "description": s.description.trim(), "scope": s.scope.trim(), "guidance": s.guidance.trim(),
        "procedureSteps": steps_payload(&s.procedure_steps),
        "forbiddenActions": steps_payload(&s.forbidden_actions),
        "warningSigns": steps_payload(&s.warning_signs),
    })
}

/// The tools an SOP's steps name, in order.
pub fn sop_tools(s: &Sop) -> impl Iterator<Item = &str> {
    s.procedure_steps.iter().chain(&s.forbidden_actions).chain(&s.warning_signs).filter_map(|st| st.tool())
}

fn fill_json(v: &mut Json, values: &Vars) {
    match v {
        Json::String(s) => *s = fill_variables(s, values),
        Json::Array(items) => items.iter_mut().for_each(|x| fill_json(x, values)),
        Json::Object(map) => map.values_mut().for_each(|x| fill_json(x, values)),
        _ => {}
    }
}

// --- rendering -----------------------------------------------------------------------------------

/// One agent's built prompt.
pub struct Rendered {
    pub agent: Agent,
    pub prompt: String,
    pub bases: Vec<Base>,
    pub sops: Vec<Sop>,
    /// SOP id → get_sop payload, for SOPs not delivered entirely in the prompt.
    pub tool_payload: Map<String, Json>,
    pub tools: Vec<String>,
}

impl Rendered {
    pub fn hash(&self) -> String {
        sha256_hex(&self.prompt)
    }

    /// "kind:id" → hash of the block's canonical JSON, in prompt order (agent first).
    pub fn block_hashes(&self) -> Vec<(String, String, String)> {
        let agent = ("agent".to_string(), self.agent.id.clone(), sha256_hex(&self.agent.canonical_json()));
        let bases = self.bases.iter().map(|b| ("base".into(), b.id.clone(), sha256_hex(&b.canonical_json())));
        let sops = self.sops.iter().map(|s| ("sop".into(), s.id.clone(), sha256_hex(&s.canonical_json())));
        std::iter::once(agent).chain(bases).chain(sops).collect()
    }
}

/// A rendered workspace, by agent id.
pub struct Build {
    pub agents: BTreeMap<String, Rendered>,
    pub warnings: Vec<Issue>,
}

impl Build {
    pub fn lock(&self) -> Json {
        let mut agents = Map::new();
        for (id, r) in &self.agents {
            let blocks: Vec<Json> = r
                .block_hashes()
                .into_iter()
                .map(|(kind, id, hash)| json!({"kind": kind, "id": id, "hash": hash}))
                .collect();
            agents.insert(
                id.clone(),
                json!({"platform_ref": r.agent.platform_ref(), "hash": r.hash(), "blocks": blocks, "tools": r.tools}),
            );
        }
        json!({"version": 1, "agents": agents})
    }
}

/// Validates and renders every agent.
pub fn render_workspace(ws: &Workspace) -> Result<Build, Issues> {
    let (warnings, errors): (Vec<Issue>, Vec<Issue>) = validate(ws).into_iter().partition(|i| i.warning);
    if !errors.is_empty() {
        return Err(Issues(errors));
    }
    let agents = ws.agents.iter().map(|a| (a.id.clone(), render_agent(ws, a))).collect();
    Ok(Build { agents, warnings })
}

pub fn render_agent(ws: &Workspace, agent: &Agent) -> Rendered {
    let bases = resolve_bases(ws, agent);
    let sops = resolve_sops(ws, agent);
    let mut sections: Vec<String> = bases.iter().filter(|b| b.position == "top").map(|b| b.text.clone()).collect();
    sections.push(agent.instructions.trim().to_string());
    if !sops.is_empty() {
        let parts: Vec<String> = sops.iter().map(|s| render_sop_in_prompt(s)).collect();
        sections.push(format!("{}\n\n{}", ws.config.sops_heading, parts.join("\n\n")));
    }
    sections.extend(bases.iter().filter(|b| b.position == "bottom").map(|b| b.text.clone()));
    sections.retain(|s| !s.is_empty());
    let values = ws.config.variables.merged(&agent.variables);
    let prompt = fill_variables(&sections.join("\n\n"), &values) + "\n";

    let mut tool_payload = Map::new();
    for s in sops.iter().filter(|s| s.delivery != "prompt") {
        let mut payload = sop_payload(s);
        fill_json(&mut payload, &values);
        tool_payload.insert(s.id.clone(), payload);
    }
    let tools: BTreeSet<&str> = sops.iter().flat_map(|s| sop_tools(s)).collect();
    Rendered {
        agent: agent.clone(),
        prompt,
        bases: bases.into_iter().cloned().collect(),
        sops: sops.into_iter().cloned().collect(),
        tool_payload,
        tools: tools.into_iter().map(String::from).collect(),
    }
}

/// Writes <agent>.prompt.md, <agent>.tool.json and lock.json into `out`. The files are written
/// to a temp folder next to it and then renamed into place (lock.json last), so an error or
/// Ctrl-C never leaves a half-written file or a missing prompt. Files the previous build listed in
/// its lock.json that this one doesn't make are removed; nothing else in `out` is touched. A
/// non-empty `out` without lock.json is refused unless `force`. Returns the number of files written.
pub fn write_build(build: &Build, out: &Path, force: bool) -> anyhow::Result<usize> {
    let previous = previous_build(out, force)?;
    let mut files = vec![];
    for (id, r) in &build.agents {
        files.push((format!("{id}.prompt.md"), r.prompt.clone()));
        if !r.tool_payload.is_empty() {
            files.push((format!("{id}.tool.json"), pretty_json(&Json::Object(r.tool_payload.clone()), false) + "\n"));
        }
    }
    let lock = pretty_json(&build.lock(), false) + "\n";
    let abs = std::path::absolute(out)?;
    let (Some(parent), Some(name)) = (abs.parent(), abs.file_name()) else {
        anyhow::bail!("can't write the build to {}", out.display())
    };
    let tmp = parent.join(format!(".{}.tmp", name.to_string_lossy()));
    let _ = std::fs::remove_dir_all(&tmp);
    let staged = std::fs::create_dir_all(&tmp)
        .and_then(|()| files.iter().try_for_each(|(name, text)| std::fs::write(tmp.join(name), text)))
        .and_then(|()| std::fs::write(tmp.join("lock.json"), &lock))
        .and_then(|()| std::fs::create_dir_all(out));
    if let Err(e) = staged {
        let _ = std::fs::remove_dir_all(&tmp);
        return Err(anyhow::Error::new(e).context(format!("can't write the build to {}", out.display())));
    }
    for (name, _) in &files {
        std::fs::rename(tmp.join(name), out.join(name))?;
    }
    for name in previous.iter().filter(|n| !files.iter().any(|(f, _)| f == *n)) {
        match std::fs::remove_file(out.join(name)) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
            _ => {}
        }
    }
    std::fs::rename(tmp.join("lock.json"), out.join("lock.json"))?;
    std::fs::remove_dir(&tmp)?;
    Ok(files.len() + 1)
}

/// The prompt and tool files the build in `dir` made, from its lock.json. A folder that has files
/// but no lock.json wasn't built by sopc: an error unless `force` (then nothing is removed).
fn previous_build(dir: &Path, force: bool) -> anyhow::Result<Vec<String>> {
    let lock = std::fs::read_to_string(dir.join("lock.json")).ok().and_then(|t| serde_json::from_str::<Json>(&t).ok());
    let Some(lock) = lock else {
        let empty = std::fs::read_dir(dir).map_or(true, |mut entries| entries.next().is_none());
        if !empty && !force {
            anyhow::bail!(
                "{} has files but no lock.json, so sopc didn't build it; use an empty or new folder, \
                 or pass --force to write into it (only the files sopc builds are overwritten)",
                dir.display()
            );
        }
        return Ok(vec![]);
    };
    let ids = lock["agents"].as_object().into_iter().flat_map(|a| a.keys());
    Ok(ids.flat_map(|id| [format!("{id}.prompt.md"), format!("{id}.tool.json")]).collect())
}
