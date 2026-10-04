<div align="center">

# 📋🧩 OpenSOP

**Modular, git-versioned instructions for teams managing multiple task-driven agents.**

Write shared instructions and SOPs once. OpenSOP builds each agent's full prompt<br>
and shows exactly which agents a change touches.

[Quickstart](#quickstart) · [How it works](#how-it-works) · [Commands](#commands) · [Format](FORMAT.md) · [Example](examples/livekit-restaurant)

</div>

---

## Why

An SOP is a standard operating procedure: what an agent should do in a given situation, step by step.

Running one agent per customer usually means dozens of near-identical prompts maintained by hand. A tone tweak means editing all of them. A new rule means remembering which ones need it. Copies drift.

OpenSOP keeps the shared parts in one place, in git, and builds every agent's prompt from them.

## Quickstart

**1. Install**

```sh
curl -fsSL https://raw.githubusercontent.com/amanmibra/opensop/main/install.sh | sh
```

Installs [uv](https://docs.astral.sh/uv/) if needed. Already have uv? `uv tool install 'opensop @ git+https://github.com/amanmibra/opensop'`

**2. Import your existing prompts** with your coding agent

```sh
opensop skills install        # adds the opensop-import skill for Claude Code, Codex and OpenCode
```

Then run it in your coding agent:

| Agent | Run |
|---|---|
| Claude Code | `/opensop-import` |
| Codex | `$opensop-import` (or pick it from `/skills`) |
| OpenCode | ask it to "use the opensop-import skill" |

The agent turns your prompts into OpenSOP files, checks that nothing was lost, and asks you to approve a one-screen plan before it writes anything.

<details>
<summary>What the import does, step by step</summary>

| Step | Who | What happens |
|---|---|---|
| Collect | agent | Copies each existing prompt into `sops/originals/`, asks for anything it can't find |
| Map | `opensop overlap` | Finds text every prompt shares, text some share, and near-copies that differ by a value (or have drifted) |
| Plan | you | Approve a one-screen plan: which bases and SOPs, which to lock, how to handle drift |
| Build | agent | Writes the files, keeping the original wording |
| Verify | `opensop compare` | Fails if any original sentence is missing or changed; the agent repeats until it passes |
| Review | `opensop check` | Flags duplicates and conflicts (10pm vs 11pm, "always X" vs "never X") for you to decide |
| Commit | you | Approve a one-screen summary before anything is committed |

The skill is installed to `.claude/skills/` (Claude Code, OpenCode) and `.agents/skills/` (Codex, OpenCode). Use `--agent claude|codex|opencode` for just one. For other agents, `--dir <their skills folder>`, or ask the agent to follow `.agents/skills/opensop-import/SKILL.md`.

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
$ opensop plan sops --against main
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
| `opensop validate sops` | Check the files |
| `opensop render sops` | Build one full prompt per agent into `sops/build/` |
| `opensop plan sops --against main` | Which agents a change touches, and why, with diffs |
| `opensop check sops` | Duplicated text and conflicting instructions |
| `opensop overlap <dir>` | What a set of existing prompts have in common |
| `opensop compare sops --originals <dir>` | Confirm built prompts still say everything the originals did |
| `opensop skills install` | Install the import skill for Claude Code, Codex and OpenCode |
| `opensop guide` | Print the format reference |

## Serving prompts

OpenSOP builds prompts into `sops/build/`, and the simplest setup ships them with your agent's code. To have agents fetch their prompt when a call starts instead (so a merge goes live without a redeploy), use **sopserve**, the companion server, which is in its own repo and still early.

## Working with coding agents

[FORMAT.md](FORMAT.md) is the full reference, written for people and agents. Add this to your project's `AGENTS.md` (Codex, OpenCode) or `CLAUDE.md` (Claude Code):

```markdown
Agent instructions live in `sops/` in the opensop format.
Run `opensop guide` and read it before editing anything there.
Finish with `opensop validate sops`, `opensop check sops` and `opensop plan sops --against main`.
```

For editor autocomplete, point `yaml-language-server` at the schemas in [`spec/`](spec).

## Status

Early. OpenSOP is deliberately just the format and the tools to build and check it; serving prompts and evaluating calls are left to other tools. See the [roadmap](ROADMAP.md).

## Contributing

Contributions welcome. Open an issue first for anything big. See [CONTRIBUTORS.md](CONTRIBUTORS.md).

```sh
uv sync && uv run pytest
```

## License

[Apache-2.0](LICENSE)
