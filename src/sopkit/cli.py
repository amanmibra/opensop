"""sopkit command line.

    sopkit validate [ROOT]
    sopkit render   [ROOT] [--out DIR] [--check]
    sopkit plan     [ROOT] [--against REF]
    sopkit serve    [--host H] [--port P] [--data-dir DIR]
    sopkit guide
"""

from __future__ import annotations

import argparse
import importlib.resources
import os
import subprocess
import sys
from pathlib import Path

from .build import write_build
from .issues import SopkitError
from .loader import load_workspace, load_workspace_files
from .plan import make_plan, read_snapshot, snapshot
from .render import render_workspace
from .validate import validate


class GitError(Exception):
    pass


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="sopkit", description="Modular, git-versioned instructions for task-driven agents.")
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

    sub.add_parser("guide", help="print the format reference (FORMAT.md)")

    p = sub.add_parser("serve", help="run the HTTP API")
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=8484)
    p.add_argument("--data-dir", default=os.environ.get("SOPKIT_DATA_DIR", ".sopkit-data"))

    args = parser.parse_args(argv)
    try:
        return {"validate": _validate, "render": _render, "plan": _plan, "serve": _serve, "guide": _guide}[args.command](args)
    except SopkitError as e:
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
        print(f"{out} is out of date; run `sopkit render`\n", file=sys.stderr)
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
    print(make_plan(before, after).text(diffs=not args.summary), end="")
    return 0


def _serve(args) -> int:
    try:
        import uvicorn

        from .server import create_app
        from .store import FileStore
    except ImportError:
        print("sopkit serve needs the server extra: pip install 'sopkit[server]'", file=sys.stderr)
        return 1
    uvicorn.run(create_app(FileStore(args.data_dir), os.environ.get("SOPKIT_TOKEN")), host=args.host, port=args.port)
    return 0


def _guide(args) -> int:
    print(read_guide(), end="")
    return 0


def read_guide() -> str:
    packaged = importlib.resources.files("sopkit") / "FORMAT.md"
    if packaged.is_file():
        return packaged.read_text()
    return (Path(__file__).resolve().parents[2] / "FORMAT.md").read_text()  # source checkout


def files_at_ref(root: Path, ref: str) -> dict[str, str]:
    """The sopkit source files under ROOT as they were at a git ref."""
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
    return rel == "sopkit.yaml" or (folder == "bases" and name.endswith(".md")) or (
        folder in ("procedures", "agents") and name.endswith(".yaml")
    )


def _git(cwd: Path, *args: str) -> str:
    result = subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True)
    if result.returncode != 0:
        raise GitError(f"git {' '.join(args)}: {result.stderr.strip()}")
    return result.stdout


if __name__ == "__main__":
    sys.exit(main())
