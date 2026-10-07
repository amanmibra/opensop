//! Deterministic text analysis for importing and reviewing prompts: `overlap`, `compare` and
//! `lint`. Prompts are split into sentence-sized units and compared by their words.

use crate::model::{Agent, Block};
use crate::render::{fill_variables, find_variables, render_step, resolve_blocks, Build, StepKind};
use crate::text::quote;
use crate::workspace::Workspace;
use regex::Regex;
use serde_json::{json, Value as Json};
use similar::{capture_diff_slices, Algorithm, DiffOp, DiffTag};
use std::collections::{BTreeMap, BTreeSet, HashSet};
use std::sync::LazyLock;

static LIST_MARKER: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^\s*(?:[-*+]|\d+[.)])\s+").unwrap());
static HEADING: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^\s*#{1,6}\s+").unwrap());
static LABEL_ONLY: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^[A-Za-z][A-Za-z ]{0,30}:$").unwrap());
static SOPC_PREFIX: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^(?:Goal|When this applies):\s+").unwrap());
static WORD: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"[a-z0-9]+(?:'[a-z]+)?").unwrap());
static NUMBER: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"[0-9]+(?:[.:][0-9]+)?").unwrap());
/// Lines sopc itself adds to SOPs, which originals never contain.
static GENERATED: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^(?:Use the `[^`]+` tool\.|This applies to the `[^`]+` tool\.|Before following this procedure, call the `get_sop` tool.*)$").unwrap()
});

const NEGATIONS: &[&str] =
    &["not", "never", "no", "don't", "dont", "doesn't", "cannot", "can't", "won't", "without", "nothing"];
const STOPWORDS: &[&str] = &[
    "a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "has", "have", "if", "in", "is", "it", "its", "of",
    "on", "or", "so", "that", "the", "their", "them", "they", "this", "to", "was", "were", "when", "with", "you",
    "your",
];

/// The shortest unit worth comparing, in words.
const MIN_WORDS: usize = 3;

/// Splits a line after `.`, `!` or `?` followed by whitespace and a capital, digit, quote or `(`.
fn split_sentences(line: &str) -> Vec<&str> {
    let (mut out, mut start) = (vec![], 0);
    let chars: Vec<(usize, char)> = line.char_indices().collect();
    let mut i = 1;
    while i < chars.len() {
        if !chars[i].1.is_whitespace() || !matches!(chars[i - 1].1, '.' | '!' | '?') {
            i += 1;
            continue;
        }
        let mut j = i;
        while j < chars.len() && chars[j].1.is_whitespace() {
            j += 1;
        }
        if j < chars.len()
            && (chars[j].1.is_ascii_uppercase() || chars[j].1.is_ascii_digit() || "\"'(".contains(chars[j].1))
        {
            out.push(&line[start..chars[i].0]);
            start = chars[j].0;
        }
        i = j;
    }
    out.push(&line[start..]);
    out
}

/// Prompt text as sentences, list items and lines; headings and bare labels are dropped.
pub fn units(text: &str) -> Vec<String> {
    let mut out = vec![];
    for line in text.lines().map(str::trim) {
        if line.is_empty() || HEADING.is_match(line) || LABEL_ONLY.is_match(line) {
            continue;
        }
        let line = LIST_MARKER.replace(line, "");
        let line = SOPC_PREFIX.replace(&line, "");
        out.extend(split_sentences(&line).into_iter().map(str::trim).filter(|s| !s.is_empty()).map(String::from));
    }
    out.retain(|u| words(u).len() >= MIN_WORDS);
    out
}

/// The lowercased words of text.
pub fn words(text: &str) -> Vec<String> {
    let lower = text.to_lowercase().replace('’', "'");
    WORD.find_iter(&lower).map(|m| m.as_str().to_string()).collect()
}

pub fn norm(text: &str) -> String {
    words(text).join(" ")
}

/// 2 × matches / total, as difflib's SequenceMatcher.ratio() (matches from a Myers diff).
fn ratio<T: PartialEq + Eq + std::hash::Hash + Ord>(a: &[T], b: &[T]) -> f64 {
    if a.is_empty() && b.is_empty() {
        return 1.0;
    }
    let ops = capture_diff_slices(Algorithm::Myers, a, b);
    let matches: usize = ops.iter().map(|op| if let DiffOp::Equal { len, .. } = op { *len } else { 0 }).sum();
    2.0 * matches as f64 / (a.len() + b.len()) as f64
}

pub fn similarity(a: &str, b: &str) -> f64 {
    ratio(&words(a), &words(b))
}

/// The words that differ between two near-identical sentences.
pub fn differing_words(a: &str, b: &str) -> (String, String) {
    let (wa, wb): (Vec<&str>, Vec<&str>) = (a.split_whitespace().collect(), b.split_whitespace().collect());
    let key = |ws: &[&str]| -> Vec<String> {
        ws.iter().map(|w| w.trim_matches(|c| ".,;:!?\"'".contains(c)).to_lowercase()).collect()
    };
    let (mut da, mut db) = (vec![], vec![]);
    for op in capture_diff_slices(Algorithm::Myers, &key(&wa), &key(&wb)) {
        let (tag, old, new) = op.as_tag_tuple();
        if tag != DiffTag::Equal {
            da.extend_from_slice(&wa[old]);
            db.extend_from_slice(&wb[new]);
        }
    }
    let trim = |ws: Vec<&str>| ws.join(" ").trim_matches(|c| ".,;:!?".contains(c)).to_string();
    (trim(da), trim(db))
}

fn dedupe(items: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::new();
    items.into_iter().filter(|u| seen.insert(norm(u))).collect()
}

// --- overlap ---------------------------------------------------------------------------------------

/// The same sentence with a different value in some prompts. Both maps are by agent.
pub struct NearCopy {
    pub variants: BTreeMap<String, String>,
    pub differing: BTreeMap<String, String>,
}

/// What a set of prompts shares.
pub struct Overlap {
    pub agents: Vec<String>,
    pub shared: Vec<(Vec<String>, String)>, // (agents, sentence)
    pub near_copies: Vec<NearCopy>,
    units: BTreeMap<String, Vec<String>>,
}

/// Finds what prompts share; sentences at least `near` similar are near-copies.
pub fn overlap(prompts: &BTreeMap<String, String>, near: f64) -> Overlap {
    let agents: Vec<String> = prompts.keys().cloned().collect();
    let per_agent: BTreeMap<String, Vec<String>> = prompts.iter().map(|(a, p)| (a.clone(), dedupe(units(p)))).collect();

    // norm → [(agent, sentence)], in first-seen order
    let mut exact: Vec<(String, Vec<(String, String)>)> = vec![];
    for (agent, us) in &per_agent {
        for u in us {
            let key = norm(u);
            match exact.iter_mut().find(|(k, _)| *k == key) {
                Some((_, list)) => list.push((agent.clone(), u.clone())),
                None => exact.push((key, vec![(agent.clone(), u.clone())])),
            }
        }
    }
    let shared: Vec<(Vec<String>, String)> = exact
        .iter()
        .filter(|(_, l)| l.len() > 1)
        .map(|(_, l)| (l.iter().map(|(a, _)| a.clone()).collect(), l[0].1.clone()))
        .collect();

    let singles: Vec<&(String, Vec<(String, String)>)> = exact.iter().filter(|(_, l)| l.len() < agents.len()).collect();
    let mut used: HashSet<&str> = HashSet::new();
    let mut near_copies = vec![];
    for (i, (key, list)) in singles.iter().map(|e| (&e.0, &e.1)).enumerate() {
        if used.contains(key.as_str()) {
            continue;
        }
        // Variants in the order they were merged: it decides which one each is compared with.
        let mut variants: Vec<(String, String)> = list.clone();
        for (other_key, other) in singles[i + 1..].iter().map(|e| (&e.0, &e.1)) {
            if used.contains(other_key.as_str()) || other.iter().any(|(a, _)| variants.iter().any(|(b, _)| a == b)) {
                continue;
            }
            if similarity(key, other_key) >= near {
                variants.extend(other.iter().cloned());
                used.insert(other_key);
            }
        }
        let norms: HashSet<String> = variants.iter().map(|(_, v)| norm(v)).collect();
        if variants.len() > 1 && norms.len() > 1 {
            used.insert(key);
            let differing = variants
                .iter()
                .map(|(agent, text)| {
                    let other = variants.iter().find(|(a, _)| a != agent).map_or("", |(_, t)| t.as_str());
                    (agent.clone(), differing_words(other, text).1)
                })
                .collect();
            let variants = variants.into_iter().collect();
            near_copies.push(NearCopy { variants, differing });
        }
    }
    let shared = shared.into_iter().filter(|(_, text)| !used.contains(norm(text).as_str())).collect();
    Overlap { agents, shared, near_copies, units: per_agent }
}

impl Overlap {
    pub fn text(&self) -> String {
        let mut out = vec![format!("{} prompts: {}", self.agents.len(), self.agents.join(", ")), String::new()];
        let mut groups: Vec<(&Vec<String>, Vec<&str>)> = vec![];
        for (agents, text) in &self.shared {
            match groups.iter_mut().find(|(a, _)| *a == agents) {
                Some((_, texts)) => texts.push(text),
                None => groups.push((agents, vec![text])),
            }
        }
        groups.sort_by(|(a, _), (b, _)| b.len().cmp(&a.len()).then_with(|| a.cmp(b)));
        for (agents, texts) in groups {
            let label = if agents.len() == self.agents.len() { "ALL agents".to_string() } else { agents.join(", ") };
            out.push(format!("Shared by {label} ({} sentences):", texts.len()));
            out.extend(texts.iter().map(|t| format!("  - {t}")));
            out.push(String::new());
        }
        if !self.near_copies.is_empty() {
            out.push("Near-copies (same sentence, different value; use a {{placeholder}}):".into());
            for nc in &self.near_copies {
                out.push(format!("  - {}", nc.variants.values().next().unwrap()));
                out.extend(nc.differing.iter().map(|(a, d)| format!("      {a}: {}", quote(d))));
            }
            out.push(String::new());
        }
        let mut covered: HashSet<(&str, String)> = HashSet::new();
        for (agents, text) in &self.shared {
            covered.extend(agents.iter().map(|a| (a.as_str(), norm(text))));
        }
        for nc in &self.near_copies {
            covered.extend(nc.variants.iter().map(|(a, v)| (a.as_str(), norm(v))));
        }
        let only: Vec<String> = self
            .agents
            .iter()
            .map(|a| {
                format!("{a} {}", self.units[a].iter().filter(|u| !covered.contains(&(a.as_str(), norm(u)))).count())
            })
            .collect();
        out.push(format!("Only in one prompt (sentences): {}", only.join(", ")));
        out.join("\n") + "\n"
    }
}

// --- compare ---------------------------------------------------------------------------------------

/// How one rendered prompt covers its original.
#[derive(Debug, Default)]
pub struct Comparison {
    pub agent: String,
    pub total: usize,
    pub missing: Vec<String>,
    pub changed: Vec<(String, String)>, // (original, rendered)
    pub reworded: Vec<String>,
    pub added: Vec<String>,
}

impl Comparison {
    /// The share of original sentences kept.
    pub fn coverage(&self) -> f64 {
        if self.total == 0 {
            return 1.0;
        }
        (self.total - self.missing.len() - self.changed.len()) as f64 / self.total as f64
    }
    /// Nothing was lost or changed.
    pub fn ok(&self) -> bool {
        self.missing.is_empty() && self.changed.is_empty()
    }
}

fn found(unit: &str, pool: &[String], threshold: f64) -> bool {
    let n = norm(unit);
    pool.iter().any(|p| {
        let np = norm(p);
        n == np || np.contains(&n) || similarity(unit, p) >= threshold
    })
}

/// The share of a unit's content words found in `pool`.
fn word_coverage(unit: &str, pool: &HashSet<String>) -> f64 {
    let content: Vec<String> = words(unit).into_iter().filter(|w| !STOPWORDS.contains(&w.as_str())).collect();
    if content.is_empty() {
        return 1.0;
    }
    content.iter().filter(|w| pool.contains(*w)).count() as f64 / content.len() as f64
}

/// Whether the unit's words are spread over several rendered units.
fn split_across(unit: &str, pool: &[String]) -> bool {
    let unit_words: HashSet<String> = words(unit).into_iter().collect();
    let parts: Vec<&String> =
        pool.iter().filter(|p| !GENERATED.is_match(p) && word_coverage(p, &unit_words) >= 0.6).collect();
    if parts.len() < 2 {
        return false;
    }
    let union: HashSet<String> = parts.iter().flat_map(|p| words(p)).collect();
    word_coverage(unit, &union) >= 0.7
}

/// Checks each rendered prompt still says everything its original said.
pub fn compare(build: &Build, originals: &BTreeMap<String, String>, threshold: f64) -> Vec<Comparison> {
    let mut results = vec![];
    for (agent, original) in originals {
        let rendered = build.agents.get(agent).map(|r| units(&r.prompt)).unwrap_or_default();
        let original_units = units(original);
        let mut c = Comparison { agent: agent.clone(), total: original_units.len(), ..Comparison::default() };
        for u in &original_units {
            if found(u, &rendered, threshold) {
                continue;
            }
            let closest =
                rendered.iter().map(|p| (p, similarity(u, p))).fold(None, |best: Option<(&String, f64)>, (p, s)| {
                    match best {
                        Some((_, b)) if b >= s => best,
                        _ => Some((p, s)),
                    }
                });
            if split_across(u, &rendered) {
                c.reworded.push(u.clone());
            } else if let Some((p, _)) = closest.filter(|(_, s)| *s >= 0.5) {
                c.changed.push((u.clone(), p.clone()));
            } else {
                c.missing.push(u.clone());
            }
        }
        let original_words: HashSet<String> = words(original).into_iter().collect();
        for u in &rendered {
            if !GENERATED.is_match(u)
                && !found(u, &original_units, threshold)
                && word_coverage(u, &original_words) < 0.9
            {
                c.added.push(u.clone());
            }
        }
        results.push(c);
    }
    results
}

pub fn compare_text(results: &[Comparison], build: &Build) -> String {
    let mut out = vec![];
    for r in results {
        let status = if r.ok() { "ok" } else { "LOST OR CHANGED TEXT" };
        out.push(format!("{}: {:.0}% of {} sentences kept ({status})", r.agent, r.coverage() * 100.0, r.total));
        out.extend(r.missing.iter().map(|u| format!("  - missing:  {u}")));
        for (original, rendered) in &r.changed {
            let (was, now) = differing_words(original, rendered);
            out.push(format!("  ! changed:  {original}"));
            out.push(format!("              now: {rendered}   ({} → {})", quote(&was), quote(&now)));
        }
        out.extend(r.reworded.iter().map(|u| format!("  ~ reworded: {u}")));
        out.extend(r.added.iter().map(|u| format!("  + added:    {u}")));
    }
    let unmatched: Vec<&str> =
        build.agents.keys().filter(|id| !results.iter().any(|r| &r.agent == *id)).map(String::as_str).collect();
    if !unmatched.is_empty() {
        out.push(format!("no original for: {}", unmatched.join(", ")));
    }
    out.join("\n") + "\n"
}

// --- lint ------------------------------------------------------------------------------------------

/// A duplicate or mechanical conflict. Sources are (block label, text).
#[derive(Debug)]
pub struct Finding {
    pub code: &'static str,
    pub message: String,
    pub sources: Vec<(String, String)>,
    pub agents: Vec<String>,
}

impl Finding {
    pub fn to_json(&self) -> Json {
        let sources: Vec<Json> =
            self.sources.iter().map(|(block, text)| json!({"block": block, "text": text})).collect();
        json!({"code": self.code, "message": self.message, "sources": sources, "agents": self.agents})
    }
}

fn conflict(a: &str, b: &str) -> Option<&'static str> {
    let (na, nb) = (norm(a), norm(b));
    if na == nb {
        return Some("duplicate_text");
    }
    if numbers_differ_in_same_sentence(&na, &nb) {
        return Some("numeric_conflict");
    }
    let (wa, wb) = (words(a), words(b));
    let negated = |ws: &[String]| ws.iter().any(|w| NEGATIONS.contains(&w.as_str()));
    let strip = |ws: &[String]| -> Vec<String> {
        ws.iter().filter(|w| !NEGATIONS.contains(&w.as_str()) && *w != "always").cloned().collect()
    };
    let (sa, sb) = (strip(&wa), strip(&wb));
    if negated(&wa) != negated(&wb) && !sa.is_empty() && ratio(&sa, &sb) >= 0.9 {
        return Some("negation_conflict");
    }
    if similarity(a, b) >= 0.85 {
        return Some("near_duplicate");
    }
    None
}

/// Number words that rules use for counts, as digits.
const NUMBER_WORDS: &[(&str, &str)] = &[
    ("once", "1"),
    ("twice", "2"),
    ("thrice", "3"),
    ("one", "1"),
    ("two", "2"),
    ("three", "3"),
    ("four", "4"),
    ("five", "5"),
    ("six", "6"),
    ("seven", "7"),
    ("eight", "8"),
    ("nine", "9"),
    ("ten", "10"),
];

/// Normalized text with number words written as digits.
fn numerals(norm: &str) -> String {
    let words = norm.split(' ').map(|w| NUMBER_WORDS.iter().find(|(n, _)| *n == w).map_or(w, |(_, d)| d));
    words.collect::<Vec<_>>().join(" ")
}

fn numbers_differ_in_same_sentence(na: &str, nb: &str) -> bool {
    let (na, nb) = (&numerals(na), &numerals(nb));
    let nums = |s: &str| -> Vec<String> { NUMBER.find_iter(s).map(|m| m.as_str().to_string()).collect() };
    let (numbers_a, numbers_b) = (nums(na), nums(nb));
    if numbers_a.is_empty() || numbers_b.is_empty() || numbers_a == numbers_b {
        return false;
    }
    let (ma, mb) = (NUMBER.replace_all(na, "#").into_owned(), NUMBER.replace_all(nb, "#").into_owned());
    let (short, long) = if mb.chars().count() < ma.chars().count() { (&mb, &ma) } else { (&ma, &mb) };
    if ma == mb || (short.split_whitespace().count() >= 3 && format!(" {long} ").contains(&format!(" {short} "))) {
        return true;
    }
    let fields = |s: &str| -> Vec<String> { s.split_whitespace().map(String::from).collect() };
    ratio(&fields(&ma), &fields(&mb)) >= 0.8
}

/// (block label, text) for every block an agent uses; `split` breaks texts into units.
fn agent_texts(ws: &Workspace, agent: &Agent, split: bool) -> Vec<(String, String)> {
    let mut out = vec![];
    let mut add = |label: String, text: &str| {
        if split {
            out.extend(units(text).into_iter().map(|u| (label.clone(), u)));
        } else {
            out.push((label, text.to_string()));
        }
    };
    add(format!("agent `{}`", agent.id), &agent.context);
    for block in resolve_blocks(ws, agent) {
        let s = match block {
            Block::Instruction(i) => {
                add(format!("instruction `{}`", i.id), &i.text);
                continue;
            }
            Block::Sop(s) => s,
        };
        let mut parts = vec![s.description.clone(), s.scope.clone(), s.guidance.clone()];
        for (list, kind) in [
            (&s.procedure_steps, StepKind::Step),
            (&s.forbidden_actions, StepKind::Forbidden),
            (&s.warning_signs, StepKind::Warning),
        ] {
            parts.extend(list.iter().map(|st| render_step(st, kind)));
        }
        parts.retain(|p| !p.is_empty());
        add(format!("SOP `{}`", s.id), &parts.join("\n"));
    }
    out
}

/// Duplicated text and mechanical conflicts within each agent's prompt, and unused variables.
pub fn lint(ws: &Workspace) -> Vec<Finding> {
    let mut findings: Vec<(String, Finding)> = vec![];
    let mut agents: Vec<&Agent> = ws.agents.iter().collect();
    agents.sort_by(|a, b| a.id.cmp(&b.id));
    for agent in agents {
        let values = ws.config.variables.merged(&agent.variables);
        let sourced: Vec<(String, String)> =
            agent_texts(ws, agent, true).into_iter().map(|(b, t)| (b, fill_variables(&t, &values))).collect();
        for (i, a) in sourced.iter().enumerate() {
            for b in &sourced[i + 1..] {
                let Some(code) = conflict(&a.1, &b.1) else { continue };
                if code == "near_duplicate" && a.0 == b.0 {
                    continue;
                }
                let key = [code, &a.0, &norm(&a.1), &b.0, &norm(&b.1)].join("\0");
                match findings.iter_mut().find(|(k, _)| *k == key) {
                    Some((_, f)) => f.agents.push(agent.id.clone()),
                    None => {
                        let message = match code {
                            "duplicate_text" => "Same sentence appears twice in the prompt",
                            "numeric_conflict" => "Same sentence with different numbers",
                            "negation_conflict" => "One block says it, another says the opposite",
                            _ => "Nearly identical sentences; a copy that drifted?",
                        };
                        let f = Finding {
                            code,
                            message: message.into(),
                            sources: vec![a.clone(), b.clone()],
                            agents: vec![agent.id.clone()],
                        };
                        findings.push((key, f));
                    }
                }
            }
        }
        let used: BTreeSet<String> =
            agent_texts(ws, agent, false).iter().flat_map(|(_, t)| find_variables(t)).collect();
        let unused: BTreeSet<&String> =
            agent.variables.0.iter().map(|(k, _)| k).filter(|k| !used.contains(*k)).collect();
        for name in unused {
            let key = format!("unused_variable\0{}\0{name}", agent.id);
            if !findings.iter().any(|(k, _)| *k == key) {
                findings.push((
                    key,
                    Finding {
                        code: "unused_variable",
                        message: format!("'{name}' is set but no block this agent uses mentions {{{{{name}}}}}"),
                        sources: vec![(format!("agent `{}`", agent.id), name.clone())],
                        agents: vec![agent.id.clone()],
                    },
                ));
            }
        }
    }
    findings.into_iter().map(|(_, f)| f).collect()
}

pub fn lint_text(findings: &[Finding]) -> String {
    if findings.is_empty() {
        return "No duplicates or mechanical conflicts found.\n".into();
    }
    let mut out =
        vec![format!("{} finding(s). These are advisory; decide which text is right.", findings.len()), String::new()];
    for f in findings {
        out.push(format!("[{}] {} (agents: {})", f.code, f.message, f.agents.join(", ")));
        out.extend(f.sources.iter().map(|(block, text)| format!("    {block}: {text}")));
        out.push(String::new());
    }
    out.join("\n")
}
