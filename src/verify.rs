//! `sopc verify`: fetches each agent's live prompt from its platform and compares it with the
//! compiled one, to catch hotfixes made in a platform's dashboard that git doesn't have.
//!
//! Read-only: only GET requests are sent. Base URLs can be overridden for tests and proxies
//! (`SOPC_ELEVENLABS_URL`, `SOPC_VAPI_URL`, `SOPC_RETELL_URL`).

use crate::plan::labelled_diff;
use crate::render::Build;
use serde_json::{json, Value as Json};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Mutex;
use std::time::Duration;

const TIMEOUT: Duration = Duration::from_secs(20);
/// Requests in flight at once.
const WORKERS: usize = 8;

/// Where a platform's API is and how to authenticate with it.
struct Api {
    name: &'static str,
    key_var: &'static str,
    url_var: &'static str,
    default_url: &'static str,
}

const ELEVENLABS: Api = Api {
    name: "ElevenLabs",
    key_var: "ELEVENLABS_API_KEY",
    url_var: "SOPC_ELEVENLABS_URL",
    default_url: "https://api.elevenlabs.io",
};
const VAPI: Api =
    Api { name: "Vapi", key_var: "VAPI_API_KEY", url_var: "SOPC_VAPI_URL", default_url: "https://api.vapi.ai" };
const RETELL: Api = Api {
    name: "Retell",
    key_var: "RETELL_API_KEY",
    url_var: "SOPC_RETELL_URL",
    default_url: "https://api.retellai.com",
};

#[derive(Clone, Copy, PartialEq, Debug)]
pub enum Status {
    InSync,
    Drifted,
    NotComparable,
    Skipped,
    Error,
}

impl Status {
    pub fn as_str(self) -> &'static str {
        match self {
            Status::InSync => "in_sync",
            Status::Drifted => "drifted",
            Status::NotComparable => "not_comparable",
            Status::Skipped => "skipped",
            Status::Error => "error",
        }
    }
    fn label(self) -> &'static str {
        match self {
            Status::InSync => "in sync",
            Status::Drifted => "drifted",
            Status::NotComparable => "not comparable",
            Status::Skipped => "skipped",
            Status::Error => "error",
        }
    }
}

/// One agent's result.
pub struct Check {
    pub id: String,
    pub platform: String,
    pub platform_id: String,
    pub status: Status,
    pub reason: Option<String>,
    /// Compiled → live, when drifted.
    pub diff: Option<String>,
}

/// What a platform holds for an agent.
#[derive(Debug, PartialEq)]
pub enum Live {
    Prompt(String),
    /// The agent's prompt can't be compared as one text (why).
    NotComparable(String),
}

pub struct Report {
    pub checks: Vec<Check>,
}

impl Report {
    pub fn ok(&self) -> bool {
        !self.checks.iter().any(|c| matches!(c.status, Status::Drifted | Status::Error))
    }

    fn count(&self, status: Status) -> usize {
        self.checks.iter().filter(|c| c.status == status).count()
    }

    /// e.g. "4 in sync, 1 drifted, 1 skipped".
    pub fn summary(&self) -> String {
        let all = [Status::InSync, Status::Drifted, Status::NotComparable, Status::Skipped, Status::Error];
        let parts: Vec<String> = all
            .into_iter()
            .filter(|s| self.count(*s) > 0)
            .map(|s| {
                let n = self.count(s);
                if s == Status::Error && n > 1 {
                    format!("{n} errors")
                } else {
                    format!("{n} {}", s.label())
                }
            })
            .collect();
        if parts.is_empty() {
            "no agents to verify".into()
        } else {
            parts.join(", ")
        }
    }

    pub fn text(&self) -> String {
        let mut out = vec![];
        for c in &self.checks {
            let reason = c.reason.as_ref().map(|r| format!(": {r}")).unwrap_or_default();
            out.push(format!("{:<24} {:<11} {:<28} {}{reason}", c.id, c.platform, c.platform_id, c.status.label()));
        }
        for c in self.checks.iter().filter(|c| c.diff.is_some()) {
            out.extend([String::new(), c.diff.as_deref().unwrap_or_default().trim_end().to_string()]);
        }
        out.extend([String::new(), self.summary()]);
        out.join("\n") + "\n"
    }

    pub fn to_json(&self) -> Json {
        let agents: Vec<Json> = self
            .checks
            .iter()
            .map(|c| {
                json!({
                    "id": c.id, "platform": c.platform, "platform_id": c.platform_id,
                    "platform_ref": format!("{}:{}", c.platform, c.platform_id),
                    "status": c.status.as_str(), "reason": c.reason, "diff": c.diff,
                })
            })
            .collect();
        let summary = json!({
            "in_sync": self.count(Status::InSync), "drifted": self.count(Status::Drifted),
            "not_comparable": self.count(Status::NotComparable), "skipped": self.count(Status::Skipped),
            "error": self.count(Status::Error),
        });
        json!({"ok": self.ok(), "summary": summary, "agents": agents})
    }
}

/// Line endings made `\n` and trailing whitespace at the end removed; one final newline.
pub fn normalize(text: &str) -> String {
    text.replace("\r\n", "\n").replace('\r', "\n").trim_end().to_string() + "\n"
}

/// Checks `ids` (every agent when empty), several at a time; results keep the order of `ids`.
pub fn verify(build: &Build, ids: &[String]) -> Report {
    let ids: Vec<&String> = if ids.is_empty() { build.agents.keys().collect() } else { ids.iter().collect() };
    let http = client();
    let next = AtomicUsize::new(0);
    let results: Mutex<Vec<Option<Check>>> = Mutex::new((0..ids.len()).map(|_| None).collect());
    std::thread::scope(|s| {
        for _ in 0..WORKERS.min(ids.len()) {
            s.spawn(|| loop {
                let i = next.fetch_add(1, Ordering::SeqCst);
                let Some(id) = ids.get(i) else { break };
                let check = check(&http, build, id);
                results.lock().unwrap()[i] = Some(check);
            });
        }
    });
    Report { checks: results.into_inner().unwrap().into_iter().flatten().collect() }
}

fn check(http: &ureq::Agent, build: &Build, id: &str) -> Check {
    let r = &build.agents[id];
    let (platform, platform_id) = (r.agent.platform(), r.agent.platform_id());
    let mut check = Check {
        id: id.to_string(),
        platform: platform.to_string(),
        platform_id: platform_id.to_string(),
        status: Status::Error,
        reason: None,
        diff: None,
    };
    let live = match platform {
        "elevenlabs" => fetch_elevenlabs(http, platform_id),
        "vapi" => fetch_vapi(http, platform_id),
        "retell" => fetch_retell(http, platform_id),
        _ => {
            check.status = Status::Skipped;
            check.reason = Some("LiveKit: your code loads the prompt; nothing to fetch".into());
            return check;
        }
    };
    match live {
        Err(e) => check.reason = Some(e),
        Ok(Live::NotComparable(why)) => {
            check.status = Status::NotComparable;
            check.reason = Some(why);
        }
        Ok(Live::Prompt(live)) => {
            let (compiled, live) = (normalize(&r.prompt), normalize(&live));
            if compiled == live {
                check.status = Status::InSync;
            } else {
                check.status = Status::Drifted;
                let old = format!("compiled/{id}.prompt.md");
                let new = format!("live/{}", r.agent.platform_ref());
                check.diff = Some(labelled_diff(&compiled, &live, &old, &new));
            }
        }
    }
    check
}

// --- HTTP ------------------------------------------------------------------------------------

fn client() -> ureq::Agent {
    let config = ureq::Agent::config_builder()
        .timeout_global(Some(TIMEOUT))
        .http_status_as_error(false)
        .user_agent(concat!("sopc/", env!("CARGO_PKG_VERSION")))
        .build();
    config.into()
}

/// GETs `path` from a platform and parses the JSON body. Errors are one plain line; the key is
/// never part of them.
fn get(http: &ureq::Agent, api: &Api, path: &str, header: &str, prefix: &str) -> Result<Json, String> {
    let key = std::env::var(api.key_var).unwrap_or_default();
    if key.trim().is_empty() {
        return Err(format!("{} is not set (needed to read {} agents)", api.key_var, api.name));
    }
    let base = std::env::var(api.url_var).ok().filter(|u| !u.trim().is_empty());
    let base = base.as_deref().unwrap_or(api.default_url).trim_end_matches('/').to_string();
    let url = format!("{base}{path}");
    let mut resp = http
        .get(&url)
        .header(header, &format!("{prefix}{}", key.trim()))
        .header("Accept", "application/json")
        .call()
        .map_err(|e| format!("can't reach {} ({base}): {e}", api.name))?;
    let status = resp.status().as_u16();
    let body = resp.body_mut().read_to_string().unwrap_or_default();
    match status {
        200..=299 => {}
        401 | 403 => return Err(format!("{} refused the key (HTTP {status}); check {}", api.name, api.key_var)),
        404 => return Err(format!("not found on {} (HTTP 404)", api.name)),
        _ => return Err(format!("{} returned HTTP {status}", api.name)),
    }
    serde_json::from_str(&body).map_err(|e| format!("{} returned a response sopc can't read: {e}", api.name))
}

/// A path segment with anything but unreserved characters percent-encoded.
fn segment(s: &str) -> String {
    s.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => (b as char).to_string(),
            _ => format!("%{b:02X}"),
        })
        .collect()
}

fn fetch_elevenlabs(http: &ureq::Agent, id: &str) -> Result<Live, String> {
    let agent = get(http, &ELEVENLABS, &format!("/v1/convai/agents/{}", segment(id)), "xi-api-key", "")?;
    Ok(elevenlabs_prompt(&agent))
}

fn fetch_vapi(http: &ureq::Agent, id: &str) -> Result<Live, String> {
    let assistant = get(http, &VAPI, &format!("/assistant/{}", segment(id)), "Authorization", "Bearer ")?;
    Ok(vapi_prompt(&assistant))
}

fn fetch_retell(http: &ureq::Agent, id: &str) -> Result<Live, String> {
    let agent = get(http, &RETELL, &format!("/get-agent/{}", segment(id)), "Authorization", "Bearer ")?;
    let engine = &agent["response_engine"];
    match engine["type"].as_str().unwrap_or_default() {
        "retell-llm" => {}
        "conversation-flow" => return Ok(Live::NotComparable("multi-node (Retell conversation flow)".into())),
        "custom-llm" => return Ok(Live::NotComparable("custom LLM: the prompt lives in your LLM server".into())),
        other => return Err(format!("Retell agent has an unknown response engine `{other}`")),
    }
    let Some(llm_id) = engine["llm_id"].as_str() else {
        return Err("Retell agent has no llm_id".into());
    };
    // The agent is pinned to an LLM version; read that one.
    let version = engine["version"].as_u64().map(|v| format!("?version={v}")).unwrap_or_default();
    let llm = get(http, &RETELL, &format!("/get-retell-llm/{}{version}", segment(llm_id)), "Authorization", "Bearer ")?;
    Ok(retell_prompt(&llm))
}

// --- where each platform keeps the prompt ----------------------------------------------------------

/// `conversation_config.agent.prompt.prompt`, unless a workflow routes calls through other nodes.
pub fn elevenlabs_prompt(agent: &Json) -> Live {
    let nodes = agent["workflow"]["nodes"].as_object();
    // A workflow with only start and end nodes runs the main prompt; anything else is multi-node.
    let routed =
        nodes.into_iter().flatten().filter(|(_, n)| !matches!(n["type"].as_str(), Some("start" | "end"))).count();
    if routed > 0 {
        return Live::NotComparable(format!("multi-node (ElevenLabs workflow with {routed} node(s))"));
    }
    let prompt = agent["conversation_config"]["agent"]["prompt"]["prompt"].as_str();
    Live::Prompt(prompt.unwrap_or_default().to_string())
}

/// The content of the `system` message in `model.messages`.
pub fn vapi_prompt(assistant: &Json) -> Live {
    let messages = assistant["model"]["messages"].as_array();
    let system: Vec<&Json> = messages.into_iter().flatten().filter(|m| m["role"] == "system").collect();
    if system.len() > 1 {
        return Live::NotComparable(format!("{} system messages", system.len()));
    }
    let content = system.first().and_then(|m| m["content"].as_str());
    Live::Prompt(content.unwrap_or_default().to_string())
}

/// `general_prompt`, unless the LLM has states with their own prompts.
pub fn retell_prompt(llm: &Json) -> Live {
    let states = llm["states"].as_array();
    let with_prompts =
        states.into_iter().flatten().filter(|s| s["state_prompt"].as_str().is_some_and(|p| !p.trim().is_empty()));
    let n = with_prompts.count();
    if n > 0 {
        return Live::NotComparable(format!("multi-node (Retell LLM with {n} state prompt(s))"));
    }
    Live::Prompt(llm["general_prompt"].as_str().unwrap_or_default().to_string())
}
