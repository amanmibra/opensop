//! Small text helpers: hashes, JSON output, quoting and path display.

use serde_json::Value as Json;
use sha2::{Digest, Sha256};
use std::path::{Component, Path, PathBuf};

pub fn sha256_hex(text: &str) -> String {
    Sha256::digest(text.as_bytes()).iter().map(|b| format!("{b:02x}")).collect()
}

/// Escapes every non-ASCII character of serialized JSON as \uXXXX (surrogate pairs above
/// U+FFFF). Non-ASCII only occurs inside strings, so this is safe on a whole document.
pub fn ascii_json(json: &str) -> String {
    let mut out = String::with_capacity(json.len());
    for c in json.chars() {
        if c.is_ascii() && c != '\x7f' {
            out.push(c);
        } else {
            let mut units = [0u16; 2];
            for unit in c.encode_utf16(&mut units) {
                out.push_str(&format!("\\u{unit:04x}"));
            }
        }
    }
    out
}

/// JSON indented by 2 spaces; `ascii` escapes non-ASCII characters.
pub fn pretty_json(v: &Json, ascii: bool) -> String {
    let s = serde_json::to_string_pretty(v).expect("JSON values always serialize");
    if ascii {
        ascii_json(&s)
    } else {
        s
    }
}

/// A string in single quotes (double quotes if it contains only single ones), with
/// backslashes, quotes and control characters escaped, as Python's repr() writes it.
pub fn quote(s: &str) -> String {
    let q = if s.contains('\'') && !s.contains('"') { '"' } else { '\'' };
    let mut out = String::from(q);
    for c in s.chars() {
        match c {
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if c == q => out.extend(['\\', c]),
            c if c.is_control() && (c as u32) < 0x100 => out.push_str(&format!("\\x{:02x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push(q);
    out
}

/// A path as typed, tidied: no `.` parts, repeated or trailing slashes (`./sops/` → `sops`).
pub fn tidy(path: &Path) -> PathBuf {
    let tidy: PathBuf = path.components().filter(|c| *c != Component::CurDir).collect();
    if tidy.as_os_str().is_empty() {
        PathBuf::from(".")
    } else {
        tidy
    }
}

/// A path under `root` (given relative to it) as seen from the current folder, so terminals and
/// editors can open it: `procedures/x.md` in `sops` → `sops/procedures/x.md`.
pub fn user_path(root: &Path, rel: &str) -> String {
    let path = tidy(&root.join(rel));
    let cwd = std::env::current_dir().ok();
    let path = cwd.and_then(|cwd| path.strip_prefix(cwd).ok().map(Path::to_path_buf)).unwrap_or(path);
    path.to_string_lossy().into_owned()
}
