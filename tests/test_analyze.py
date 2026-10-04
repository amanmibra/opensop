import shutil
from pathlib import Path

import pytest

import opensop
from opensop import analyze
from opensop.cli import main

FIXTURES = Path(__file__).parent / "fixtures" / "restaurants"
SOPS = FIXTURES / "sops"
ORIGINALS = FIXTURES / "originals"


def read_dir(path: Path) -> dict[str, str]:
    return {p.stem: p.read_text() for p in sorted(path.glob("*.md"))}


@pytest.fixture
def repo(tmp_path):
    dest = tmp_path / "sops"
    shutil.copytree(SOPS, dest)
    return dest


def edit(path: Path, old: str, new: str) -> None:
    text = path.read_text()
    assert old in text
    path.write_text(text.replace(old, new))


# --- overlap ----------------------------------------------------------------------


def test_overlap_finds_text_every_prompt_shares():
    result = analyze.overlap(read_dir(ORIGINALS))
    everyone = [s.text for s in result.shared if len(s.agents) == 3]
    assert "Speak warmly and briefly." in everyone
    assert "Before hanging up, repeat the order total and the pickup or delivery time." in everyone


def test_overlap_finds_placeholder_candidates_and_drift():
    result = analyze.overlap(read_dir(ORIGINALS))
    by_first = {next(iter(nc.variants.values())): nc for nc in result.near_copies}
    host = next(nc for text, nc in by_first.items() if "phone host" in text)
    assert host.differing == {"luigis-trattoria": "Luigi's Trattoria", "sakura-sushi": "Sakura Sushi", "tonys-pizza": "Tony's Pizza"}
    upsell = next(nc for text, nc in by_first.items() if "upsell" in text)
    assert upsell.differing["luigis-trattoria"] == "twice"
    assert all("upsell" not in s.text for s in result.shared), "drifted text is reported once, as a near-copy"


# --- compare ----------------------------------------------------------------------


def test_compare_passes_when_nothing_was_lost():
    build = opensop.render_workspace(opensop.load_workspace(SOPS))
    originals = read_dir(FIXTURES / "expected")
    originals = {k.removesuffix(".prompt"): v for k, v in originals.items()}
    assert all(r.ok and r.coverage == 1.0 and not r.added for r in analyze.compare(build, originals))


def test_compare_reports_changed_missing_and_reworded():
    build = opensop.render_workspace(opensop.load_workspace(SOPS))
    originals = read_dir(ORIGINALS)
    originals["sakura-sushi"] += "\nGift cards can be bought at the counter on weekends.\n"
    originals["sakura-sushi"] = originals["sakura-sushi"].replace("Ask one question at a time.", "Ask up to two questions at a time.")
    results = {r.agent: r for r in analyze.compare(build, originals)}

    assert results["luigis-trattoria"].changed == [("Never upsell more than twice per call.", "Never upsell more than once per call.")]
    assert results["sakura-sushi"].missing == ["Gift cards can be bought at the counter on weekends."]
    assert results["sakura-sushi"].changed == [("Ask up to two questions at a time.", "Ask one question at a time.")]
    assert any(u.startswith("ALLERGIES:") for u in results["tonys-pizza"].reworded)
    assert not any(r.ok for r in results.values() if r.agent != "tonys-pizza" or r.changed)
    assert not any("tool." in u for r in results.values() for u in r.added), "opensop's own tool lines are not 'added'"


def test_cli_compare_exit_code(capsys):
    assert main(["compare", str(SOPS), "--originals", str(ORIGINALS)]) == 1
    out = capsys.readouterr().out
    assert "('twice' → 'once')" in out


# --- check ------------------------------------------------------------------------


def test_check_is_clean_on_the_fixture():
    assert analyze.check(opensop.load_workspace(SOPS)) == []


def test_check_finds_mechanical_conflicts(repo):
    edit(repo / "agents" / "tonys-pizza.yaml", "Pickup only after 10pm. Cash and card.", "Pickup only after 11pm. Cash and card. Speak warmly and briefly. Always confirm the delivery address.")
    edit(repo / "agents" / "tonys-pizza.yaml", "  menu_allergen_link: tonys.com/allergens", "  menu_allergen_link: tonys.com/allergens\n  old_phone: 555-0100")
    (repo / "bases" / "pizza-context.md").write_text((repo / "bases" / "pizza-context.md").read_text().rstrip() + " Pickup only after 10pm.\n")
    edit(repo / "procedures" / "delivery-handling.yaml", "procedureSteps:", "forbiddenActions:\n  - Never confirm the delivery address\nprocedureSteps:")

    findings = {f.code: f for f in analyze.check(opensop.load_workspace(repo))}
    assert set(findings) == {"numeric_conflict", "duplicate_text", "negation_conflict", "unused_variable"}
    assert findings["numeric_conflict"].sources == [("base `pizza-context`", "Pickup only after 10pm."), ("agent `tonys-pizza`", "Pickup only after 11pm.")]
    assert findings["negation_conflict"].agents == ["tonys-pizza"]
    assert findings["duplicate_text"].sources[0][0] == "base `brand-voice`"


def test_check_reports_a_shared_conflict_once_for_all_agents(repo):
    (repo / "bases" / "closing.md").write_text((repo / "bases" / "closing.md").read_text().rstrip() + " Before hanging up, never repeat the order total and the pickup or delivery time.\n")
    [finding] = analyze.check(opensop.load_workspace(repo))
    assert finding.code == "negation_conflict"
    assert finding.agents == ["luigis-trattoria", "sakura-sushi", "tonys-pizza"]


# --- skills -----------------------------------------------------------------------


def test_skills_install_covers_claude_code_codex_and_opencode(tmp_path, capsys, monkeypatch):
    monkeypatch.chdir(tmp_path)
    assert main(["skills", "install"]) == 0
    for folder in (".claude/skills", ".agents/skills"):
        skill = (tmp_path / folder / "opensop-import" / "SKILL.md").read_text()
        assert skill.startswith("---\nname: opensop-import\n")
        for command in ("opensop overlap", "opensop compare", "opensop check", "opensop guide"):
            assert command in skill
    out = capsys.readouterr().out
    assert "/opensop-import" in out and "$opensop-import" in out and "OpenCode" in out


def test_skills_install_for_one_agent(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    assert main(["skills", "install", "--agent", "codex"]) == 0
    assert (tmp_path / ".agents/skills/opensop-import/SKILL.md").exists()
    assert not (tmp_path / ".claude").exists()
    assert main(["skills", "install", "--dir", "custom"]) == 0
    assert (tmp_path / "custom/opensop-import/SKILL.md").exists()


def test_check_finds_a_number_conflict_inside_a_longer_sentence(repo):
    edit(repo / "agents" / "tonys-pizza.yaml", "Pickup only after 10pm. Cash and card.", "Pickup and delivery until 11pm. Delivery until 10pm on Sundays. Cash and card.")
    assert [f.code for f in analyze.check(opensop.load_workspace(repo))] == []  # different statements: no conflict
    edit(repo / "agents" / "tonys-pizza.yaml", "Delivery until 10pm on Sundays.", "Delivery until 10pm.")
    [finding] = analyze.check(opensop.load_workspace(repo))
    assert finding.code == "numeric_conflict"
