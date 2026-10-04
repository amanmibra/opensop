"""Checks that need the whole workspace: references, cycles, locks, variables."""

from __future__ import annotations

from .issues import Issue
from .models import SOP, Base, Workspace
from .render import find_variables, resolve_bases, resolve_sops, sop_tool_payload, targets


def validate(ws: Workspace) -> list[Issue]:
    issues: list[Issue] = []
    agent_names = {a.id for a in ws.agents.values()} | {a.platform_ref for a in ws.agents.values()}
    blocks: dict[str, Base | SOP] = {**ws.bases, **ws.sops}

    for base_id in ws.bases.keys() & ws.sops.keys():
        issues.append(Issue("duplicate_id", f"'{base_id}' is both a base and an SOP; ids must be unique"))

    seen_refs: dict[str, str] = {}
    for agent in ws.agents.values():
        if agent.platform_ref in seen_refs:
            issues.append(
                Issue("duplicate_platform_ref", f"{agent.platform_ref} is used by '{seen_refs[agent.platform_ref]}' and '{agent.id}'", _agent_path(agent.id))
            )
        seen_refs[agent.platform_ref] = agent.id

    for path, block in [(_base_path(b.id), b) for b in ws.bases.values()] + [(_sop_path(s.id), s) for s in ws.sops.values()]:
        if block.agents != "*":
            for name in block.agents:
                if name not in agent_names:
                    issues.append(Issue("unknown_agent", f"agents lists '{name}', which is not a known agent", path))
        for name in block.exclude:
            if name not in agent_names:
                issues.append(Issue("unknown_agent", f"exclude lists '{name}', which is not a known agent", path))

    for base in ws.bases.values():
        for parent in base.inherits:
            if parent not in ws.bases:
                issues.append(Issue("unknown_base", f"inherits '{parent}', which is not a base", _base_path(base.id)))
    issues += _inheritance_cycles(ws)

    for sop in ws.sops.values():
        if not sop.description.strip():
            issues.append(Issue("missing_goal", "no description (goal); QA can't judge whether the goal was met", _sop_path(sop.id), "warning"))
    for sop_id in ws.config.sop_order:
        if sop_id not in ws.sops:
            issues.append(Issue("unknown_sop", f"sop_order lists '{sop_id}', which is not an SOP", "sopkit.yaml"))

    for agent in ws.agents.values():
        path = _agent_path(agent.id)
        for base_id in agent.inherits:
            if base_id not in ws.bases:
                issues.append(Issue("unknown_base", f"inherits '{base_id}', which is not a base", path))
        for block_id in agent.exclude:
            block = blocks.get(block_id)
            if block is None:
                issues.append(Issue("unknown_block", f"exclude lists '{block_id}', which is not a base or SOP", path))
            elif block.locked and targets(block, agent):
                issues.append(Issue("locked", f"can't exclude '{block_id}': it is locked", path))
            elif not targets(block, agent):
                issues.append(Issue("useless_exclude", f"exclude lists '{block_id}', which doesn't target this agent", path, "warning"))

        if any(i.code in ("unknown_base", "inheritance_cycle") for i in issues):
            continue  # resolution below would be misleading
        texts = [b.text for b in resolve_bases(ws, agent)] + [agent.instructions]
        texts += [str(sop_tool_payload(s)) for s in resolve_sops(ws, agent)]
        values = {**ws.config.variables, **agent.variables}
        for name in sorted(set().union(*(find_variables(t) for t in texts)) - values.keys()):
            issues.append(Issue("unset_variable", f"'{{{{{name}}}}}' is used but has no value", path))

    return issues


def _inheritance_cycles(ws: Workspace) -> list[Issue]:
    issues: list[Issue] = []
    reported: set[frozenset[str]] = set()

    def walk(base_id: str, stack: list[str]) -> None:
        if base_id in stack:
            cycle = stack[stack.index(base_id) :]
            if frozenset(cycle) not in reported:
                reported.add(frozenset(cycle))
                issues.append(Issue("inheritance_cycle", " → ".join(cycle + [base_id]), _base_path(cycle[0])))
            return
        base = ws.bases.get(base_id)
        if base:
            for parent in base.inherits:
                walk(parent, stack + [base_id])

    for base_id in sorted(ws.bases):
        walk(base_id, [])
    return issues


def _base_path(i: str) -> str:
    return f"bases/{i}.md"


def _sop_path(i: str) -> str:
    return f"procedures/{i}.yaml"


def _agent_path(i: str) -> str:
    return f"agents/{i}.yaml"
