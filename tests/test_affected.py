import json
import shutil
import subprocess
from pathlib import Path

import pytest

import opensop
from opensop.affected import affected
from opensop.cli import main

FIXTURE = Path(__file__).parent / "fixtures" / "restaurants" / "sops"


def build(root):
    return opensop.render_workspace(opensop.load_workspace(root))


@pytest.fixture
def repo(tmp_path):
    dest = tmp_path / "sops"
    shutil.copytree(FIXTURE, dest)
    return dest


def edit(path: Path, old: str, new: str) -> None:
    text = path.read_text()
    assert old in text
    path.write_text(text.replace(old, new))


def test_only_agents_whose_prompt_changed_with_the_blocks_that_changed_it(repo):
    base = build(repo)
    edit(repo / "procedures" / "reservations.yaml", "Never double-book a table", "Never double-book or overbook a table")
    edit(repo / "bases" / "pizza-context.md", '12" and 16"', '10", 12" and 16"')
    result = affected(build(repo), base)
    assert not result.all
    assert {a.id: (a.reason, a.changed, a.changed_sops) for a in result.agents} == {
        "luigis-trattoria": ("changed", ["sop:reservations"], ["reservations"]),
        "sakura-sushi": ("changed", ["sop:reservations"], ["reservations"]),
        "tonys-pizza": ("changed", ["base:pizza-context"], []),
    }


def test_nothing_changed(repo):
    head = build(repo)
    assert affected(head, head).agents == []
    everyone = affected(head, head, all_if_none=True)
    assert everyone.all and everyone.ids == sorted(head.agents)


def test_no_base_means_every_agent(repo):
    result = affected(build(repo), None)
    assert result.all and len(result.agents) == 3


def test_requested_by_opensop_id_or_platform_id(repo):
    edit(repo / "agents" / "luigis-trattoria.yaml", "livekit: luigis-trattoria", "vapi: asst_9f3e")
    head = build(repo)
    result = affected(head, head, requested=["asst_9f3e", "sakura-sushi", "vapi:asst_9f3e"])
    assert result.ids == ["luigis-trattoria", "sakura-sushi"]
    assert result.agents[0].platform_id == "asst_9f3e"
    assert result.github_outputs()["platform_ids"] == "asst_9f3e sakura-sushi"
    with pytest.raises(opensop.OpenSOPError) as exc:
        affected(head, head, requested=["la-casa"])
    assert "unknown agent(s): la-casa" in str(exc.value)


def test_cli_against_git_ref_writes_github_outputs(tmp_path, monkeypatch, capsys):
    root = tmp_path / "agent-repo"
    shutil.copytree(FIXTURE, root / "sops")
    git = lambda *a: subprocess.run(["git", *a], cwd=root, check=True, capture_output=True)  # noqa: E731
    git("init", "-q", "-b", "main")
    git("add", ".")
    git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
    edit(root / "sops" / "procedures" / "allergen-check.yaml", "Name the specific allergen", "Repeat the specific allergen")

    out, summary = tmp_path / "out", tmp_path / "summary"
    monkeypatch.setenv("GITHUB_OUTPUT", str(out))
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(summary))
    assert main(["affected", str(root / "sops"), "--against", "main", "--all-if-none", "--ci"]) == 0
    assert capsys.readouterr().out.split() == ["luigis-trattoria", "sakura-sushi", "tonys-pizza"]

    outputs = dict(line.split("=", 1) for line in out.read_text().splitlines())
    assert outputs["ids"] == "luigis-trattoria sakura-sushi tonys-pizza"
    assert outputs["count"] == "3" and outputs["all"] == "false"
    assert json.loads(outputs["matrix"])[0]["changed_sops"] == ["allergen-check"]
    assert "`tonys-pizza` (livekit `tonys-pizza`): sop:allergen-check" in summary.read_text()


def test_cli_json_format(repo, capsys):
    assert main(["affected", str(repo), "--agents", "tonys-pizza", "--format", "json"]) == 0
    data = json.loads(capsys.readouterr().out)
    assert data["count"] == 1 and data["agents"][0]["reason"] == "requested"
    assert data["agents"][0]["sops"] == ["allergen-check", "delivery-handling", "large-orders"]
