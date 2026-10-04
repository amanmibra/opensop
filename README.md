<div align="center">

# 🧩 sopkit

**Modular, git-versioned instructions for task-driven agents.**

Write shared prompt text and SOPs once. sopkit assembles every agent's full prompt,<br>
shows which agents a change touches, and serves the result to LiveKit, Vapi and ElevenLabs.

</div>

---

## Why

Teams running one voice agent per customer end up maintaining dozens of near-identical prompts by hand. A brand-voice tweak means editing every one of them; a new rule means remembering which agents need it. sopkit splits prompts into pieces:

| Piece | What it holds | Reaches agents by |
|---|---|---|
| 🧱 **Base** | identity, brand voice, context, policy | agents `inherits` it, or it targets `agents: "*"` |
| 📋 **SOP** | a procedure: goal, steps, forbidden actions, warning signs, tools | it targets `agents: [...]` |
| 🎙️ **Agent** | platform id, placeholder values, text only this agent gets | — |

Bases and SOPs can be `locked` so no agent can drop them. Everything lives in git, so review is a PR and rollback is a revert.

## Example

```
sops/
  sopkit.yaml
  bases/brand-voice.md             every agent, locked
  bases/restaurant-host.md         "You are the phone host for {{restaurant_name}}..."
  procedures/allergen-check.yaml   every agent
  procedures/reservations.yaml     only sakura-sushi and luigis-trattoria
  agents/sakura-sushi.yaml         livekit: sakura-sushi, inherits: [restaurant-host]
```

Change one shared base and see exactly what moves before you merge:

```
$ sopkit plan sops --against main
3 agents change:
  base `brand-voice` edited → 3 agents: luigis-trattoria, sakura-sushi, tonys-pizza
  SOP `reservations` edited → 2 agents: luigis-trattoria, sakura-sushi

--- a/sakura-sushi.prompt.md
+++ b/sakura-sushi.prompt.md
-Speak warmly and briefly. Ask one question at a time.
+Speak warmly and briefly. Use the caller's name once you have it. Ask one question at a time.
```

The full commented example is in [`examples/livekit-restaurant/`](examples/livekit-restaurant): a LiveKit agent in TypeScript that loads its prompt from `sops/build/`.

## Get started: import your existing prompts

Already have a prompt per agent? Let your coding agent do the conversion. sopkit ships a skill for it:

```
uv tool install 'sopkit[server] @ git+https://github.com/amanmibra/sopkit'
sopkit skills install                    # writes .claude/skills/sopkit-import/SKILL.md
```

Then, in Claude Code:

```
/sopkit-import
```

(Codex, Cursor and others: install with `sopkit skills install --dir <their skills folder>`, or ask the agent to follow `.claude/skills/sopkit-import/SKILL.md`.)

The agent copies each existing prompt into `sops/originals/`, and the CLI does the checking:

| Step | Command | What it does |
|---|---|---|
| Map | `sopkit overlap sops/originals` | Text every prompt shares, text a subset shares, and near-copies that differ only by a value (placeholder candidates, or drift: "upsell once" in two prompts, "twice" in the third) |
| Build | (the agent) | Writes shared bases, SOPs and one short file per agent, keeping the original wording |
| Verify | `sopkit compare sops --originals sops/originals` | Fails if any sentence from an original is missing or changed in the rebuilt prompt, and shows the changed words |
| Review | `sopkit check sops` | Duplicated text, the same sentence with different numbers, "always X" vs "never X", unused variables |

The agent repeats Build and Verify until nothing is lost, then reports the conflicts for you to decide. It doesn't resolve them on its own.

## Use

```
uv tool install 'sopkit[server] @ git+https://github.com/amanmibra/sopkit'
# or: pip install 'sopkit[server] @ git+https://github.com/amanmibra/sopkit'

sopkit validate sops/                  # check the files
sopkit render sops/                    # write sops/build/: one full prompt per agent + lock.json
sopkit plan sops/ --against main       # which agents change, because of which blocks
sopkit check sops/                     # duplicates and conflicting instructions
sopkit guide                           # print the format reference
SOPKIT_TOKEN=... sopkit serve          # HTTP API; OpenAPI at /openapi.json
```

An agent fetches its prompt when a call starts:

```
GET /v1/workspaces/<workspace>/agents/<agent>/prompt
Authorization: Bearer <token>
```

The response carries `X-Sopkit-Hash`, and the server logs which version each agent was served, so a call can always be traced to the exact prompt it ran with.

## The format

[**FORMAT.md**](FORMAT.md) is the complete reference: every file, every field, how a prompt is assembled, recipes, and validation codes. JSON Schemas are in [`spec/`](spec); add a `# yaml-language-server: $schema=...` line to get autocomplete and hover docs in your editor.

**Using a coding agent?** Point it at the format. In a project that uses sopkit, add this to your `AGENTS.md` or `CLAUDE.md`:

```markdown
Voice-agent instructions live in `sops/` in the sopkit format.
Run `sopkit guide` and read it before editing anything there.
Finish with `sopkit validate sops` and `sopkit plan sops --against main`.
```

## Status

Early. Working: the format, rendering, validation, `plan`, import via a coding-agent skill, conflict checks, the CLI, and the HTTP API with a file-based store. Next: a TypeScript client, a GitHub App (plans as PR checks, publish on merge), and suggestions from real calls. See [ROADMAP.md](ROADMAP.md).

## Contributing

See [CONTRIBUTORS.md](CONTRIBUTORS.md). Contributions welcome; open an issue first for anything big.

## License

[Apache-2.0](LICENSE)

## Develop

```
uv sync
uv run pytest
uv run python -m sopkit.schema   # regenerate spec/ after changing models.py
```
