"""opensop command line.

    opensop validate [ROOT]
    opensop render   [ROOT] [--out DIR] [--check]
    opensop plan     [ROOT] [--against REF] [--json]
    opensop agents   [ROOT] [--json]
    opensop affected [ROOT] [--against REF] [--agents IDS] [--all-if-none] [--format F] [--ci]
    opensop overlap  DIR                         what several original prompts share
    opensop compare  [ROOT] --originals DIR      does each rendered prompt still say everything?
    opensop check    [ROOT] [--json]             duplicates and mechanical conflicts
    opensop skills install [--agent claude|codex|opencode] [--dir DIR]
    opensop guide
"""

from __future__ import annotations

import argparse
import importlib.resources
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

from . import analyze
from .affected import affected
from .build import write_build
from .issues import Issue, OpenSOPError
from .loader import load_workspace, load_workspace_files
from .plan import make_plan, read_snapshot, snapshot
from .render import render_workspace
from .validate import validate


class GitError(Exception):
    pass


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="opensop", description="Modular, git-versioned instructions for teams managing multiple task-driven agents.")
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("validate", help="check the files and print problems")
    p.add_argument("root", nargs="?", default=".")

    p = sub.add_parser("render", help="write one full prompt per agent into build/")
    p.add_argument("root", nargs="?", default=".")
    p.add_argument("--out", help="output folder (default: ROOT/build)")
    p.add_argument("--check", action="store_true", help="fail if build/ is out of date instead of writing it")

    p = sub.add_parser("plan", help="show which agents change and why")
    p.add_argument("root", nargs="?", default=".")
    p.add_argument("--against", metavar="REF", help="git ref to compare with (default: the committed build/ folder)")
    p.add_argument("--summary", action="store_true", help="omit the diffs")
    p.add_argument("--json", action="store_true", help="machine-readable output (for CI)")

    p = sub.add_parser("affected", help="which agents a change affects (for tests and CI)")
    p.add_argument("root", nargs="?", default=".")
    p.add_argument("--against", metavar="REF", help="git ref to compare with; agents whose prompt changed are selected")
    p.add_argument("--agents", default="", help="select these agents instead (OpenSOP ids or platform ids, space or comma separated)")
    p.add_argument("--all-if-none", action="store_true", help="select every agent when nothing else is selected")
    p.add_argument(
        "--format",
        choices=["ids", "platform-ids", "json"],
        default="ids",
        help="ids: OpenSOP ids (file names); platform-ids: the platform's own ids; json: everything",
    )
    p.add_argument("--ci", action="store_true", help="also write GitHub Actions outputs and a step summary")

    p = sub.add_parser("agents", help="list agents with their platform ids, SOPs and tools")
    p.add_argument("root", nargs="?", default=".")
    p.add_argument("--json", action="store_true")

    p = sub.add_parser("overlap", help="show text shared across existing prompts (for importing)")
    p.add_argument("dir", help="folder with one existing prompt per agent, named <agent-id>.md or .txt")

    p = sub.add_parser("compare", help="check rendered prompts still contain everything the originals said")
    p.add_argument("root", nargs="?", default=".")
    p.add_argument("--originals", required=True, help="folder with <agent-id>.md or .txt originals")

    p = sub.add_parser("check", help="find duplicated text and mechanical conflicts in each agent's prompt")
    p.add_argument("root", nargs="?", default=".")
    p.add_argument("--json", action="store_true")

    p = sub.add_parser("skills", help="install the opensop skills for coding agents")
    p.add_argument("action", choices=["install"])
    p.add_argument(
        "--agent",
        action="append",
        choices=sorted(SKILL_DIRS),
        help="install for this coding agent only (repeatable). Default: Claude Code, Codex and OpenCode",
    )
    p.add_argument("--dir", help="install into this folder instead")

    sub.add_parser("guide", help="print the format reference (FORMAT.md)")

    args = parser.parse_args(argv)
    try:
        return {"validate": _validate, "render": _render, "plan": _plan, "guide": _guide,
        "agents": _agents, "affected": _affected, "overlap": _overlap, "compare": _compare, "check": _check, "skills": _skills}[args.command](args)
    except OpenSOPError as e:
        for issue in e.issues:
            print(issue, file=sys.stderr)
        return 1
    except GitError as e:
        print(e, file=sys.stderr)
        return 1


def _validate(args) -> int:
    issues = validate(load_workspace(args.root))
    for issue in issues:
        print(issue, file=sys.stderr)
    errors = sum(i.severity == "error" for i in issues)
    print(f"{errors} error(s), {len(issues) - errors} warning(s)")
    return 1 if errors else 0


def _render(args) -> int:
    root = Path(args.root)
    out = Path(args.out) if args.out else root / "build"
    build = render_workspace(load_workspace(root))
    for warning in build.warnings:
        print(warning, file=sys.stderr)
    if args.check:
        plan = make_plan(read_snapshot(out), snapshot(build))
        if plan.empty:
            print(f"{out} is up to date")
            return 0
        print(f"{out} is out of date; run `opensop render`\n", file=sys.stderr)
        print(plan.text(diffs=False), file=sys.stderr)
        return 1
    written = write_build(build, out)
    print(f"wrote {len(written)} files to {out}")
    return 0


def _plan(args) -> int:
    root = Path(args.root)
    after = snapshot(render_workspace(load_workspace(root)))
    if args.against:
        files = files_at_ref(root, args.against)
        before = snapshot(render_workspace(load_workspace_files(files))) if files else {}
    else:
        before = read_snapshot(root / "build")
    plan = make_plan(before, after)
    if args.json:
        print(json.dumps(plan.to_dict(), indent=2))
    else:
        print(plan.text(diffs=not args.summary), end="")
    return 0


def _affected(args) -> int:
    root = Path(args.root)
    head = render_workspace(load_workspace(root))
    base = None
    if args.against:
        files = files_at_ref(root, args.against)
        base = render_workspace(load_workspace_files(files)) if files else None
        if base is None:
            print(f"no OpenSOP files at {args.against}; treating every agent as new", file=sys.stderr)
    requested = args.agents.replace(",", " ").split()
    # Without a base to compare with, every agent is selected (unless specific ones were requested).
    result = affected(head, base, requested, all_if_none=args.all_if_none)

    if args.format == "json":
        print(json.dumps(result.to_dict(), indent=2))
    else:
        field = {"ids": "id", "platform-ids": "platform_id"}[args.format]
        for agent in result.agents:
            print(getattr(agent, field))
    if args.ci:
        _write_github(result)
    return 0


def _write_github(result) -> None:
    """Write outputs for later steps and a summary for the run page (no-ops outside GitHub Actions)."""
    outputs = result.github_outputs()
    if path := os.environ.get("GITHUB_OUTPUT"):
        with open(path, "a") as f:
            f.writelines(f"{k}={v}\n" for k, v in outputs.items())
    else:
        print("\n# GITHUB_OUTPUT not set; these would be written:", file=sys.stderr)
        for k, v in outputs.items():
            print(f"#   {k}={v}", file=sys.stderr)
    if path := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(path, "a") as f:
            f.write(result.markdown())


def _agents(args) -> int:
    build = render_workspace(load_workspace(args.root))
    agents = [
        {
            "id": agent_id,
            "platform_ref": r.agent.platform_ref,
            "platform": r.agent.platform,
            "platform_id": getattr(r.agent, r.agent.platform),
            "bases": [b.id for b in r.bases],
            "sops": [s.id for s in r.sops],
            "tools": r.tools,
            "hash": r.hash,
        }
        for agent_id, r in build.agents.items()
    ]
    if args.json:
        print(json.dumps(agents, indent=2))
    else:
        for a in agents:
            print(f"{a['id']:24} {a['platform']:11} {a['platform_id']:28} sops: {', '.join(a['sops']) or '-'}")
    return 0


def _read_prompts(folder: str) -> dict[str, str]:
    paths = sorted(p for p in Path(folder).iterdir() if p.suffix in (".md", ".txt") and p.is_file())
    if not paths:
        raise OpenSOPError([Issue("no_prompts", f"no .md or .txt files in {folder}")])
    return {p.stem: p.read_text() for p in paths}


def _overlap(args) -> int:
    print(analyze.overlap(_read_prompts(args.dir)).text(), end="")
    return 0


def _compare(args) -> int:
    build = render_workspace(load_workspace(args.root))
    results = analyze.compare(build, _read_prompts(args.originals))
    print(analyze.compare_text(results, build), end="")
    return 0 if all(r.ok for r in results) else 1


def _check(args) -> int:
    findings = analyze.check(load_workspace(args.root))
    if args.json:
        print(json.dumps([f.to_dict() for f in findings], indent=2))
    else:
        print(analyze.check_text(findings), end="")
    return 0


# Where each coding agent looks for project skills. Codex and OpenCode both read .agents/skills;
# OpenCode also reads .claude/skills, so the default (both folders) covers all three.
SKILL_DIRS = {"claude": ".claude/skills", "codex": ".agents/skills", "opencode": ".agents/skills"}
HOW_TO_RUN = {
    ".claude/skills": "Claude Code: /opensop-import",
    ".agents/skills": "Codex: $opensop-import   OpenCode: ask it to use the opensop-import skill",
}


def _skills(args) -> int:
    source = _resource_dir("skills")
    if args.dir:
        dests = [args.dir]
    else:
        dests = sorted({SKILL_DIRS[a] for a in (args.agent or SKILL_DIRS)})
    for dest in dests:
        for skill in sorted(p for p in source.iterdir() if p.is_dir()):
            target = Path(dest) / skill.name
            shutil.copytree(skill, target, dirs_exist_ok=True)
            print(f"installed {target}/SKILL.md")
    print()
    for dest in dests:
        if dest in HOW_TO_RUN:
            print(HOW_TO_RUN[dest])
    return 0


def _resource_dir(name: str) -> Path:
    packaged = Path(str(importlib.resources.files("opensop"))) / name
    return packaged if packaged.is_dir() else Path(__file__).resolve().parents[2] / name  # source checkout


def _guide(args) -> int:
    print(read_guide(), end="")
    return 0


def read_guide() -> str:
    packaged = importlib.resources.files("opensop") / "FORMAT.md"
    if packaged.is_file():
        return packaged.read_text()
    return (Path(__file__).resolve().parents[2] / "FORMAT.md").read_text()  # source checkout


def files_at_ref(root: Path, ref: str) -> dict[str, str]:
    """The opensop source files under ROOT as they were at a git ref."""
    root = root.resolve()
    top = Path(_git(root, "rev-parse", "--show-toplevel").strip())
    prefix = root.relative_to(top).as_posix()
    prefix = "" if prefix == "." else prefix + "/"
    names = _git(top, "ls-tree", "-r", "--name-only", ref, "--", prefix or ".").splitlines()
    return {
        name[len(prefix) :]: _git(top, "show", f"{ref}:{name}")
        for name in names
        if _is_source(name[len(prefix) :])
    }


def _is_source(rel: str) -> bool:
    folder, _, name = rel.rpartition("/")
    return rel == "opensop.yaml" or (folder == "bases" and name.endswith(".md")) or (
        folder in ("procedures", "agents") and name.endswith(".yaml")
    )


def _git(cwd: Path, *args: str) -> str:
    result = subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True)
    if result.returncode != 0:
        raise GitError(f"git {' '.join(args)}: {result.stderr.strip()}")
    return result.stdout


if __name__ == "__main__":
    sys.exit(main())
