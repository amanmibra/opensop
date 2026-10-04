"""Write a Build to disk: build/<agent>.prompt.md, build/<agent>.tool.json, build/lock.json."""

from __future__ import annotations

import json
from pathlib import Path

from .render import Build


def write_build(build: Build, out_dir: str | Path) -> list[Path]:
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    for stale in [*out.glob("*.prompt.md"), *out.glob("*.tool.json")]:
        stale.unlink()

    written: list[Path] = []
    for agent_id, rendered in build.agents.items():
        written.append(_write(out / f"{agent_id}.prompt.md", rendered.prompt))
        if rendered.tool_payload:
            written.append(_write(out / f"{agent_id}.tool.json", _json(rendered.tool_payload)))
    written.append(_write(out / "lock.json", _json(build.lock())))
    return written


def _json(data: dict) -> str:
    return json.dumps(data, indent=2, ensure_ascii=False) + "\n"


def _write(path: Path, text: str) -> Path:
    path.write_text(text)
    return path
