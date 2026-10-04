import json
import shutil
from pathlib import Path

import pytest

import opensop
from opensop.schema import SPEC_DIR, render_schemas

FIXTURE = Path(__file__).parent / "fixtures" / "restaurants"


@pytest.fixture
def build():
    return opensop.render_workspace(opensop.load_workspace(FIXTURE / "sops"))


@pytest.fixture
def repo(tmp_path):
    """A writable copy of the fixture folder, for tests that edit files."""
    dest = tmp_path / "sops"
    shutil.copytree(FIXTURE / "sops", dest)
    return dest


def headings(prompt: str) -> list[str]:
    return [line[4:] for line in prompt.splitlines() if line.startswith("### ")]


def test_build_matches_golden_output(build, tmp_path):
    opensop.write_build(build, tmp_path)
    expected = FIXTURE / "expected"
    assert sorted(p.name for p in tmp_path.iterdir()) == sorted(p.name for p in expected.iterdir())
    for path in expected.iterdir():
        assert (tmp_path / path.name).read_text() == path.read_text(), path.name


def test_bases_render_parents_first_then_targeted_then_bottom(build):
    tonys = build.agents["tonys-pizza"]
    assert [b.id for b in tonys.bases] == ["restaurant-host", "pizza-context", "brand-voice", "closing"]
    prompt = tonys.prompt
    assert prompt.index("phone host") < prompt.index('12" and 16"') < prompt.index("Speak warmly")
    assert prompt.index("Speak warmly") < prompt.index("wood-fired") < prompt.index("## Procedures")
    assert prompt.rstrip().endswith("pickup or delivery time.")


def test_sop_targeting_order_and_exclude(build):
    assert headings(build.agents["tonys-pizza"].prompt) == ["Allergen check", "Delivery", "Large orders"]
    assert headings(build.agents["luigis-trattoria"].prompt) == ["Allergen check", "Delivery", "Large orders", "Reservations"]
    assert headings(build.agents["sakura-sushi"].prompt) == ["Allergen check", "Reservations"]


def test_variables_use_agent_values_over_workspace_defaults(build):
    assert "tonys.com/allergens" in build.agents["tonys-pizza"].prompt
    assert "transfer to the manager on duty" in build.agents["tonys-pizza"].prompt
    assert "transfer to the head chef" in build.agents["sakura-sushi"].prompt
    assert "{{" not in "".join(r.prompt for r in build.agents.values())


def test_auto_delivery_keeps_guards_in_prompt_and_steps_in_tool(build):
    luigis = build.agents["luigis-trattoria"]
    assert "Never promise a pickup time" in luigis.prompt
    assert "Check kitchen capacity" not in luigis.prompt
    assert "id `large-orders`" in luigis.prompt
    payload = luigis.tool_payload["large-orders"]
    assert payload["procedureSteps"][1] == {"text": "Check kitchen capacity for the requested time", "tool": "check_capacity"}
    assert "transfer to the manager on duty" in payload["text"]
    assert "sakura-sushi" not in {a for a, r in build.agents.items() if r.tool_payload}


def test_tool_steps_render_as_instructions_and_are_listed(build):
    tonys = build.agents["tonys-pizza"]
    assert "Use the `lookup_allergens` tool." in tonys.prompt
    assert "This applies to the `place_order` tool." in tonys.prompt
    assert tonys.tools == ["check_capacity", "check_delivery_zone", "lookup_allergens", "place_order", "transfer_to_staff"]


def test_editing_a_shared_base_changes_every_agent_that_uses_it(repo, build):
    path = repo / "bases" / "pizza-context.md"
    path.write_text(path.read_text().replace('12" and 16"', '10", 12" and 16"'))
    after = opensop.render_workspace(opensop.load_workspace(repo))
    changed = {a for a in build.agents if build.agents[a].hash != after.agents[a].hash}
    assert changed == {"tonys-pizza"}

    path = repo / "bases" / "brand-voice.md"
    path.write_text(path.read_text().replace("briefly", "concisely"))
    final = opensop.render_workspace(opensop.load_workspace(repo))
    assert {a for a in after.agents if after.agents[a].hash != final.agents[a].hash} == set(build.agents)


def test_lock_lists_blocks_and_tools(build):
    lock = build.lock()
    sakura = lock["agents"]["sakura-sushi"]
    assert sakura["platform_ref"] == "livekit:sakura-sushi"
    assert [(b["kind"], b["id"]) for b in sakura["blocks"]] == [
        ("agent", "sakura-sushi"),
        ("base", "restaurant-host"),
        ("base", "brand-voice"),
        ("base", "closing"),
        ("sop", "allergen-check"),
        ("sop", "reservations"),
    ]
    assert "check_delivery_zone" not in sakura["tools"]


def test_write_build_removes_stale_files(build, tmp_path):
    (tmp_path / "old-agent.prompt.md").write_text("stale")
    opensop.write_build(build, tmp_path)
    assert not (tmp_path / "old-agent.prompt.md").exists()
    assert json.loads((tmp_path / "lock.json").read_text())["version"] == 1


def test_spec_is_up_to_date():
    for name, text in render_schemas().items():
        assert (SPEC_DIR / name).read_text() == text, f"spec/{name} is stale; run `python -m opensop.schema`"
