---
name: opensop-import
description: Convert existing voice/task agent prompts into OpenSOP files (shared bases, SOPs, one file per agent), verify nothing was lost, and report conflicts. Asks the user to fill gaps and approve a short import plan before writing files, and again before committing. Use when the user asks to import, migrate, modularize or "opensop-ify" agent prompts or instructions, or runs /opensop-import.
---

# Import existing prompts into OpenSOP

You are turning a team's hand-written agent prompts into an OpenSOP folder: shared text written once, procedures as SOPs, and a short file per agent. The rendered prompts must say everything the originals said. You do the judgment; the `opensop` CLI does the counting and checking; the user makes the decisions.

The flow has two approval checkpoints:

1. **Before writing any OpenSOP file:** the user approves a one-screen import plan.
2. **Before committing or pushing:** the user approves a one-screen result summary.

Never commit, push or open a pull request without the second approval.

## How to ask questions

- Look first. Only ask what you can't find in the code, config or prompts.
- Batch questions: ask everything you need at one point, not one at a time. Use your question tool if you have one; otherwise a short numbered list.
- Offer a default with each question ("I'll use the LiveKit agent_name as the id unless you say otherwise"), so the user can answer "yes" to most of them.
- When the user doesn't know, take the option that keeps today's behavior, and note it in the plan.

## 0. Make sure the CLI works

Run `opensop --help`. If it's missing, install it:

```
curl -fsSL https://raw.githubusercontent.com/amanmibra/opensop/main/install.sh | sh
```

Then run `opensop guide` and read the whole format reference before writing any file.

## 1. Collect the originals

Find every agent's current prompt: strings in code, prompt files, a database, or a platform dashboard (Vapi, ElevenLabs, LiveKit).

For each agent:
- Choose an id: lowercase kebab-case, ideally the platform's own name for it (LiveKit `agent_name`).
- Copy the prompt **verbatim** into `sops/originals/<id>.md`. Resolve string concatenation or templating in code so the file is the text the model actually receives. Keep code placeholders (e.g. `${restaurantName}`) for now.
- Note the platform and platform id (LiveKit `agent_name`, Vapi assistant id, ElevenLabs agent_id).
- Note tool names the agent code registers, so SOP steps can reference them.

**Ask now (one batch), only what you couldn't find:**
- Where prompts live, if you couldn't find all of them, and whether any agents should be left out.
- Platform ids you couldn't find.
- Where the OpenSOP folder should go (default: `sops/` at the repo root).
- Prompts assembled at runtime from data you can't see (e.g. a database): ask for an export or a sample.

## 2. Find what's shared

```
opensop overlap sops/originals
```

This lists sentences shared by all agents, shared by subsets, near-copies that differ only by a value, and how much is unique to each agent. Use it as the map for the plan.

## 3. Checkpoint 1: the import plan

Before writing any file in `sops/` (other than `sops/originals/`), show the user a plan that fits on one screen. Don't paste prompt text; name things and say who gets them. Example:

```
Import plan: 30 agents (LiveKit), sops/ at the repo root

Bases (5)
  restaurant-host   identity + what the host does          all 30
  brand-voice       tone, one question at a time           all 30   lock? (recommended)
  pizza-context     sizes, toppings, gluten-free note      12 pizza agents
  allergy-policy    allergen disclaimer                    all 30   lock? (recommended)
  closing           repeat total and time before hanging up all 30, at the end

SOPs (4)
  allergen-check    all 30      steps use lookup_allergens
  reservations      18 agents
  large-orders      12 agents
  delivery          26 agents   (4 don't deliver)

Per agent: restaurant_name, hours, menu link become variables; 2–6 sentences stay in each agent's own instructions.

Decisions needed
  1. Lock brand-voice and allergy-policy so no agent can drop them? (default: yes)
  2. Upsell rule differs: 29 agents say "once", luigis-trattoria says "twice". Unify on "once"? (default: keep both, as a variable, and flag it)
  3. No stated goal for reservations and large-orders. Use these? (default: yes)
       reservations: "The caller has a confirmed table, or knows why one isn't available."
       large-orders: "The order is placed with a pickup time the kitchen can meet."
  4. Passages that may or may not be procedures: "late-night menu" in 3 agents. SOP or plain text? (default: plain text)
```

Then ask: **"OK to write these files? Answer the numbered decisions or say 'defaults'."** Wait for the answer. Apply their changes to the plan before writing.

What belongs in "Decisions needed":
- **Locks.** Never lock a block without the user's yes.
- **Drift.** The same sentence with different values across agents. The default keeps each agent's current behavior (a variable with each agent's value) and records it as a conflict. Unify only if the user says so.
- **Goals you'd have to invent.** Show the exact sentence you'd add.
- **Unclear classification.** Passages that could be a procedure or plain text.
- **Contradictions you already see.** Report them, don't resolve them.

## 4. Write the OpenSOP files

Create `sops/opensop.yaml`, `sops/bases/`, `sops/procedures/`, `sops/agents/`, following the approved plan and the format reference.

| Text in the originals | Goes to |
|---|---|
| Shared by every agent, not a procedure (identity, tone, policies) | A base with `agents: "*"` |
| Shared by every agent and must never be dropped | A base with `agents: "*"` and `locked: true` (only if approved) |
| Shared by a subset | A base those agents `inherits`, or a base with `agents: [ids]` |
| A near-copy that differs only by a value (name, hours, phone number, link) | One shared sentence with a `{{placeholder}}`; each agent's value in its `variables` |
| A procedure: a situation and what to do in it | An SOP (see below) |
| Said only at the end of the call | A base with `position: bottom` |
| Facts about one agent only | That agent's `instructions` |

**SOPs.** A passage is a procedure when it describes a situation and the steps to handle it ("If the caller wants to book a table, ask for…"). For each:
- `name`: a short title.
- `scope`: the situation that triggers it, in the original's words.
- `description`: the goal. Use the original's words if it states one; otherwise the sentence the user approved.
- `procedureSteps`: the ordered actions, in the original wording.
- `forbiddenActions`: "never / don't / do not" rules for this procedure.
- `warningSigns`: "if the caller says X, transfer / escalate" rules.
- `guidance`: anything else in the passage.
- If a step uses a tool, set `tool:` to the tool's exact name from the agent code.
- `agents`: which agents had this procedure.

**Rules while writing:**
- Preserve wording. Don't improve, shorten or merge instructions during import; that's a separate, reviewed step. Allowed edits: replacing a value with a `{{placeholder}}`, and removing a sentence that a shared block now provides.
- Don't drop anything. If you can't place a sentence, put it in that agent's `instructions`.
- Quote any YAML string containing `: ` (colon space), or use a `|` block.
- Convert code placeholders like `${restaurantName}` to `{{restaurant_name}}` with a value per agent.

If something comes up that the plan didn't cover and that changes what an agent would say, stop and ask before continuing.

## 5. Validate, render, compare. Repeat until clean.

```
opensop validate sops
opensop render sops
opensop compare sops --originals sops/originals
```

`compare` fails if any sentence from an original is **missing** or **changed** in the rendered prompt, and shows the changed words. Fix those; they're lost or altered instructions. **Reworded** lines (most words present, e.g. one sentence split into steps) don't fail; check each one says the same thing. **Added** lines should only be SOP headings and approved goals. Repeat until every agent passes.

## 6. Check for conflicts

```
opensop check sops
```

This finds duplicated sentences, the same sentence with different numbers, "do X" vs "never X", near-identical sentences that drifted, and unused variables. Then read each rendered prompt in `sops/build/` yourself for contradictions the CLI can't see (e.g. one block offers delivery while the agent says pickup only).

**Don't fix conflicts silently.** List them for the user.

## 7. Checkpoint 2: the result summary

Show one screen, then ask before committing:

```
Imported 30 agents into sops/

  5 bases (2 locked), 4 SOPs, 30 agent files
  compare: all 30 agents pass (0 missing, 0 changed; 41 sentences reworded into SOP steps)
  added: 2 goals you approved

Conflicts to decide later (unchanged from today's behavior)
  1. upsell: "once" (29 agents) vs "twice" (luigis-trattoria), kept as a variable
  2. tonys-pizza says "pickup only after 10pm"; pizza-context says delivery until 11pm

Files: sops/opensop.yaml, sops/bases/ (5), sops/procedures/ (4), sops/agents/ (30), sops/build/ (generated)
Not included: sops/originals/ (your old prompts, for reference; delete or add to .gitignore)

Commit these on a new branch and open a PR? (default: commit on branch opensop-import, don't push)
```

Only commit, push or open a PR after the user says so, and only as far as they said (commit only, push, or PR). Don't include `sops/originals/` unless asked.

Then list the next steps:
- Load each agent's prompt from `sops/build/<id>.prompt.md` in the agent code instead of the hard-coded string.
- Before future edits, run `opensop plan sops --against main` to see which agents a change reaches.
