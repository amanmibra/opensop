# Notes for coding agents

## Writing OpenSOP files (bases, SOPs, agents)

Read [FORMAT.md](FORMAT.md) before creating or editing any file in an OpenSOP folder. It is the complete format reference and ends with a checklist. In a project that uses OpenSOP, `opensop guide` prints the same reference.

## Working on this repo

- Python 3.12+, managed with uv: `uv sync`, `uv run pytest`.
- `src/opensop/models.py` is the source of truth for the format. After changing it, run `uv run python -m opensop.schema` to regenerate `spec/`, and update FORMAT.md.
- `tests/fixtures/restaurants/expected/` is golden output. If a rendering change is intended, regenerate it with `uv run opensop render tests/fixtures/restaurants/sops --out tests/fixtures/restaurants/expected` and review the diff.
- `examples/livekit-restaurant/sops/` must stay valid and its `build/` current; a test checks both. Run `uv run opensop render examples/livekit-restaurant/sops` after changing rendering.
- `skills/` holds skills shipped to users (`opensop skills install`). Keep them in sync with the CLI's commands and output.
