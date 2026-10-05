# 📋🧩 Example: a behavior gate for any test setup

A GitHub Action that works out **which agents a pull request changes** and hands their ids to **your own test command**. Use it with whatever you already have: Braintrust, pytest, a simulation tool, a script that calls your agents.

```
.github/workflows/behavior-gate.yml   copy into your repo, then fill in one step
```

## What it does

| Situation | Agents tested |
|---|---|
| PR changes SOP files | The agents whose built prompt changed (`opensop plan --json` against the base branch) |
| PR changes only tests, or SOP edits that don't change any prompt | All agents |
| Started by hand with `agents: "la-casita"` | Those agents (unknown ids fail the run) |
| Started by hand with no agents | All agents |

Agents removed in the PR are skipped. A shared change reaches only the agents that use it: editing the English brand voice in the [restaurant example](../livekit-restaurant) tests three agents and skips `la-casita`, which uses the Spanish one.

## What your command gets

The `test` job sets these, so your command can take ids in whatever form it needs:

| Variable | Example |
|---|---|
| `AGENT_IDS` | `tonys-pizza sakura-sushi` (OpenSOP ids: file names in `agents/`) |
| `AGENT_REFS` | `livekit:tonys-pizza livekit:sakura-sushi` |
| `AGENT_PLATFORM_IDS` | the platform's own ids (LiveKit `agent_name`, Vapi assistant id, ElevenLabs `agent_id`) |
| `AGENTS_JSON` | `[{"id": "tonys-pizza", "platform_ref": "livekit:tonys-pizza", "platform": "livekit", "platform_id": "tonys-pizza"}, ...]` |
| `ALL_AGENTS` | `true` when testing every agent |

Fill in the one step:

```yaml
- name: Run your tests
  run: npm run test:agents -- --agents "$AGENT_IDS"
```

Other shapes it fits:

```sh
pytest tests/agents -k "$(echo $AGENT_IDS | sed 's/ / or /g')"     # pytest, filtered by agent
for id in $AGENT_PLATFORM_IDS; do ./scripts/simulate.sh "$id"; done  # one run per agent
python evals/run_evals.py --base origin/main                        # the Braintrust example
```

To test agents in parallel instead, uncomment the `test-each` job: it runs once per agent with `matrix.agent.id`, `matrix.agent.platform_id` and so on.

## Set it up

1. Copy `.github/workflows/behavior-gate.yml` into your repo.
2. Set `OPENSOP_ROOT` if your OpenSOP folder isn't `sops/`, and the `paths:` your tests live in.
3. Replace the placeholder in **Run your tests** with your command (it fails on purpose until you do).
4. Make the **behavior-gate / test** check required in your branch protection rules, so failing tests block the merge.

The ids come from `opensop plan --json` and `opensop agents --json`, which you can also call from your own scripts:

```sh
opensop plan sops --against origin/main --json | jq -r '.changes[] | select(.status != "removed") | .agent'
opensop agents sops --json | jq -r '.[] | "\(.id) \(.platform_ref)"'
```
