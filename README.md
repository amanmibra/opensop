<div align="center">

# 📋🧩 sopc

**dbt for agent instructions.**

The SOP compiler: modular, git-versioned instructions for teams managing multiple task-driven agents.

Write shared instructions and SOPs once. sopc compiles each agent's full prompt<br>
and shows exactly which agents a change touches.

[Quickstart](#quickstart) · [How it works](#how-it-works) · [Commands](#commands) · [Format](FORMAT.md) · [Example](examples/livekit-restaurant)

</div>

---

## Why

An SOP is a standard operating procedure: what an agent should do in a given situation, step by step.

Running one agent per customer usually means dozens of near-identical prompts maintained by hand. A tone tweak means editing all of them. A new rule means remembering which ones need it. Copies drift.

sopc, the SOP compiler, keeps the shared parts (bases, SOPs, agents) in one place, in git, and compiles them into one prompt per agent.

Not related to Mozilla's `sops` (secrets).

## Quickstart

### Already have agent prompts?

Most teams start here: a prompt per agent, written by hand, mostly copied from each other. The `sopc-import` skill converts them for you.

**1. Add the import skill to your repo** (one line, run in the repo root):

```sh
curl -fsSL https://raw.githubusercontent.com/amanmibra/sopc/main/skills/sopc-import/SKILL.md --create-dirs -o .claude/skills/sopc-import/SKILL.md
```

For Codex, use `-o .agents/skills/sopc-import/SKILL.md` instead. OpenCode reads either.

**2. Run it in your coding agent:**

| Agent | Run |
|---|---|
| Claude Code | `/sopc-import` |
| Codex | `$sopc-import` (or pick it from `/skills`) |
| OpenCode | ask it to "use the sopc-import skill" |

That's the whole setup. The skill installs the `sopc` CLI if it's missing, finds your existing prompts (in code, files or a platform dashboard), turns them into sopc files, checks that nothing was lost, and asks you to approve a one-screen plan before it writes anything.

<details>
<summary>Claude Code plugin (shares the skill with your whole team)</summary>

```
/plugin marketplace add amanmibra/sopc
/plugin install sopc@sopc
```

Then run `/sopc:sopc-import` (plugin skills are prefixed with the plugin name). To set it up for everyone who opens the repo, add the marketplace to your project's `.claude/settings.json` under `extraKnownMarketplaces` and enable `sopc@sopc` in `enabledPlugins`; see [Claude Code's plugin docs](https://code.claude.com/docs/en/plugins/marketplace-reference).

</details>

<details>
<summary>What the import does, step by step</summary>

| Step | Who | What happens |
|---|---|---|
| Collect | agent | Copies each existing prompt into `sops/originals/`, asks for anything it can't find |
| Map | `sopc overlap` | Finds text every prompt shares, text some share, and near-copies that differ by a value (or have drifted) |
| Plan | you | Approve a one-screen plan: which bases and SOPs, which to lock, how to handle drift |
| Build | agent | Writes the files, keeping the original wording |
| Verify | `sopc compare` | Fails if any original sentence is missing or changed; the agent repeats until it passes |
| Review | `sopc check` | Flags duplicates and conflicts (10pm vs 11pm, "always X" vs "never X") for you to decide |
| Commit | you | Approve a one-screen summary before anything is committed |

</details>

### Starting from scratch?

No existing prompts to convert? Skip the skill: install the CLI (below), copy [`examples/livekit-restaurant/sops`](examples/livekit-restaurant/sops) as a starting point, and edit it. Every file is commented, and `sopc guide` prints the full format reference. Coding agents can write sopc files from it too.

### Then: load the built prompt in your agent

```ts
const instructions = readFileSync(`sops/build/${agentId}.prompt.md`, "utf8");
```

**Installing the CLI yourself** (for CI, or to run the commands below):

```sh
curl -fsSL https://raw.githubusercontent.com/amanmibra/sopc/main/install.sh | sh
```

Installs a single `sopc` binary into `~/.local/bin` (macOS, Linux, Windows; amd64 and arm64). Pin a version with `SOPC_REF=v0.0.6`, or pick the folder with `SOPC_INSTALL_DIR`. From source, with Rust: `cargo install --git https://github.com/amanmibra/sopc`. `sopc skills install` writes the import skill for Claude Code, Codex and OpenCode at once.

See the [LiveKit example](examples/livekit-restaurant) for a complete agent, the [Braintrust example](examples/braintrust-evals) for blocking SOP changes that make agents worse, and the [behavior gate](examples/behavior-gate) for running your own tests on just the agents a PR changes.

## How it works

Prompts are built from three kinds of files:

| | File | Holds | Reaches agents by |
|---|---|---|---|
| 🧱 | `bases/*.md` | identity, tone, context, policy | `inherits:` in the agent, or `agents: "*"` |
| 📋 | `procedures/*.md` (or `.yaml`) | SOPs: goal, steps, never-do's, warning signs, tools | `agents: [...]` in the SOP |
| 🎙️ | `agents/*.yaml` | platform id, values for `{{placeholders}}`, agent-only text | one file per agent |

SOPs are Markdown that reads like a checklist (YAML works too):

```markdown
---
agents: "*"
---
# Allergen check

**Goal:** Customer leaves knowing whether their order is safe for their allergy.

## Steps
1. Ask if anyone in the order has a food allergy
2. Check each item against {{menu_allergen_link}} `tool: lookup_allergens` `required`

## Never
- Never say an item is "allergen-free" or "safe"
```

- **Every SOP needs at least one step.** A goal is recommended (a warning, not an error).
- **The format is checked strictly:** only `## Steps`, `## Never`, `## Warning signs`, `**Goal:**` and `**When:**` are allowed, and every mistake is reported with its file and line (`procedures/takeout.md: error [md_unknown_section] line 11: ...`).
- **YAML SOPs still work,** and `sopc convert` switches between the two without changing any prompt.
- **Lock a base or SOP** (`locked: true`) and no agent can drop it.

`sopc fmt` keeps SOP files in one style, and it never deletes anything silently: if formatting or converting a file would remove comments, the file is left as it is and listed with those comments. Add `--yes` to allow it.

Change one shared file and see what moves before you merge:

```console
$ sopc plan sops --against main
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
| `sopc validate sops` | Check the files |
| `sopc render sops` | Build one full prompt per agent into `sops/build/` |
| `sopc plan sops --against main` | Which agents a change touches, and why, with diffs (`--json` for CI) |
| `sopc agents sops` | List agents with their platform ids, SOPs and tools (`--json` for CI) |
| `sopc affected sops --against main` | Which agents to test for a change, with the SOPs that changed (`--ci` for GitHub Actions) |
| `sopc check sops` | Duplicated text and conflicting instructions |
| `sopc fmt sops` | Rewrite Markdown SOPs in canonical style (`--yaml` for YAML too, `--check` for CI) |
| `sopc convert sops --to md` | Rewrite YAML SOPs as Markdown (or `--to yaml`), without changing any prompt; changes that would drop comments need `--yes` |
| `sopc overlap <dir>` | What a set of existing prompts have in common |
| `sopc compare sops --originals <dir>` | Confirm built prompts still say everything the originals did |
| `sopc skills install` | Install the import skill for Claude Code, Codex and OpenCode |
| `sopc guide` | Print the format reference |

## Serving prompts

sopc builds prompts into `sops/build/`, and the simplest setup ships them with your agent's code. To have agents fetch their prompt when a call starts instead (so a merge goes live without a redeploy), use **sopserve**, the companion server, which is in its own repo and still early.

## Working with coding agents

[FORMAT.md](FORMAT.md) is the full reference, written for people and agents. Add this to your project's `AGENTS.md` (Codex, OpenCode) or `CLAUDE.md` (Claude Code):

```markdown
Agent instructions live in `sops/` in the sopc format.
Run `sopc guide` and read it before editing anything there.
Finish with `sopc fmt sops`, `sopc validate sops`, `sopc check sops` and `sopc plan sops --against main`.
Never pass `--yes` to `sopc fmt` or `sopc convert` unless I've approved removing the comments it lists.
```

For editor autocomplete, point `yaml-language-server` at the schemas in [`spec/`](spec).

## Status

Early. sopc is deliberately just the format and the tools to build and check it; serving prompts and evaluating calls are left to other tools. See the [roadmap](ROADMAP.md).

## Contributing

Contributions welcome. Open an issue first for anything big. See [CONTRIBUTORS.md](CONTRIBUTORS.md).

```sh
cargo test
```

## License

[Apache-2.0](LICENSE)
