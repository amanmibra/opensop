"""Read a opensop folder into a Workspace, from disk or from an in-memory file map.

Layout:
    opensop.yaml
    bases/<id>.md          front matter + prompt text
    procedures/<id>.yaml
    agents/<id>.yaml
"""

from __future__ import annotations

import json
from pathlib import Path, PurePosixPath
from typing import Any, Mapping

import yaml
from pydantic import ValidationError

from .issues import Issue, OpenSOPError
from .models import SOP, Agent, Base, Workspace, WorkspaceConfig

SOURCE_GLOBS = ("opensop.yaml", "bases/*.md", "procedures/*.yaml", "agents/*.yaml")


def read_files(root: str | Path) -> dict[str, str]:
    """The source files of a opensop folder, keyed by path relative to the root."""
    root = Path(root)
    return {
        path.relative_to(root).as_posix(): path.read_text()
        for pattern in SOURCE_GLOBS
        for path in sorted(root.glob(pattern))
    }


def load_workspace(root: str | Path) -> Workspace:
    return load_workspace_files(read_files(root))


def load_workspace_files(files: Mapping[str, str]) -> Workspace:
    issues: list[Issue] = []

    if "opensop.yaml" not in files:
        raise OpenSOPError([Issue("missing_config", "opensop.yaml not found")])
    config_data = _load_yaml(files["opensop.yaml"], "opensop.yaml", issues)
    config = _parse(WorkspaceConfig, config_data, "opensop.yaml", issues) if config_data is not None else None

    bases: dict[str, Base] = {}
    for path in _matching(files, "bases", ".md"):
        meta, body = _split_front_matter(files[path])
        if (data := _load_yaml(meta, path, issues)) is None:
            continue
        data = _with_id(data, path, issues)
        if base := _parse(Base, {**data, "text": body.strip()}, path, issues):
            bases[base.id] = base

    sops: dict[str, SOP] = {}
    for path in _matching(files, "procedures", ".yaml"):
        if (data := _load_yaml(files[path], path, issues)) is None:
            continue
        data = _with_id(data, path, issues)
        if _check_steps(data, path, issues) and (sop := _parse(SOP, data, path, issues)):
            sops[sop.id] = sop

    agents: dict[str, Agent] = {}
    for path in _matching(files, "agents", ".yaml"):
        if (data := _load_yaml(files[path], path, issues)) is None:
            continue
        data = _with_id(data, path, issues)
        if agent := _parse(Agent, data, path, issues):
            agents[agent.id] = agent

    if issues:
        raise OpenSOPError(issues)
    return Workspace(config=config, bases=bases, sops=sops, agents=agents)


def _matching(files: Mapping[str, str], folder: str, suffix: str) -> list[str]:
    return sorted(p for p in files if PurePosixPath(p).parent == PurePosixPath(folder) and p.endswith(suffix))


def _split_front_matter(text: str) -> tuple[str, str]:
    if text.startswith("---\n"):
        end = text.find("\n---", 3)
        if end != -1:
            return text[4:end], text[end + 4 :].lstrip("\n")
    return "", text


def _load_yaml(text: str, path: str, issues: list[Issue]) -> dict[str, Any] | None:
    """Parse a YAML mapping, or report the problem and return None."""
    try:
        data = yaml.safe_load(text) or {}
    except yaml.YAMLError as e:
        hint = ""
        if "mapping values are not allowed" in str(e):
            hint = " Usually a colon followed by a space inside unquoted text: put the text in quotes, or use a | block."
        issues.append(Issue("invalid_yaml", " ".join(str(e).split()) + hint, path))
        return None
    if not isinstance(data, dict):
        issues.append(Issue("invalid_yaml", "expected a mapping", path))
        return None
    return data


STEP_FIELDS = ("procedureSteps", "forbiddenActions", "warningSigns")


def _check_steps(data: dict[str, Any], path: str, issues: list[Issue]) -> bool:
    """Catch steps YAML silently turned into something other than text. Returns True if all are fine.

    `- Never say: "allergen-free"` parses as the mapping {"Never say": "allergen-free"}, which would
    otherwise surface as a confusing schema error.
    """
    ok = True
    for field in STEP_FIELDS:
        items = data.get(field)
        if not isinstance(items, list):
            continue
        for i, item in enumerate(items):
            where = f"{field}[{i}]"
            if isinstance(item, dict) and len(item) == 1 and not set(item) <= {"text", "tool", "required"}:
                key, value = next(iter(item.items()))
                text = f"{key}: {value}" if value is not None else f"{key}:"
                issues.append(
                    Issue(
                        "colon_in_step",
                        f"{where} was read as a key and value because of the colon. "
                        f"Put the whole step in quotes: - {json.dumps(text)}",
                        path,
                    )
                )
                ok = False
            elif item is None:
                issues.append(Issue("empty_step", f"{where} is empty", path))
                ok = False
            elif isinstance(item, (bool, int, float)):
                issues.append(
                    Issue("unquoted_value", f"{where} was read as {item!r}, not text. Put the step in quotes.", path)
                )
                ok = False
    return ok


def _with_id(data: dict[str, Any], path: str, issues: list[Issue]) -> dict[str, Any]:
    """Ids come from file names. An explicit `id:` must agree with the file name."""
    stem = PurePosixPath(path).stem
    if "id" in data and data["id"] != stem:
        issues.append(Issue("id_mismatch", f"id '{data['id']}' does not match file name '{stem}'", path))
    return {**data, "id": stem}


def _parse(model, data: dict[str, Any], path: str, issues: list[Issue]):
    try:
        return model.model_validate(data)
    except ValidationError as e:
        for err in e.errors():
            loc = ".".join(str(p) for p in err["loc"])
            issues.append(Issue("invalid_field", f"{loc}: {err['msg']}" if loc else err["msg"], path))
        return None
