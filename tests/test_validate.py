import shutil
from pathlib import Path

import pytest

import sopkit

FIXTURE = Path(__file__).parent / "fixtures" / "restaurants" / "sops"


@pytest.fixture
def repo(tmp_path):
    dest = tmp_path / "sops"
    shutil.copytree(FIXTURE, dest)
    return dest


def edit(path: Path, old: str, new: str) -> None:
    text = path.read_text()
    assert old in text, f"{old!r} not in {path.name}"
    path.write_text(text.replace(old, new))


def codes(repo: Path) -> list[str]:
    with pytest.raises(sopkit.SopkitError) as exc:
        sopkit.render_workspace(sopkit.load_workspace(repo))
    return [i.code for i in exc.value.issues]


def test_fixture_is_valid():
    assert sopkit.validate(sopkit.load_workspace(FIXTURE)) == []


def test_locked_base_cannot_be_excluded(repo):
    edit(repo / "agents" / "sakura-sushi.yaml", "exclude: [delivery-handling]", "exclude: [delivery-handling, brand-voice]")
    assert codes(repo) == ["locked"]


def test_unlocked_base_can_be_excluded(repo):
    edit(repo / "agents" / "sakura-sushi.yaml", "exclude: [delivery-handling]", "exclude: [delivery-handling, closing]")
    build = sopkit.render_workspace(sopkit.load_workspace(repo))
    assert "closing" not in [b.id for b in build.agents["sakura-sushi"].bases]


def test_inheritance_cycle(repo):
    edit(repo / "bases" / "restaurant-host.md", "---\n---", "---\ninherits: [pizza-context]\n---")
    assert codes(repo) == ["inheritance_cycle"]


def test_unknown_base(repo):
    edit(repo / "agents" / "tonys-pizza.yaml", "inherits: [pizza-context]", "inherits: [pasta-context]")
    assert codes(repo) == ["unknown_base"]


def test_unknown_agent_in_targeting(repo):
    edit(repo / "procedures" / "reservations.yaml", "[sakura-sushi, luigis-trattoria]", "[sakura-sushi, luigis]")
    assert codes(repo) == ["unknown_agent"]


def test_platform_ref_can_be_used_in_targeting(repo):
    edit(repo / "procedures" / "reservations.yaml", "[sakura-sushi, luigis-trattoria]", '[sakura-sushi, "livekit:tonys-pizza"]')
    build = sopkit.render_workspace(sopkit.load_workspace(repo))
    assert "Reservations" in build.agents["tonys-pizza"].prompt
    assert "Reservations" not in build.agents["luigis-trattoria"].prompt


def test_unset_variable(repo):
    edit(repo / "agents" / "luigis-trattoria.yaml", "  menu_allergen_link: luigis.com/menu#allergens\n", "")
    assert codes(repo) == ["unset_variable"]


def test_duplicate_platform_ref(repo):
    edit(repo / "agents" / "sakura-sushi.yaml", "livekit: sakura-sushi", "livekit: tonys-pizza")
    assert codes(repo) == ["duplicate_platform_ref"]


def test_agent_needs_exactly_one_platform(repo):
    edit(repo / "agents" / "sakura-sushi.yaml", "livekit: sakura-sushi", "livekit: sakura-sushi\nvapi: asst_123")
    with pytest.raises(sopkit.SopkitError) as exc:
        sopkit.load_workspace(repo)
    assert [i.code for i in exc.value.issues] == ["invalid_field"]
    assert exc.value.issues[0].path == "agents/sakura-sushi.yaml"


def test_explicit_id_must_match_file_name(repo):
    edit(repo / "procedures" / "reservations.yaml", "name: Reservations", "id: bookings\nname: Reservations")
    with pytest.raises(sopkit.SopkitError) as exc:
        sopkit.load_workspace(repo)
    assert [i.code for i in exc.value.issues] == ["id_mismatch"]


def test_unknown_field_is_rejected(repo):
    edit(repo / "procedures" / "reservations.yaml", "name: Reservations", "name: Reservations\nsteps: []")
    with pytest.raises(sopkit.SopkitError) as exc:
        sopkit.load_workspace(repo)
    assert [i.code for i in exc.value.issues] == ["invalid_field"]


def test_missing_goal_is_a_warning(repo):
    edit(repo / "procedures" / "reservations.yaml", "description: The customer has a confirmed table, or knows exactly why one isn't available.\n", "")
    build = sopkit.render_workspace(sopkit.load_workspace(repo))
    assert [(w.code, w.severity) for w in build.warnings] == [("missing_goal", "warning")]


def load_codes(repo: Path) -> list[sopkit.Issue]:
    with pytest.raises(sopkit.SopkitError) as exc:
        sopkit.load_workspace(repo)
    return exc.value.issues


def test_colon_in_step_gets_a_clear_fix(repo):
    edit(repo / "procedures" / "reservations.yaml", "  - Never double-book a table", "  - Never say: we're fully booked")
    [issue] = load_codes(repo)
    assert issue.code == "colon_in_step"
    assert issue.path == "procedures/reservations.yaml"
    assert "forbiddenActions[0]" in issue.message
    assert '- "Never say: we\'re fully booked"' in issue.message


def test_quoted_colon_step_is_fine(repo):
    edit(repo / "procedures" / "reservations.yaml", "  - Never double-book a table", '  - "Never say: fully booked"')
    build = sopkit.render_workspace(sopkit.load_workspace(repo))
    assert "- Never say: fully booked" in build.agents["sakura-sushi"].prompt


def test_unquoted_boolean_and_empty_steps(repo):
    edit(repo / "procedures" / "reservations.yaml", "  - Never double-book a table", "  - no\n  -")
    assert [i.code for i in load_codes(repo)] == ["unquoted_value", "empty_step"]


def test_colon_in_unquoted_field_gets_a_hint(repo):
    edit(repo / "procedures" / "reservations.yaml", "scope: The customer wants", "scope: Note: the customer wants")
    [issue] = load_codes(repo)
    assert issue.code == "invalid_yaml"
    assert "put the text in quotes" in issue.message
