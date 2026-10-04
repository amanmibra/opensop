"""Turn a Workspace into one full prompt per agent.

Render order for an agent:
    1. top bases: the agent's `inherits` (depth-first, parents before children),
       then bases that target the agent, in id order; each base appears once
    2. the agent's own instructions
    3. SOPs that target the agent, under `sops_heading`
    4. bottom bases (`position: bottom`), same ordering rules
Variables (`{{name}}`) are filled last, from opensop.yaml defaults then the agent.
"""

from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass, field

from .issues import Issue, OpenSOPError
from .models import SOP, Agent, Base, Step, StepLike, Targeting, Workspace

VARIABLE = re.compile(r"\{\{\s*([A-Za-z_][\w.-]*)\s*\}\}")


@dataclass
class RenderedAgent:
    agent: Agent
    prompt: str
    bases: list[Base]
    sops: list[SOP]
    tool_payload: dict[str, dict] = field(default_factory=dict)  # what get_sop serves, by SOP id
    tools: list[str] = field(default_factory=list)  # tool names the SOPs reference

    @property
    def hash(self) -> str:
        return _sha256(self.prompt)


@dataclass
class Build:
    agents: dict[str, RenderedAgent]
    warnings: list[Issue]

    def lock(self) -> dict:
        return {
            "version": 1,
            "agents": {
                agent_id: {
                    "platform_ref": r.agent.platform_ref,
                    "hash": r.hash,
                    "blocks": [{"kind": "agent", "id": r.agent.id, "hash": block_hash(r.agent)}]
                    + [{"kind": "base", "id": b.id, "hash": block_hash(b)} for b in r.bases]
                    + [{"kind": "sop", "id": s.id, "hash": block_hash(s)} for s in r.sops],
                    "tools": r.tools,
                }
                for agent_id, r in sorted(self.agents.items())
            },
        }


def render_workspace(ws: Workspace) -> Build:
    """Validate and render every agent. Raises OpenSOPError if there are errors."""
    from .validate import validate

    issues = validate(ws)
    errors = [i for i in issues if i.severity == "error"]
    if errors:
        raise OpenSOPError(errors)
    return Build(
        agents={agent_id: render_agent(ws, agent) for agent_id, agent in sorted(ws.agents.items())},
        warnings=[i for i in issues if i.severity == "warning"],
    )


def render_agent(ws: Workspace, agent: Agent) -> RenderedAgent:
    bases = resolve_bases(ws, agent)
    sops = resolve_sops(ws, agent)

    sections = [b.text for b in bases if b.position == "top"]
    if agent.instructions.strip():
        sections.append(agent.instructions.strip())
    if sops:
        sections.append(ws.config.sops_heading + "\n\n" + "\n\n".join(render_sop_in_prompt(s) for s in sops))
    sections += [b.text for b in bases if b.position == "bottom"]

    values = {**ws.config.variables, **agent.variables}
    prompt = fill_variables("\n\n".join(s for s in sections if s), values) + "\n"

    tool_payload = {
        s.id: json.loads(fill_variables(json.dumps(sop_tool_payload(s)), values, escape_json=True))
        for s in sops
        if s.delivery != "prompt"
    }
    return RenderedAgent(
        agent=agent,
        prompt=prompt,
        bases=bases,
        sops=sops,
        tool_payload=tool_payload,
        tools=sorted({t for s in sops for t in sop_tools(s)}),
    )


# --- resolution ---------------------------------------------------------------


def targets(block: Targeting, agent: Agent) -> bool:
    """True if a base/SOP applies to the agent through its own `agents:` field."""
    names = {agent.id, agent.platform_ref}
    if names & set(block.exclude):
        return False
    if block.agents == "*":
        return True
    return bool(names & set(block.agents))


def opted_out(block: Base | SOP, agent: Agent) -> bool:
    return block.id in agent.exclude and not block.locked


def resolve_bases(ws: Workspace, agent: Agent) -> list[Base]:
    targeted = [b.id for b in sorted(ws.bases.values(), key=lambda b: b.id) if targets(b, agent)]
    ordered: list[str] = []

    def visit(base_id: str, stack: tuple[str, ...]) -> None:
        if base_id in ordered or base_id in stack or base_id not in ws.bases:
            return
        for parent in ws.bases[base_id].inherits:
            visit(parent, stack + (base_id,))
        ordered.append(base_id)

    for base_id in agent.inherits:
        visit(base_id, ())
    for base_id in targeted:
        if not opted_out(ws.bases[base_id], agent):
            visit(base_id, ())
    return [ws.bases[i] for i in ordered]


def resolve_sops(ws: Workspace, agent: Agent) -> list[SOP]:
    matching = {s.id: s for s in ws.sops.values() if targets(s, agent) and not opted_out(s, agent)}
    first = [matching[i] for i in ws.config.sop_order if i in matching]
    rest = [s for i, s in sorted(matching.items()) if i not in ws.config.sop_order]
    return first + rest


# --- SOP text -----------------------------------------------------------------


def _step(step: StepLike) -> Step:
    return Step(text=step) if isinstance(step, str) else step


def _clean(text: str) -> str:
    return text.strip().rstrip(".")


def render_step(step: StepLike, kind: str) -> str:
    s = _step(step)
    if not s.tool:
        return s.text.strip()
    if kind == "forbidden":
        return f"{_clean(s.text)}. This applies to the `{s.tool}` tool."
    return f"{_clean(s.text)}. Use the `{s.tool}` tool."


def render_sop(sop: SOP, *, steps: bool = True, guards: bool = True, details: bool = True) -> str:
    lines = [f"### {sop.name}"]
    if details and sop.description:
        lines.append(f"Goal: {sop.description.strip()}")
    if sop.scope:
        lines.append(f"When this applies: {sop.scope.strip()}")
    if details and sop.guidance:
        lines += ["", sop.guidance.strip()]
    if steps and sop.procedureSteps:
        lines += ["", "Steps:"] + [f"{n}. {render_step(s, 'step')}" for n, s in enumerate(sop.procedureSteps, 1)]
    if guards and sop.forbiddenActions:
        lines += ["", "Never:"] + [f"- {render_step(s, 'forbidden')}" for s in sop.forbiddenActions]
    if guards and sop.warningSigns:
        lines += ["", "Warning signs:"] + [f"- {render_step(s, 'warning')}" for s in sop.warningSigns]
    return "\n".join(lines)


def render_sop_in_prompt(sop: SOP) -> str:
    """What the prompt carries for an SOP, depending on its delivery mode."""
    if sop.delivery == "prompt":
        return render_sop(sop)
    fetch = f"Before following this procedure, call the `get_sop` tool with id `{sop.id}` for the full steps."
    if sop.delivery == "auto":
        return render_sop(sop, steps=False, details=False) + "\n\n" + fetch
    return render_sop(sop, steps=False, guards=False, details=False) + "\n\n" + fetch


def sop_tool_payload(sop: SOP) -> dict:
    return {
        "id": sop.id,
        "name": sop.name,
        "text": render_sop(sop),
        "description": sop.description,
        "scope": sop.scope,
        "guidance": sop.guidance,
        "procedureSteps": [_step(s).model_dump(exclude_defaults=True) for s in sop.procedureSteps],
        "forbiddenActions": [_step(s).model_dump(exclude_defaults=True) for s in sop.forbiddenActions],
        "warningSigns": [_step(s).model_dump(exclude_defaults=True) for s in sop.warningSigns],
    }


def sop_tools(sop: SOP) -> list[str]:
    steps = sop.procedureSteps + sop.forbiddenActions + sop.warningSigns
    return [t for t in (_step(s).tool for s in steps) if t]


# --- helpers ------------------------------------------------------------------


def find_variables(text: str) -> set[str]:
    return set(VARIABLE.findall(text))


def fill_variables(text: str, values: dict[str, str], *, escape_json: bool = False) -> str:
    def sub(m: re.Match) -> str:
        if m.group(1) not in values:
            return m.group(0)
        value = values[m.group(1)]
        return json.dumps(value)[1:-1] if escape_json else value

    return VARIABLE.sub(sub, text)


def block_hash(block: Agent | Base | SOP) -> str:
    return _sha256(block.model_dump_json())


def _sha256(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()
