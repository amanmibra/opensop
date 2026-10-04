# Roadmap

## Done

- **Format.** Bases (inherited prompt text, lockable), SOPs (goal, scope, guidance, steps, forbidden actions, warning signs, tools) and agents keyed by platform id. JSON Schemas in `spec/`; reference in [FORMAT.md](FORMAT.md).
- **Rendering and validation.** One full prompt per agent, `lock.json` with block hashes, clear errors (including YAML colon traps).
- **`sopkit plan`.** Which agents a change touches and because of which block, with a prompt diff per agent, against a git ref or the committed build.
- **HTTP API.** Validate, render, plan, publish, and serve each agent's prompt at call start, logging which version was served when. File-based store, so it self-hosts anywhere.
- **Import via coding agents.** A `/sopkit-import` skill plus `sopkit overlap` (what prompts share), `sopkit compare` (nothing lost or changed) and `sopkit check` (duplicates and mechanical conflicts).
- **LiveKit example.** A TypeScript agent that loads its prompt from the build ([examples/livekit-restaurant](examples/livekit-restaurant)).

## Next

1. **`sopkit publish` and a TypeScript client.** `loadPrompt(agent)`: fetch from the server with a short timeout and cache, falling back to the prompt shipped in `build/`.
2. **GitHub App.** Post `plan` as a PR check, publish on merge, open PRs for edits made elsewhere.
3. **Editor (web UI).** Compose agents with inherited blocks shown inline and a live preview of every affected prompt.
4. **More static checks.** Vague or uncheckable rules, SOPs without goals, prompt length per agent.
5. **Calls → clusters → suggestions.** Ingest calls and findings from any source, grade them against the exact SOP versions they ran with (goal met? required tools called? forbidden actions avoided?), group violations into clusters with example calls, and turn labeled clusters into suggested changes as PRs.
6. **Tool delivery.** An MCP / HTTP `get_sop` tool for `delivery: auto | tool` SOPs.
7. **Push adapters.** Write rendered prompts to Vapi and ElevenLabs by assistant / agent id, with drift detection. LiveKit has no instructions API, so LiveKit agents load their prompt instead.

## Principles

- Git holds what's accepted. Nothing reaches an agent without going through it.
- An agent's integration is one call: load the prompt.
- Platform ids, not new ids: agents are identified by what the platform already calls them.
