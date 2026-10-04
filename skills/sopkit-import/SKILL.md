---
name: sopkit-import
description: Convert existing voice/task agent prompts into sopkit files (shared bases, SOPs, one file per agent), verify nothing was lost, and report conflicts. Use when the user asks to import, migrate, modularize or "sopkit-ify" agent prompts or instructions, or runs /sopkit-import.
---

# Import existing prompts into sopkit

You are turning a team's hand-written agent prompts into a sopkit folder: shared text written once, procedures as SOPs, and a short file per agent. The rendered prompts must say everything the originals said. You do the judgment; the `sopkit` CLI does the counting and checking.

Work in this order. Don't skip the verification steps.

## 0. Make sure the CLI works

Run `sopkit --help`. If it's missing, install it:

```
curl -fsSL https://raw.githubusercontent.com/amanmibra/sopkit/main/install.sh | sh
```

Then run `sopkit guide` and read the whole format reference before writing any file.

## 1. Collect the originals

Find every agent's current prompt. Ask the user where they live if it isn't obvious: strings in code, prompt files, a database, or a platform dashboard (Vapi, ElevenLabs, LiveKit).

For each agent:
- Choose an id: lowercase kebab-case, ideally the platform's own name for it (LiveKit `agent_name`).
- Copy the prompt **verbatim** into `sops/originals/<id>.md`. Resolve any string concatenation or templating in code so the file is the text the model actually receives. Keep the code's own placeholders (e.g. `${restaurantName}`) as they are for now.
- Note the platform and platform id (LiveKit `agent_name`, Vapi assistant id, ElevenLabs agent_id). Ask the user if you can't find it.

If prompts differ by runtime values only (one template filled per customer), say so; the template becomes bases and SOPs, and the values become each agent's `variables`.

## 2. Find what's shared

```
sopkit overlap sops/originals
```

This lists sentences shared by all agents, shared by subsets, near-copies that differ only by a value, and how much is unique to each agent. Use it as the map for the next step.

## 3. Write the sopkit files

Create `sops/sopkit.yaml`, `sops/bases/`, `sops/procedures/`, `sops/agents/`, following the format reference. Decide where each piece of text goes:

| Text in the originals | Goes to |
|---|---|
| Shared by every agent, not a procedure (identity, tone, policies) | A base with `agents: "*"` |
| Shared by every agent and must never be dropped (safety, legal, compliance) | A base with `agents: "*"` and `locked: true`. **Ask the user** before locking. |
| Shared by a subset | A base those agents `inherits`, or a base with `agents: [ids]` |
| A near-copy that differs only by a value (name, hours, phone number, link) | One shared sentence with a `{{placeholder}}`; each agent's value in its `variables` |
| A procedure: a situation and what to do in it | An SOP (see below) |
| Said only at the end of the call | A base with `position: bottom` |
| Facts about one agent only | That agent's `instructions` |

**SOPs.** A passage is a procedure when it describes a situation and the steps to handle it ("If the caller wants to book a table, ask for…"). For each:
- `name`: a short title.
- `scope`: the situation that triggers it, in the original's words.
- `description`: the goal, the outcome that means it went well. If the original doesn't state one, write one plain sentence and **tell the user it's new**.
- `procedureSteps`: the ordered actions. Keep the original wording.
- `forbiddenActions`: "never / don't / do not" rules for this procedure.
- `warningSigns`: "if the caller says X, transfer / escalate" rules.
- `guidance`: anything else in the passage.
- If a step uses a tool (function call), set `tool:` to the tool's exact name from the agent code.
- `agents`: which agents had this procedure.

**Rules while writing:**
- Preserve wording. Don't improve, shorten or merge instructions during import; that's a separate, reviewed step. Small edits are allowed only to replace a value with a `{{placeholder}}` or to remove a sentence that's now duplicated by a shared block.
- Don't drop anything. If you can't place a sentence, put it in that agent's `instructions`.
- Quote any YAML string containing `: ` (colon space), or use a `|` block.
- Convert code placeholders like `${restaurantName}` to `{{restaurant_name}}` with a value per agent.

## 4. Validate, render, compare. Repeat until clean.

```
sopkit validate sops
sopkit render sops
sopkit compare sops --originals sops/originals
```

`compare` reports, per agent, sentences from the original **missing** from the rendered prompt, and sentences **added**. Fix every missing sentence; that's lost instruction. Expected additions are fine: SOP headings, "Goal:" lines you wrote, and "Use the `tool` tool." lines. Repeat until no agent has missing text.

## 5. Check for conflicts

```
sopkit check sops
```

This finds duplicated sentences, the same sentence with different numbers (`numeric_conflict`), "do X" vs "never X" (`negation_conflict`), near-identical sentences that drifted, and unused variables. These usually existed in the originals already; import makes them visible.

Then read each rendered prompt in `sops/build/` yourself and look for contradictions the CLI can't see, e.g. one block says delivery is available while the agent says pickup only, or two procedures give different escalation paths.

**Don't fix conflicts silently.** Which version is right is the user's decision. List them.

## 6. Report to the user

Summarize:
- Blocks created: bases (which are `"*"`, which are locked), SOPs (and which agents each reaches), agents.
- `compare` result: every agent's coverage, and any additions you made (especially new goals).
- Conflicts from `check` and from your own read, each with the blocks involved and a suggested resolution.
- Next steps: delete `sops/originals/` once they're happy (or keep it out of git); load prompts from `sops/build/<id>.prompt.md` in the agent code; run `sopkit plan sops --against main` before future changes.
