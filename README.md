<div align="center">

# 🧩 sopkit

**Modular, git-versioned instructions for task-driven agents.**

Write shared instructions and SOPs once. sopkit builds each agent's full prompt<br>
and shows exactly which agents a change touches.

[Quickstart](#quickstart) · [How it works](#how-it-works) · [Commands](#commands) · [Format](FORMAT.md) · [Example](examples/livekit-restaurant)

</div>

---

## Why

Running one agent per customer usually means dozens of near-identical prompts maintained by hand. A tone tweak means editing all of them. A new rule means remembering which ones need it. Copies drift.

sopkit keeps the shared parts in one place, in git, and builds every agent's prompt from them.

## Quickstart

**1. Install**

```sh
curl -fsSL https://raw.githubusercontent.com/amanmibra/sopkit/main/install.sh | sh
```

Installs [uv](https://docs.astral.sh/uv/) if needed. Already have uv? `uv tool install 'sopkit @ git+https://github.com/amanmibra/sopkit'`

**2. Import your existing prompts** with your coding agent

```sh
sopkit skills install        # adds .claude/skills/sopkit-import
```

Then run `/sopkit-import` in Claude Code. The agent turns your prompts into sopkit files, checks that nothing was lost, and asks you to approve a one-screen plan before it writes anything.

<details>
<summary>What the import does, step by step</summary>

| Step | Who | What happens |
|---|---|---|
| Collect | agent | Copies each existing prompt into `sops/originals/`, asks for anything it can't find |
| Map | `sopkit overlap` | Finds text every prompt shares, text some share, and near-copies that differ by a value (or have drifted) |
| Plan | you | Approve a one-screen plan: which bases and SOPs, which to lock, how to handle drift |
| Build | agent | Writes the files, keeping the original wording |
| Verify | `sopkit compare` | Fails if any original sentence is missing or changed; the agent repeats until it passes |
| Review | `sopkit check` | Flags duplicates and conflicts (10pm vs 11pm, "always X" vs "never X") for you to decide |
| Commit | you | Approve a one-screen summary before anything is committed |

Codex, Cursor and others: `sopkit skills install --dir <their skills folder>`, or ask the agent to follow `.claude/skills/sopkit-import/SKILL.md`.

</details>

**3. Load the built prompt in your agent**

```ts
const instructions = readFileSync(`sops/build/${agentId}.prompt.md`, "utf8");
```

See the [LiveKit example](examples/livekit-restaurant) for a complete agent.

## How it works

Prompts are built from three kinds of files:

| | File | Holds | Reaches agents by |
|---|---|---|---|
| 🧱 | `bases/*.md` | identity, tone, context, policy | `inherits:` in the agent, or `agents: "*"` |
| 📋 | `procedures/*.yaml` | SOPs: goal, steps, never-do's, warning signs, tools | `agents: [...]` in the SOP |
| 🎙️ | `agents/*.yaml` | platform id, values for `{{placeholders}}`, agent-only text | one file per agent |

Lock a base or SOP (`locked: true`) and no agent can drop it.

Change one shared file and see what moves before you merge:

```console
$ sopkit plan sops --against main
3 agents change:
  base `brand-voice` edited → 3 agents: luigis-trattoria, sakura-sushi, tonys-pizza
  SOP `reservations` edited → 2 agents: luigis-trattoria, sakura-sushi

--- a/sakura-sushi.prompt.md
+++ b/sakura-sushi.prompt.md
-Speak warmly and briefly. Ask one question at a time.
+Speak warmly and briefly. Use the caller's name once you have it. Ask one question at a time.
```

## Commands

| Command | What it does |
|---|---|
| `sopkit validate sops` | Check the files |
| `sopkit render sops` | Build one full prompt per agent into `sops/build/` |
| `sopkit plan sops --against main` | Which agents a change touches, and why, with diffs |
| `sopkit check sops` | Duplicated text and conflicting instructions |
| `sopkit overlap <dir>` | What a set of existing prompts have in common |
| `sopkit compare sops --originals <dir>` | Confirm built prompts still say everything the originals did |
| `sopkit skills install` | Install the `/sopkit-import` skill |
| `sopkit guide` | Print the format reference |

## Serving prompts

sopkit builds prompts into `sops/build/`, and the simplest setup ships them with your agent's code. To have agents fetch their prompt when a call starts instead (so a merge goes live without a redeploy), use **sopserve**, the companion server, which is in its own repo and still early.

## Working with coding agents

[FORMAT.md](FORMAT.md) is the full reference, written for people and agents. Add this to your project's `AGENTS.md` or `CLAUDE.md`:

```markdown
Agent instructions live in `sops/` in the sopkit format.
Run `sopkit guide` and read it before editing anything there.
Finish with `sopkit validate sops`, `sopkit check sops` and `sopkit plan sops --against main`.
```

For editor autocomplete, point `yaml-language-server` at the schemas in [`spec/`](spec).

## Status

Early, and moving fast. See the [roadmap](ROADMAP.md): next up are more checks and suggestions from real calls. Serving, the GitHub App and the TypeScript client are part of sopserve.

## Contributing

Contributions welcome. Open an issue first for anything big. See [CONTRIBUTORS.md](CONTRIBUTORS.md).

```sh
uv sync && uv run pytest
```

## License

[Apache-2.0](LICENSE)
