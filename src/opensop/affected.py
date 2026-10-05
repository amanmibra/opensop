"""Which agents a change affects, and what to hand to tests and CI.

An agent is affected when its built prompt differs between the base and the head (so an
edit to a shared block only affects the agents that use it). For each affected agent,
`changed` says which blocks caused it, e.g. ["sop:allergen-check"], so tests can run just
the cases for what changed.
"""

from __future__ import annotations

import json
from dataclasses import asdict, dataclass, field
from typing import Literal

from .issues import Issue, OpenSOPError
from .plan import make_plan, snapshot
from .render import Build

Reason = Literal["changed", "added", "requested", "all"]


@dataclass
class AffectedAgent:
    id: str  # OpenSOP id: the file name in agents/
    platform_ref: str  # "livekit:tonys-pizza"
    platform: str  # "livekit"
    platform_id: str  # the platform's own id: "tonys-pizza"
    reason: Reason
    changed: list[str] = field(default_factory=list)  # blocks that changed this agent's prompt
    changed_sops: list[str] = field(default_factory=list)  # just the SOP ids from `changed`
    sops: list[str] = field(default_factory=list)  # every SOP this agent has


@dataclass
class Affected:
    agents: list[AffectedAgent]
    all: bool  # true when every agent was selected (nothing changed, or nothing requested)

    @property
    def ids(self) -> list[str]:
        return [a.id for a in self.agents]

    def to_dict(self) -> dict:
        return {"all": self.all, "count": len(self.agents), "agents": [asdict(a) for a in self.agents]}

    def github_outputs(self) -> dict[str, str]:
        """Values for $GITHUB_OUTPUT: space-separated lists, plus a compact JSON matrix."""
        return {
            "ids": " ".join(self.ids),
            "platform_ids": " ".join(a.platform_id for a in self.agents),
            "matrix": json.dumps([asdict(a) for a in self.agents], separators=(",", ":")),
            "count": str(len(self.agents)),
            "all": "true" if self.all else "false",
        }

    def markdown(self) -> str:
        if not self.agents:
            return "### Agents to test\n\nNone.\n"
        why = "all agents" if self.all else "affected by this change"
        lines = [f"### Agents to test ({len(self.agents)}, {why})", ""]
        for a in self.agents:
            detail = f": {', '.join(a.changed)}" if a.changed else f" ({a.reason})"
            lines.append(f"- `{a.id}` ({a.platform} `{a.platform_id}`){detail}")
        return "\n".join(lines) + "\n"


def affected(
    head: Build,
    base: Build | None = None,
    requested: list[str] | None = None,
    all_if_none: bool = False,
) -> Affected:
    """Agents to test.

    requested    explicit OpenSOP ids or platform ids; overrides the comparison
    base         compare with this build; agents whose prompt changed are selected
    all_if_none  select every agent when the comparison (or no base) selects none
    """
    if requested:
        return Affected([_agent(head, i, "requested") for i in _resolve(head, requested)], all=False)

    picked: list[AffectedAgent] = []
    if base is not None:
        plan = make_plan(snapshot(base), snapshot(head))
        for change in plan.changes:
            if change.status == "removed":
                continue
            agent = _agent(head, change.agent_id, "added" if change.status == "added" else "changed")
            agent.changed = sorted(change.blocks)
            agent.changed_sops = sorted(b.split(":", 1)[1] for b in change.blocks if b.startswith("sop:"))
            picked.append(agent)
    if picked or not (all_if_none or base is None):
        return Affected(picked, all=False)
    return Affected([_agent(head, i, "all") for i in sorted(head.agents)], all=True)


def _agent(head: Build, agent_id: str, reason: Reason) -> AffectedAgent:
    rendered = head.agents[agent_id]
    a = rendered.agent
    return AffectedAgent(
        id=agent_id,
        platform_ref=a.platform_ref,
        platform=a.platform,
        platform_id=getattr(a, a.platform),
        reason=reason,
        sops=[s.id for s in rendered.sops],
    )


def _resolve(head: Build, requested: list[str]) -> list[str]:
    """Accept OpenSOP ids, platform ids ("asst_9f3e") or platform refs ("vapi:asst_9f3e")."""
    lookup = {}
    for agent_id, r in head.agents.items():
        lookup[r.agent.platform_ref] = agent_id
        lookup.setdefault(getattr(r.agent, r.agent.platform), agent_id)
    lookup.update({agent_id: agent_id for agent_id in head.agents})
    ids, unknown = [], []
    for name in requested:
        agent_id = lookup.get(name)
        if agent_id is None:
            unknown.append(name)
        elif agent_id not in ids:
            ids.append(agent_id)
    if unknown:
        known = ", ".join(sorted(head.agents))
        raise OpenSOPError([Issue("unknown_agent", f"unknown agent(s): {', '.join(unknown)}. Known: {known}")])
    return ids
