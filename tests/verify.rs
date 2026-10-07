//! Tests of `sopc verify` against a mock of the platforms' APIs.

use serde_json::{json, Value as Json};
use std::collections::BTreeMap;
use std::io::{BufRead, BufReader, Write};
use std::net::TcpListener;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Arc, Mutex};

const KEYS: [(&str, &str); 3] =
    [("ELEVENLABS_API_KEY", "el-secret-123"), ("VAPI_API_KEY", "vapi-secret-456"), ("RETELL_API_KEY", "rt-secret-789")];

/// A request the mock received: "GET /path?query" and its auth header value.
type Seen = Arc<Mutex<Vec<(String, String)>>>;

/// Serves `routes` (path with query → (status, JSON body)) on a local port until the test ends.
fn mock(routes: BTreeMap<String, (u16, Json)>) -> (String, Seen) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    let seen: Seen = Arc::default();
    let log = seen.clone();
    std::thread::spawn(move || {
        for stream in listener.incoming() {
            let Ok(mut stream) = stream else { continue };
            let mut reader = BufReader::new(stream.try_clone().unwrap());
            let mut request = String::new();
            reader.read_line(&mut request).unwrap();
            let mut auth = String::new();
            loop {
                let mut line = String::new();
                if reader.read_line(&mut line).unwrap() == 0 || line.trim().is_empty() {
                    break;
                }
                let (name, value) = line.split_once(':').unwrap_or_default();
                if ["authorization", "xi-api-key"].contains(&name.to_ascii_lowercase().as_str()) {
                    auth = value.trim().to_string();
                }
            }
            let mut parts = request.split_whitespace();
            let (method, path) = (parts.next().unwrap_or_default(), parts.next().unwrap_or_default());
            log.lock().unwrap().push((format!("{method} {path}"), auth));
            let (status, body) = routes.get(path).cloned().unwrap_or((404, json!({"detail": "not found"})));
            let body = body.to_string();
            let head = format!(
                "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            let _ = stream.write_all(head.as_bytes()).and_then(|_| stream.write_all(body.as_bytes()));
        }
    });
    (url, seen)
}

struct Output {
    code: i32,
    stdout: String,
    stderr: String,
}

/// Runs `sopc verify` in `root` with every key set except `unset`, against `url` for every platform.
fn verify(root: &Path, url: &str, args: &[&str], unset: &[&str]) -> Output {
    let mut cmd = Command::new(env!("CARGO_BIN_EXE_sopc"));
    cmd.arg("verify").args(args).arg("--dir").arg(root);
    for var in ["HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"] {
        cmd.env_remove(var);
    }
    for (k, v) in KEYS {
        if unset.contains(&k) {
            cmd.env_remove(k);
        } else {
            cmd.env(k, v);
        }
    }
    for var in ["SOPC_ELEVENLABS_URL", "SOPC_VAPI_URL", "SOPC_RETELL_URL"] {
        cmd.env(var, url);
    }
    let out = cmd.output().unwrap();
    let out = Output {
        code: out.status.code().unwrap(),
        stdout: String::from_utf8(out.stdout).unwrap(),
        stderr: String::from_utf8(out.stderr).unwrap(),
    };
    for (_, key) in KEYS {
        assert!(!out.stdout.contains(key) && !out.stderr.contains(key), "a key was printed");
    }
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

/// Agents added to the fixture's three LiveKit ones: (sopc id, platform line).
const AGENTS: [(&str, &str); 7] = [
    ("el-sync", "elevenlabs: el_sync"),
    ("el-flow", "elevenlabs: el_flow"),
    ("vapi-drift", "vapi: asst_drift"),
    ("vapi-gone", "vapi: asst_gone"),
    ("retell-sync", "retell: agent_sync"),
    ("retell-states", "retell: agent_states"),
    ("retell-flow", "retell: agent_flow"),
];

/// The fixture with an agent per case, and each agent's compiled prompt.
fn workspace() -> (tempfile::TempDir, PathBuf, BTreeMap<String, String>) {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("sops");
    copy_dir(&PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/restaurants/sops"), &root);
    let template = std::fs::read_to_string(root.join("agents/luigis-trattoria.yaml")).unwrap();
    for (id, platform) in AGENTS {
        let text = template.replace("livekit: luigis-trattoria", platform);
        std::fs::write(root.join(format!("agents/{id}.yaml")), text).unwrap();
    }
    let out = dir.path().join("out");
    let status = Command::new(env!("CARGO_BIN_EXE_sopc")).arg("--dir").arg(&root).arg("-o").arg(&out).output();
    assert!(status.unwrap().status.success());
    let prompts = AGENTS
        .iter()
        .map(|(id, _)| (id.to_string(), std::fs::read_to_string(out.join(format!("{id}.prompt.md"))).unwrap()))
        .collect();
    (dir, root, prompts)
}

/// Each platform's API, answering for the agents in AGENTS.
fn routes(prompts: &BTreeMap<String, String>) -> BTreeMap<String, (u16, Json)> {
    let el = |prompt: &str, workflow: Json| json!({"agent_id": "x", "conversation_config": {"agent": {"prompt": {"prompt": prompt}}}, "workflow": workflow});
    // Windows line endings and trailing spaces don't count as drift.
    let crlf = prompts["el-sync"].replace('\n', "\r\n") + "  \r\n";
    let drifted = prompts["vapi-drift"].replace("Takeout and reservations", "Takeout only tonight");
    let flow = json!({
        "nodes": {"start_node": {"type": "start"}, "billing": {"type": "override_agent"}, "end_node": {"type": "end"}},
        "edges": {}
    });
    let retell_agent = |engine: Json| json!({"agent_id": "x", "response_engine": engine});
    let entries = [
        ("/v1/convai/agents/el_sync", 200, el(&crlf, json!({"nodes": {}, "edges": {}}))),
        ("/v1/convai/agents/el_flow", 200, el("anything", flow)),
        (
            "/assistant/asst_drift",
            200,
            json!({"id": "asst_drift", "model": {"messages": [{"role": "system", "content": drifted}]}}),
        ),
        ("/get-agent/agent_sync", 200, retell_agent(json!({"type": "retell-llm", "llm_id": "llm_sync", "version": 3}))),
        ("/get-retell-llm/llm_sync?version=3", 200, json!({"general_prompt": prompts["retell-sync"], "states": []})),
        ("/get-agent/agent_states", 200, retell_agent(json!({"type": "retell-llm", "llm_id": "llm_states"}))),
        (
            "/get-retell-llm/llm_states",
            200,
            json!({"general_prompt": "x", "states": [{"name": "a", "state_prompt": "Greet."}]}),
        ),
        ("/get-agent/agent_flow", 200, retell_agent(json!({"type": "conversation-flow", "conversation_flow_id": "f"}))),
    ];
    entries.into_iter().map(|(path, status, body)| (path.to_string(), (status, body))).collect()
}

fn line<'a>(out: &'a Output, id: &str) -> &'a str {
    let prefix = format!("{id} ");
    out.stdout.lines().find(|l| l.starts_with(&prefix)).unwrap_or_else(|| panic!("no {id} in {}", out.stdout))
}

#[test]
fn verify_reports_every_agent() {
    let (_dir, root, prompts) = workspace();
    let (url, seen) = mock(routes(&prompts));
    let out = verify(&root, &url, &[], &[]);
    assert_eq!(out.code, 1, "{}\n{}", out.stdout, out.stderr);
    assert!(line(&out, "el-sync").ends_with("in sync"), "{}", out.stdout);
    assert!(line(&out, "retell-sync").ends_with("in sync"), "{}", out.stdout);
    assert!(line(&out, "vapi-drift").ends_with("drifted"), "{}", out.stdout);
    assert!(line(&out, "vapi-gone").contains("error: not found on Vapi (HTTP 404)"), "{}", out.stdout);
    assert!(line(&out, "el-flow").contains("not comparable: multi-node (ElevenLabs workflow with 1 node(s))"));
    assert!(line(&out, "retell-states").contains("not comparable: multi-node (Retell LLM with 1 state prompt(s))"));
    assert!(line(&out, "retell-flow").contains("not comparable: multi-node (Retell conversation flow)"));
    assert!(line(&out, "tonys-pizza").contains("skipped: LiveKit"), "{}", out.stdout);
    // The drift is shown as a diff from the compiled prompt to the live one.
    assert!(out.stdout.contains("--- compiled/vapi-drift.prompt.md\n+++ live/vapi:asst_drift\n"), "{}", out.stdout);
    assert!(out.stdout.contains("\n+Luigi's is a sit-down Italian restaurant in Queens. Takeout only tonight"));
    assert!(out.stdout.ends_with("\n2 in sync, 1 drifted, 3 not comparable, 3 skipped, 1 error\n"), "{}", out.stdout);
    // Only GETs, with each platform's auth header.
    let seen = seen.lock().unwrap();
    assert!(seen.iter().all(|(req, _)| req.starts_with("GET ")), "{seen:?}");
    let auth = |path: &str| seen.iter().find(|(r, _)| r == &format!("GET {path}")).map(|(_, a)| a.clone());
    assert_eq!(auth("/v1/convai/agents/el_sync").as_deref(), Some("el-secret-123"));
    assert_eq!(auth("/assistant/asst_drift").as_deref(), Some("Bearer vapi-secret-456"));
    assert_eq!(auth("/get-retell-llm/llm_sync?version=3").as_deref(), Some("Bearer rt-secret-789"));
}

#[test]
fn verify_in_sync_exits_0() {
    let (_dir, root, prompts) = workspace();
    let (url, _) = mock(routes(&prompts));
    let out = verify(&root, &url, &["el-sync", "agent_sync", "el-flow", "tonys-pizza"], &[]);
    assert_eq!(out.code, 0, "{}\n{}", out.stdout, out.stderr);
    assert_eq!(out.stdout.lines().count(), 6, "{}", out.stdout); // 4 agents, a blank line, the summary
    assert!(out.stdout.ends_with("\n2 in sync, 1 not comparable, 1 skipped\n"), "{}", out.stdout);
}

#[test]
fn verify_filters_by_id_and_rejects_unknown_ones() {
    let (_dir, root, prompts) = workspace();
    let (url, seen) = mock(routes(&prompts));
    let out = verify(&root, &url, &["vapi:asst_drift"], &[]);
    assert_eq!(out.code, 1);
    assert!(out.stdout.ends_with("\n1 drifted\n"), "{}", out.stdout);
    assert_eq!(seen.lock().unwrap().len(), 1);
    let out = verify(&root, &url, &["nope"], &[]);
    assert_eq!(out.code, 1);
    assert!(out.stderr.contains("unknown agent(s): nope"), "{}", out.stderr);
}

#[test]
fn verify_names_a_missing_key() {
    let (_dir, root, prompts) = workspace();
    let (url, seen) = mock(routes(&prompts));
    let out = verify(&root, &url, &["el-sync", "retell-sync"], &["RETELL_API_KEY"]);
    assert_eq!(out.code, 1);
    assert!(line(&out, "el-sync").ends_with("in sync"));
    assert!(line(&out, "retell-sync").ends_with("error: RETELL_API_KEY is not set (needed to read Retell agents)"));
    assert!(seen.lock().unwrap().iter().all(|(r, _)| !r.contains("get-agent")), "no request without a key");
}

#[test]
fn verify_json_report() {
    let (_dir, root, prompts) = workspace();
    let (url, _) = mock(routes(&prompts));
    let out = verify(&root, &url, &["--json", "el-sync", "vapi-drift", "vapi-gone", "luigis-trattoria"], &[]);
    assert_eq!(out.code, 1);
    let report: Json = serde_json::from_str(&out.stdout).unwrap();
    assert_eq!(report["ok"], false);
    assert_eq!(report["summary"], json!({"in_sync": 1, "drifted": 1, "not_comparable": 0, "skipped": 1, "error": 1}));
    let agents = report["agents"].as_array().unwrap();
    let ids: Vec<&str> = agents.iter().map(|a| a["id"].as_str().unwrap()).collect();
    assert_eq!(ids, ["el-sync", "vapi-drift", "vapi-gone", "luigis-trattoria"]);
    assert_eq!(
        agents[0],
        json!({"id": "el-sync", "platform": "elevenlabs", "platform_id": "el_sync", "platform_ref": "elevenlabs:el_sync",
               "status": "in_sync", "reason": null, "diff": null})
    );
    assert_eq!(agents[1]["status"], "drifted");
    assert!(agents[1]["diff"].as_str().unwrap().starts_with("--- compiled/vapi-drift.prompt.md\n+++ live/"));
    assert_eq!(agents[2]["status"], "error");
    assert_eq!(agents[3]["status"], "skipped");
}

#[test]
fn verify_reports_an_unreachable_platform() {
    let (_dir, root, _) = workspace();
    // Nothing listens on a port that was just freed.
    let url = {
        let l = TcpListener::bind("127.0.0.1:0").unwrap();
        format!("http://{}", l.local_addr().unwrap())
    };
    let out = verify(&root, &url, &["vapi-drift"], &[]);
    assert_eq!(out.code, 1);
    assert!(line(&out, "vapi-drift").contains("error: can't reach Vapi"), "{}", out.stdout);
}
