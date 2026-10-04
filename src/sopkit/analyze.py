"""Deterministic text analysis for importing and reviewing prompts.

    overlap(prompts)          which sentences several original prompts share, and near-copies
                              that differ only by a value (candidates for {{placeholders}})
    compare(build, originals) does each rendered prompt still contain everything its original said?
    check(workspace)          duplicated text and mechanical conflicts within each agent's prompt

These do the counting so an LLM (the /sopkit-import skill) can focus on judgment. None of
them understand meaning: "we deliver" vs "pickup only" needs a model to catch.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from difflib import SequenceMatcher

from .models import SOP, Agent, Workspace
from .render import Build, fill_variables, find_variables, render_step, resolve_bases, resolve_sops

# --- text units -------------------------------------------------------------------

_SENTENCE_END = re.compile(r"(?<=[.!?])\s+(?=[A-Z0-9\"'(])")
_LIST_MARKER = re.compile(r"^\s*(?:[-*+]|\d+[.)])\s+")
_HEADING = re.compile(r"^\s*#{1,6}\s+")
_LABEL_ONLY = re.compile(r"^[A-Za-z][A-Za-z ]{0,30}:$")  # "Steps:", "Never:", "Warning signs:"
_SOPKIT_PREFIX = re.compile(r"^(?:Goal|When this applies):\s+")
_WORD = re.compile(r"[a-z0-9]+(?:'[a-z]+)?")
_NUMBER = re.compile(r"\d+(?:[.:]\d+)?")
_NEGATIONS = {"not", "never", "no", "don't", "dont", "doesn't", "cannot", "can't", "won't", "without", "nothing"}

MIN_WORDS = 3


def units(text: str) -> list[str]:
    """Split prompt text into comparable units: sentences, list items, lines. Headings and bare labels are dropped."""
    out: list[str] = []
    for line in text.splitlines():
        line = line.strip()
        if not line or _HEADING.match(line) or _LABEL_ONLY.match(line):
            continue
        line = _LIST_MARKER.sub("", line)
        line = _SOPKIT_PREFIX.sub("", line)
        out += [s.strip() for s in _SENTENCE_END.split(line) if s.strip()]
    return [u for u in out if len(words(u)) >= MIN_WORDS]


def words(text: str) -> list[str]:
    return _WORD.findall(text.lower().replace("’", "'"))


def norm(text: str) -> str:
    return " ".join(words(text))


def similarity(a: str, b: str) -> float:
    return SequenceMatcher(None, words(a), words(b), autojunk=False).ratio()


def differing_words(a: str, b: str) -> tuple[str, str]:
    """The words that differ between two near-identical sentences: ("Tony's Pizza", "Sakura Sushi")."""
    wa, wb = a.split(), b.split()
    key = lambda t: t.strip(".,;:!?\"'").lower()  # noqa: E731
    sm = SequenceMatcher(None, [key(t) for t in wa], [key(t) for t in wb], autojunk=False)
    da, db = [], []
    for op, i1, i2, j1, j2 in sm.get_opcodes():
        if op != "equal":
            da += wa[i1:i2]
            db += wb[j1:j2]
    return " ".join(da).strip(".,;:!?"), " ".join(db).strip(".,;:!?")


# --- overlap: what do the original prompts share? ------------------------------------


@dataclass
class SharedText:
    agents: list[str]
    text: str


@dataclass
class NearCopy:
    """The same sentence with a different value in some prompts: a placeholder candidate."""

    variants: dict[str, str]  # agent → its version
    differing: dict[str, str]  # agent → just the words that differ


@dataclass
class Overlap:
    agents: list[str]
    shared: list[SharedText]
    near_copies: list[NearCopy]

    def text(self) -> str:
        out = [f"{len(self.agents)} prompts: {', '.join(self.agents)}", ""]
        by_group: dict[tuple[str, ...], list[str]] = {}
        for s in self.shared:
            by_group.setdefault(tuple(s.agents), []).append(s.text)
        for group, texts in sorted(by_group.items(), key=lambda g: (-len(g[0]), g[0])):
            label = "ALL agents" if len(group) == len(self.agents) else ", ".join(group)
            out.append(f"Shared by {label} ({len(texts)} sentences):")
            out += [f"  - {t}" for t in texts]
            out.append("")
        if self.near_copies:
            out.append("Near-copies (same sentence, different value; use a {{placeholder}}):")
            for nc in self.near_copies:
                first = next(iter(nc.variants.values()))
                out.append(f"  - {first}")
                out += [f"      {agent}: {diff!r}" for agent, diff in nc.differing.items()]
            out.append("")
        unique = {a: 0 for a in self.agents}
        covered = {(a, norm(s.text)) for s in self.shared for a in s.agents}
        covered |= {(a, norm(v)) for nc in self.near_copies for a, v in nc.variants.items()}
        for agent in self.agents:
            unique[agent] = sum((agent, norm(u)) not in covered for u in self._units[agent])
        out.append("Only in one prompt (sentences): " + ", ".join(f"{a} {n}" for a, n in unique.items()))
        return "\n".join(out) + "\n"

    _units: dict[str, list[str]] = field(default_factory=dict, repr=False)


def overlap(prompts: dict[str, str], near: float = 0.75) -> Overlap:
    per_agent = {agent: _dedupe(units(text)) for agent, text in sorted(prompts.items())}
    agents = list(per_agent)

    exact: dict[str, dict[str, str]] = {}  # norm → agent → original sentence
    for agent, us in per_agent.items():
        for u in us:
            exact.setdefault(norm(u), {})[agent] = u
    shared = [SharedText(sorted(v), next(iter(v.values()))) for v in exact.values() if len(v) > 1]

    # Near-copies: sentences found in one form per agent that match across agents except for a few words.
    singles = [(k, v) for k, v in exact.items() if len(v) < len(agents)]
    used: set[str] = set()
    near_copies: list[NearCopy] = []
    for i, (key, group) in enumerate(singles):
        if key in used:
            continue
        variants = dict(group)
        for other_key, other in singles[i + 1 :]:
            if other_key in used or set(other) & set(variants):
                continue
            if similarity(key, other_key) >= near:
                variants.update(other)
                used.add(other_key)
        if len(variants) > 1 and len({norm(v) for v in variants.values()}) > 1:
            used.add(key)
            differing = {a: differing_words(_other(variants, a), v)[1] for a, v in variants.items()}
            near_copies.append(NearCopy(variants=dict(sorted(variants.items())), differing=dict(sorted(differing.items()))))
    shared = [sh for sh in shared if norm(sh.text) not in used]  # reported as a near-copy instead
    result = Overlap(agents=agents, shared=shared, near_copies=near_copies)
    result._units = per_agent
    return result


def _other(variants: dict[str, str], agent: str) -> str:
    return next(v for a, v in variants.items() if a != agent)


def _dedupe(items: list[str]) -> list[str]:
    seen: set[str] = set()
    return [u for u in items if not (norm(u) in seen or seen.add(norm(u)))]


# --- compare: did anything get lost? ---------------------------------------------------


@dataclass
class Comparison:
    agent: str
    total: int
    missing: list[str]  # in the original; its words mostly aren't in the rendered prompt
    changed: list[tuple[str, str]]  # (original, rendered): nearly the same sentence, different words
    reworded: list[str]  # in the original; most of its words are there, but not as one sentence
    added: list[str]  # in the rendered prompt, not in the original (sopkit's own tool lines excluded)

    @property
    def coverage(self) -> float:
        return 1.0 if self.total == 0 else (self.total - len(self.missing) - len(self.changed)) / self.total

    @property
    def ok(self) -> bool:
        return not self.missing and not self.changed


# Sentences sopkit writes itself when rendering steps with tools or tool-delivered SOPs.
_GENERATED = re.compile(r"^(?:Use the `[^`]+` tool\.|This applies to the `[^`]+` tool\.|Before following this procedure, call the `get_sop` tool.*)$")
_STOPWORDS = set(
    "a an and are as at be by for from has have if in is it its of on or so that the their them they this to was were when with you your".split()
)


def compare(build: Build, originals: dict[str, str], threshold: float = 0.9, reworded_at: float = 0.6) -> list[Comparison]:
    results = []
    for agent, original in sorted(originals.items()):
        rendered = build.agents.get(agent)
        rendered_units = units(rendered.prompt) if rendered else []
        rendered_words = set(words(rendered.prompt)) if rendered else set()
        original_units = units(original)
        missing, changed, reworded = [], [], []
        for u in original_units:
            if _found(u, rendered_units, threshold):
                continue
            closest = max(rendered_units, key=lambda p: similarity(u, p), default="")
            if closest and similarity(u, closest) >= 0.7:
                changed.append((u, closest))
            elif _word_coverage(u, rendered_words) >= reworded_at:
                reworded.append(u)
            else:
                missing.append(u)
        added = [u for u in rendered_units if not _GENERATED.match(u) and not _found(u, original_units, threshold)]
        added = [u for u in added if _word_coverage(u, set(words(original))) < 0.9]
        results.append(Comparison(agent, len(original_units), missing, changed, reworded, added))
    return results


def _found(unit: str, pool: list[str], threshold: float) -> bool:
    n = norm(unit)
    return any(n == norm(p) or n in norm(p) or similarity(unit, p) >= threshold for p in pool)


def _word_coverage(unit: str, pool: set[str]) -> float:
    content = [w for w in words(unit) if w not in _STOPWORDS]
    return 1.0 if not content else sum(w in pool for w in content) / len(content)


def compare_text(results: list[Comparison], build: Build) -> str:
    out = []
    for r in results:
        status = "ok" if r.ok else "LOST OR CHANGED TEXT"
        out.append(f"{r.agent}: {r.coverage:.0%} of {r.total} sentences kept ({status})")
        out += [f"  - missing:  {u}" for u in r.missing]
        for original, rendered in r.changed:
            was, now = differing_words(original, rendered)
            out.append(f"  ! changed:  {original}")
            out.append(f"              now: {rendered}   ({was!r} → {now!r})")
        out += [f"  ~ reworded: {u}" for u in r.reworded]
        out += [f"  + added:    {u}" for u in r.added]
    unmatched = sorted(set(build.agents) - {r.agent for r in results})
    if unmatched:
        out.append(f"no original for: {', '.join(unmatched)}")
    return "\n".join(out) + "\n"


# --- check: duplicates and mechanical conflicts inside each agent's prompt ----------------


@dataclass
class Finding:
    code: str  # duplicate_text | numeric_conflict | negation_conflict | near_duplicate | unused_variable
    message: str
    sources: list[tuple[str, str]]  # (block, text)
    agents: list[str] = field(default_factory=list)

    def to_dict(self) -> dict:
        return {"code": self.code, "message": self.message, "sources": [{"block": b, "text": t} for b, t in self.sources], "agents": self.agents}


_MESSAGES = {
    "duplicate_text": "Same sentence appears twice in the prompt",
    "numeric_conflict": "Same sentence with different numbers",
    "negation_conflict": "One block says it, another says the opposite",
    "near_duplicate": "Nearly identical sentences; a copy that drifted?",
}


def check(ws: Workspace) -> list[Finding]:
    found: dict[tuple, Finding] = {}
    for agent in sorted(ws.agents.values(), key=lambda a: a.id):
        values = {**ws.config.variables, **agent.variables}
        sourced = [(src, fill_variables(u, values)) for src, u in _agent_units(ws, agent)]
        for i, (src_a, a) in enumerate(sourced):
            for src_b, b in sourced[i + 1 :]:
                code = _conflict(a, b)
                if not code or (code == "near_duplicate" and src_a == src_b):
                    continue
                key = (code, src_a, norm(a), src_b, norm(b))
                finding = found.setdefault(key, Finding(code, _MESSAGES[code], [(src_a, a), (src_b, b)]))
                finding.agents.append(agent.id)
        used = set().union(*(find_variables(t) for _, t in _agent_units(ws, agent, raw=True)))
        for name in sorted(set(agent.variables) - used):
            key = ("unused_variable", agent.id, name)
            found[key] = Finding("unused_variable", f"'{name}' is set but no block this agent uses mentions {{{{{name}}}}}", [(f"agent `{agent.id}`", name)], [agent.id])
    return list(found.values())


def _conflict(a: str, b: str) -> str | None:
    na, nb = norm(a), norm(b)
    if na == nb:
        return "duplicate_text"
    if _numbers_differ_in_same_sentence(na, nb):
        return "numeric_conflict"
    wa, wb = words(a), words(b)
    neg_a, neg_b = bool(_NEGATIONS & set(wa)), bool(_NEGATIONS & set(wb))
    stripped_a = [w for w in wa if w not in _NEGATIONS and w != "always"]
    stripped_b = [w for w in wb if w not in _NEGATIONS and w != "always"]
    if neg_a != neg_b and stripped_a and SequenceMatcher(None, stripped_a, stripped_b, autojunk=False).ratio() >= 0.9:
        return "negation_conflict"
    if similarity(a, b) >= 0.85:
        return "near_duplicate"
    return None


def _numbers_differ_in_same_sentence(na: str, nb: str) -> bool:
    """Same statement with a different number: identical once numbers are masked, one contained
    in the other ("delivery until #pm" inside "takeout and delivery until #pm"), or nearly identical."""
    nums_a, nums_b = _NUMBER.findall(na), _NUMBER.findall(nb)
    if not nums_a or not nums_b or nums_a == nums_b:
        return False
    ma, mb = _NUMBER.sub("#", na), _NUMBER.sub("#", nb)
    short, long = sorted((ma, mb), key=len)
    if ma == mb or (len(short.split()) >= 3 and f" {short} " in f" {long} "):
        return True
    return SequenceMatcher(None, ma.split(), mb.split(), autojunk=False).ratio() >= 0.8


def _agent_units(ws: Workspace, agent: Agent, raw: bool = False) -> list[tuple[str, str]]:
    """(block label, unit) for everything that ends up in this agent's prompt."""
    out: list[tuple[str, str]] = []
    split = (lambda t: [t]) if raw else units
    for base in resolve_bases(ws, agent):
        out += [(f"base `{base.id}`", u) for u in split(base.text)]
    out += [(f"agent `{agent.id}`", u) for u in split(agent.instructions)]
    for sop in resolve_sops(ws, agent):
        out += [(f"SOP `{sop.id}`", u) for u in split(_sop_text(sop))]
    return out


def _sop_text(sop: SOP) -> str:
    parts = [sop.description, sop.scope, sop.guidance]
    parts += [render_step(s, "step") for s in sop.procedureSteps]
    parts += [render_step(s, "forbidden") for s in sop.forbiddenActions]
    parts += [render_step(s, "warning") for s in sop.warningSigns]
    return "\n".join(p for p in parts if p)


def check_text(findings: list[Finding]) -> str:
    if not findings:
        return "No duplicates or mechanical conflicts found.\n"
    out = [f"{len(findings)} finding(s). These are advisory; decide which text is right.", ""]
    for f in findings:
        out.append(f"[{f.code}] {f.message} (agents: {', '.join(f.agents)})")
        out += [f"    {block}: {text}" for block, text in f.sources]
        out.append("")
    return "\n".join(out)
