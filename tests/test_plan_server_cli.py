import shutil
import subprocess
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

import sopkit
from sopkit.cli import main
from sopkit.loader import read_files
from sopkit.plan import make_plan, read_snapshot, snapshot
from sopkit.server import create_app
from sopkit.store import FileStore

FIXTURE = Path(__file__).parent / "fixtures" / "restaurants" / "sops"


@pytest.fixture
def repo(tmp_path):
    dest = tmp_path / "sops"
    shutil.copytree(FIXTURE, dest)
    return dest


def edit(path: Path, old: str, new: str) -> None:
    text = path.read_text()
    assert old in text
    path.write_text(text.replace(old, new))


def render(root):
    return snapshot(sopkit.render_workspace(sopkit.load_workspace(root)))


# --- plan ---------------------------------------------------------------------


def test_plan_attributes_changes_to_blocks(repo):
    before = render(repo)
    edit(repo / "bases" / "brand-voice.md", "briefly", "concisely")
    edit(repo / "procedures" / "reservations.yaml", "Never double-book a table", "Never double-book or overbook a table")
    plan = make_plan(before, render(repo))

    assert [c.agent_id for c in plan.changes] == ["luigis-trattoria", "sakura-sushi", "tonys-pizza"]
    assert plan.by_block() == {
        ("base:brand-voice", "edited"): ["luigis-trattoria", "sakura-sushi", "tonys-pizza"],
        ("sop:reservations", "edited"): ["luigis-trattoria", "sakura-sushi"],
    }
    assert "base `brand-voice` edited → 3 agents" in plan.text()
    assert "-Speak warmly and briefly." in plan.changes[0].diff
    assert "+Speak warmly and concisely." in plan.changes[0].diff


def test_plan_reports_targeting_changes(repo):
    before = render(repo)
    edit(repo / "procedures" / "reservations.yaml", "[sakura-sushi, luigis-trattoria]", "[sakura-sushi]")
    plan = make_plan(before, render(repo))
    assert plan.by_block() == {("sop:reservations", "removed"): ["luigis-trattoria"]}
    assert "SOP `reservations` no longer applies → 1 agent: luigis-trattoria" in plan.summary()


def test_plan_attributes_default_variable_changes_to_workspace(repo):
    before = render(repo)
    edit(repo / "sopkit.yaml", "the manager on duty", "the shift lead")
    plan = make_plan(before, render(repo))
    assert plan.by_block() == {("workspace:sopkit.yaml", "edited"): ["luigis-trattoria", "tonys-pizza"]}


def test_plan_new_and_removed_agents(repo):
    before = render(repo)
    (repo / "agents" / "sakura-sushi.yaml").unlink()
    shutil.copy(repo / "agents" / "luigis-trattoria.yaml", repo / "agents" / "luigis-brooklyn.yaml")
    edit(repo / "agents" / "luigis-brooklyn.yaml", "livekit: luigis-trattoria", "livekit: luigis-brooklyn")
    edit(repo / "procedures" / "reservations.yaml", "[sakura-sushi, luigis-trattoria]", "[luigis-trattoria]")
    plan = make_plan(before, render(repo))
    assert {(c.agent_id, c.status) for c in plan.changes} == {("luigis-brooklyn", "added"), ("sakura-sushi", "removed")}


def test_no_change_means_empty_plan():
    assert make_plan(render(FIXTURE), read_snapshot(FIXTURE.parent / "expected")).empty


# --- server -------------------------------------------------------------------


@pytest.fixture
def client(tmp_path):
    return TestClient(create_app(FileStore(tmp_path / "data"), token="secret"), headers={"Authorization": "Bearer secret"})


def test_server_requires_token(tmp_path):
    client = TestClient(create_app(FileStore(tmp_path), token="secret"))
    assert client.post("/v1/validate", json={"files": {}}).status_code == 401


def test_validate_and_render_endpoints(client):
    files = read_files(FIXTURE)
    assert client.post("/v1/validate", json={"files": files}).json() == {"valid": True, "issues": []}

    body = client.post("/v1/render", json={"files": files}).json()
    expected = (FIXTURE.parent / "expected" / "tonys-pizza.prompt.md").read_text()
    assert body["agents"]["tonys-pizza"]["prompt"] == expected

    broken = {**files, "agents/tonys-pizza.yaml": files["agents/tonys-pizza.yaml"].replace("pizza-context", "nope")}
    assert client.post("/v1/validate", json={"files": broken}).json()["valid"] is False
    res = client.post("/v1/render", json={"files": broken})
    assert res.status_code == 422
    assert res.json()["issues"][0]["code"] == "unknown_base"


def test_plan_endpoint(client):
    base = read_files(FIXTURE)
    head = {**base, "bases/brand-voice.md": base["bases/brand-voice.md"].replace("briefly", "concisely")}
    body = client.post("/v1/plan", json={"base": base, "head": head}).json()
    assert body["by_block"] == [
        {"block": "base:brand-voice", "change": "edited", "agents": ["luigis-trattoria", "sakura-sushi", "tonys-pizza"]}
    ]
    assert body["markdown"].startswith("**sopkit plan:** 3 agents change")


def test_publish_then_fetch_prompt_logs_the_version(client):
    files = read_files(FIXTURE)
    published = client.post("/v1/workspaces/demo/publish", json={"files": files}).json()

    res = client.get("/v1/workspaces/demo/agents/tonys-pizza/prompt")
    assert res.status_code == 200
    assert res.text == (FIXTURE.parent / "expected" / "tonys-pizza.prompt.md").read_text()
    assert res.headers["X-Sopkit-Build"] == published["build_id"]

    by_ref = client.get("/v1/workspaces/demo/agents/livekit:tonys-pizza/prompt")
    assert by_ref.text == res.text

    fetches = client.get("/v1/workspaces/demo/fetches", params={"agent": "tonys-pizza"}).json()
    assert [f["hash"] for f in fetches] == [res.headers["X-Sopkit-Hash"]] * 2

    edited = {**files, "bases/brand-voice.md": files["bases/brand-voice.md"].replace("briefly", "concisely")}
    client.post("/v1/workspaces/demo/publish", json={"files": edited})
    assert "concisely" in client.get("/v1/workspaces/demo/agents/tonys-pizza/prompt").text


def test_get_sop_and_not_found(client):
    client.post("/v1/workspaces/demo/publish", json={"files": read_files(FIXTURE)})
    sop = client.get("/v1/workspaces/demo/agents/luigis-trattoria/sops/large-orders").json()
    assert sop["procedureSteps"][1]["tool"] == "check_capacity"
    assert client.get("/v1/workspaces/demo/agents/luigis-trattoria/sops/allergen-check").status_code == 404
    assert client.get("/v1/workspaces/demo/agents/nobody/prompt").status_code == 404
    assert client.get("/v1/workspaces/unpublished/agents/tonys-pizza/prompt").status_code == 404


def test_openapi_lists_the_customer_endpoints(client):
    paths = client.get("/openapi.json").json()["paths"]
    assert "/v1/workspaces/{workspace}/agents/{agent}/prompt" in paths
    assert "/v1/plan" in paths


# --- cli ----------------------------------------------------------------------


def test_cli_render_check_and_plan(repo, capsys):
    assert main(["render", str(repo)]) == 0
    assert main(["render", str(repo), "--check"]) == 0

    edit(repo / "bases" / "closing.md", "repeat the order total", "repeat the order and total")
    assert main(["render", str(repo), "--check"]) == 1
    capsys.readouterr()
    assert main(["plan", str(repo), "--summary"]) == 0
    assert "base `closing` edited → 3 agents" in capsys.readouterr().out


def test_cli_plan_against_git_ref(tmp_path, capsys):
    root = tmp_path / "agent-repo"
    shutil.copytree(FIXTURE, root / "sops")
    git = lambda *a: subprocess.run(["git", *a], cwd=root, check=True, capture_output=True)  # noqa: E731
    git("init", "-q", "-b", "main")
    git("add", ".")
    git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")

    edit(root / "sops" / "procedures" / "allergen-check.yaml", "Name the specific allergen", "Repeat the specific allergen")
    assert main(["plan", str(root / "sops"), "--against", "main", "--summary"]) == 0
    assert "SOP `allergen-check` edited → 3 agents" in capsys.readouterr().out


def test_cli_validate_reports_errors(repo, capsys):
    edit(repo / "agents" / "tonys-pizza.yaml", "inherits: [pizza-context]", "inherits: [nope]")
    assert main(["validate", str(repo)]) == 1
    assert "unknown_base" in capsys.readouterr().err


# --- examples and guide -------------------------------------------------------

EXAMPLE = Path(__file__).parents[1] / "examples" / "livekit-restaurant" / "sops"


def test_example_is_valid_and_its_build_is_current(capsys):
    assert sopkit.validate(sopkit.load_workspace(EXAMPLE)) == []
    assert main(["render", str(EXAMPLE), "--check"]) == 0, "run `sopkit render examples/livekit-restaurant/sops`"


def test_guide_prints_the_format_reference(capsys):
    assert main(["guide"]) == 0
    out = capsys.readouterr().out
    assert out.startswith("# The sopkit format")
    assert "Checklist for coding agents" in out


def test_format_reference_lists_every_validation_code():
    import re

    codes = set(re.findall(r'Issue\(\s*"([a-z_]+)"', (Path(sopkit.__file__).parent / "validate.py").read_text()))
    codes |= set(re.findall(r'Issue\(\s*"([a-z_]+)"', (Path(sopkit.__file__).parent / "loader.py").read_text()))
    guide = (Path(__file__).parents[1] / "FORMAT.md").read_text()
    assert {c for c in codes if f"`{c}`" not in guide} == set()
