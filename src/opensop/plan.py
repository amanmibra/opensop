"""Compare two builds: which agents change, because of which blocks, and how.

A Snapshot is what a build looks like from the outside (prompt + lock entry per
agent), so it can come from a fresh render or from a committed build/ folder.
"""

from __future__ import annotations

import difflib
import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Literal

from .render import Build


@dataclass(frozen=True)
class AgentSnapshot:
    prompt: str
    platform_ref: str
    blocks: dict[str, str]  # "kind:id" → hash


Snapshot = dict[str, AgentSnapshot]


def snapshot(build: Build) -> Snapshot:
    lock = build.lock()["agents"]
    return {
        agent_id: AgentSnapshot(
            prompt=rendered.prompt,
            platform_ref=rendered.agent.platform_ref,
            blocks={f"{b['kind']}:{b['id']}": b["hash"] for b in lock[agent_id]["blocks"]},
        )
        for agent_id, rendered in build.agents.items()
    }


def read_snapshot(build_dir: str | Path) -> Snapshot:
    """Read a build/ folder written by write_build. Missing folder → empty snapshot."""
    build_dir = Path(build_dir)
    lock_path = build_dir / "lock.json"
    if not lock_path.exists():
        return {}
    lock = json.loads(lock_path.read_text())["agents"]
    return {
        agent_id: AgentSnapshot(
            prompt=(build_dir / f"{agent_id}.prompt.md").read_text(),
            platform_ref=entry["platform_ref"],
            blocks={f"{b['kind']}:{b['id']}": b["hash"] for b in entry["blocks"]},
        )
        for agent_id, entry in lock.items()
    }


BlockChange = Literal["edited", "added", "removed"]


@dataclass
class AgentChange:
    agent_id: str
    status: Literal["added", "removed", "changed"]
    blocks: dict[str, BlockChange] = field(default_factory=dict)  # why it changed
    diff: str = ""


@dataclass
class Plan:
    changes: list[AgentChange]

    @property
    def empty(self) -> bool:
        return not self.changes

    def by_block(self) -> dict[tuple[str, BlockChange], list[str]]:
        """("base:brand-voice", "edited") → affected agent ids."""
        grouped: dict[tuple[str, BlockChange], list[str]] = {}
        for change in self.changes:
            for block, kind in change.blocks.items():
                grouped.setdefault((block, kind), []).append(change.agent_id)
        return dict(sorted(grouped.items()))

    def summary(self) -> list[str]:
        lines = []
        for change in self.changes:
            if change.status != "changed":
                lines.append(f"agent `{change.agent_id}` {change.status}")
        for (block, kind), agents in self.by_block().items():
            lines.append(f"{_label(block)} {_verb(block, kind)} → {_count(agents)}: {', '.join(agents)}")
        return lines

    def markdown(self) -> str:
        if self.empty:
            return "**opensop plan:** no agent prompts change."
        out = [f"**opensop plan:** {_count([c.agent_id for c in self.changes])} change", ""]
        out += [f"- {line}" for line in self.summary()]
        for change in self.changes:
            out += ["", f"<details><summary>{change.agent_id} ({change.status})</summary>", "", "```diff", change.diff.rstrip(), "```", "</details>"]
        return "\n".join(out) + "\n"

    def text(self, diffs: bool = True) -> str:
        if self.empty:
            return "No agent prompts change.\n"
        out = [f"{_count([c.agent_id for c in self.changes])} change:"] + [f"  {line}" for line in self.summary()]
        if diffs:
            for change in self.changes:
                out += ["", change.diff.rstrip()]
        return "\n".join(out) + "\n"

    def to_dict(self) -> dict:
        return {
            "changes": [
                {"agent": c.agent_id, "status": c.status, "blocks": c.blocks, "diff": c.diff} for c in self.changes
            ],
            "by_block": [
                {"block": block, "change": kind, "agents": agents} for (block, kind), agents in self.by_block().items()
            ],
        }


def make_plan(before: Snapshot, after: Snapshot) -> Plan:
    changes: list[AgentChange] = []
    for agent_id in sorted(before.keys() | after.keys()):
        old, new = before.get(agent_id), after.get(agent_id)
        if old and new and old.prompt == new.prompt:
            continue
        status = "added" if old is None else "removed" if new is None else "changed"
        changes.append(
            AgentChange(
                agent_id=agent_id,
                status=status,
                blocks=_block_changes(old.blocks, new.blocks) if old and new else {},
                diff=_diff(agent_id, old.prompt if old else "", new.prompt if new else ""),
            )
        )
    for change in changes:
        if change.status == "changed" and not change.blocks:
            change.blocks["workspace:opensop.yaml"] = "edited"  # e.g. a default variable changed
    return Plan(changes)


def _block_changes(old: dict[str, str], new: dict[str, str]) -> dict[str, BlockChange]:
    result: dict[str, BlockChange] = {}
    for block in sorted(old.keys() | new.keys()):
        if block not in old:
            result[block] = "added"
        elif block not in new:
            result[block] = "removed"
        elif old[block] != new[block]:
            result[block] = "edited"
    return result


def _diff(agent_id: str, old: str, new: str) -> str:
    return "".join(
        difflib.unified_diff(
            old.splitlines(keepends=True),
            new.splitlines(keepends=True),
            fromfile=f"a/{agent_id}.prompt.md",
            tofile=f"b/{agent_id}.prompt.md",
        )
    )


def _label(block: str) -> str:
    kind, _, block_id = block.partition(":")
    if kind == "workspace":
        return f"`{block_id}`"
    return f"{ {'sop': 'SOP', 'agent': 'agent file', 'base': 'base'}[kind] } `{block_id}`"


def _verb(block: str, kind: BlockChange) -> str:
    if block.startswith(("agent:", "workspace:")):
        return "edited"
    return {"edited": "edited", "added": "now applies", "removed": "no longer applies"}[kind]


def _count(agents: list[str]) -> str:
    return f"{len(agents)} agent{'s' if len(agents) != 1 else ''}"
