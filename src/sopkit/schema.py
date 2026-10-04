"""Export JSON Schemas for the file formats into spec/.

Run `python -m sopkit.schema` after changing models.py; a test checks spec/ is current.
"""

from __future__ import annotations

import json
from pathlib import Path

from pydantic import BaseModel

from .models import SOP, Agent, Base, WorkspaceConfig

SPEC_DIR = Path(__file__).resolve().parents[2] / "spec"

def _schema(model: type[BaseModel], title: str, drop: set[str] = frozenset()) -> dict:
    """JSON Schema for a file. `id` stays allowed but optional (it comes from the file name)."""
    schema = model.model_json_schema()
    schema["title"] = title
    for name in drop:
        schema.get("properties", {}).pop(name, None)
    schema["required"] = [r for r in schema.get("required", []) if r not in drop and r != "id"]
    if not schema["required"]:
        del schema["required"]
    return schema


def schemas() -> dict[str, dict]:
    return {
        "base.schema.json": _schema(Base, "sopkit base: front matter of bases/<id>.md", {"text"}),
        "sop.schema.json": _schema(SOP, "sopkit SOP: procedures/<id>.yaml"),
        "agent.schema.json": _schema(Agent, "sopkit agent: agents/<id>.yaml"),
        "sopkit.schema.json": _schema(WorkspaceConfig, "sopkit.yaml"),
    }


def render_schemas() -> dict[str, str]:
    return {name: json.dumps(s, indent=2) + "\n" for name, s in schemas().items()}


def main() -> None:
    SPEC_DIR.mkdir(exist_ok=True)
    for name, text in render_schemas().items():
        (SPEC_DIR / name).write_text(text)
        print(f"wrote spec/{name}")


if __name__ == "__main__":
    main()
