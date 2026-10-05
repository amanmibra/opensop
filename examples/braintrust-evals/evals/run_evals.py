"""Run Braintrust evals for the agents an OpenSOP change touches, and fail on regressions.

    python evals/run_evals.py --sops sops --cases evals/cases.yaml --base origin/main

1. Builds every agent's prompt twice: at --base (e.g. the PR's target branch) and as it is now.
2. Asks `opensop plan` which agents' prompts changed.
3. Picks the cases that apply to those agents (by SOP or by agent, see cases.yaml).
4. Runs the cases against both prompts as two Braintrust experiments, so the Braintrust
   UI shows them side by side.
5. Fails (exit 1) if a case scores lower with the new prompt than with the old one.

A case's model replies are produced by MODEL (default below) through an OpenAI-compatible
API. With BRAINTRUST_API_KEY set, calls go through the Braintrust AI proxy, so one key
covers OpenAI, Anthropic and others. Without it, OPENAI_API_KEY is used directly and
results stay local (nothing is sent to Braintrust).
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import dataclass, field
from pathlib import Path

import autoevals
import yaml
from autoevals import LLMClassifier
from braintrust import Eval
from openai import AsyncOpenAI, OpenAI

from opensop.cli import files_at_ref
from opensop.loader import load_workspace, load_workspace_files
from opensop.plan import make_plan, snapshot
from opensop.render import Build, render_workspace

MODEL = os.environ.get("OPENSOP_EVAL_MODEL", "gpt-4.1-mini")
JUDGE_MODEL = os.environ.get("OPENSOP_JUDGE_MODEL", MODEL)
PROXY_URL = "https://api.braintrust.dev/v1/proxy"
TRIALS = int(os.environ.get("OPENSOP_EVAL_TRIALS", "3"))  # model replies vary; average a few runs
REGRESSION_MARGIN = 0.25  # a case regresses if its average score drops by more than this


@dataclass
class Case:
    id: str
    turns: list[str]
    rubric: str
    sops: list[str] = field(default_factory=list)  # run for agents that have any of these SOPs
    agents: list[str] = field(default_factory=list)  # or for these agents ("*" = all)
    must_not_say: list[str] = field(default_factory=list)  # phrases that fail the case outright
    tools: dict[str, object] = field(default_factory=dict)  # what each tool returns in this case

    def applies_to(self, agent_id: str, agent_sops: set[str]) -> bool:
        return "*" in self.agents or agent_id in self.agents or bool(agent_sops & set(self.sops))


def client(cls=OpenAI):
    if os.environ.get("BRAINTRUST_API_KEY"):
        return cls(base_url=PROXY_URL, api_key=os.environ["BRAINTRUST_API_KEY"])
    return cls()


def tool_specs(names: list[str]) -> list[dict]:
    """Every tool the agent's SOPs name (from lock.json). Arguments are free-form; results come from the case."""
    return [
        {
            "type": "function",
            "function": {
                "name": name,
                "description": f"The {name} tool referenced in the agent's procedures.",
                "parameters": {"type": "object", "properties": {}, "additionalProperties": True},
            },
        }
        for name in names
    ]


def call_agent(llm: OpenAI, prompt: str, tools: list[str], case_tools: dict, turns: list[str]) -> str:
    """Play the caller's turns against the agent's prompt; return the transcript, tool calls included."""
    messages = [{"role": "system", "content": prompt + "\n\nYou are on a phone call. Reply with only what you would say."}]
    transcript = []
    for turn in turns:
        messages.append({"role": "user", "content": turn})
        transcript.append(f"CALLER: {turn}")
        for _ in range(5):  # let the agent call tools, then answer
            kwargs = {"tools": tool_specs(tools)} if tools else {}
            msg = llm.chat.completions.create(model=MODEL, messages=messages, **kwargs).choices[0].message
            messages.append(msg.model_dump(exclude_none=True))
            if not msg.tool_calls:
                transcript.append(f"AGENT: {msg.content or ''}")
                break
            for call in msg.tool_calls:
                result = case_tools.get(call.function.name, {"status": "ok"})
                transcript.append(f"TOOL: {call.function.name}({call.function.arguments}) -> {json.dumps(result)}")
                messages.append({"role": "tool", "tool_call_id": call.id, "content": json.dumps(result)})
    return "\n".join(transcript)


def no_forbidden_phrases(input, output, expected=None, metadata=None, **_):
    banned = [p.lower() for p in (metadata or {}).get("must_not_say", [])]
    agent_lines = "\n".join(line for line in output.splitlines() if line.startswith("AGENT:")).lower()
    hits = [p for p in banned if p in agent_lines]
    return {"name": "NoForbiddenPhrases", "score": 0 if hits else 1, "metadata": {"said": hits}}


def rubric_judge() -> LLMClassifier:
    return LLMClassifier(
        name="Rubric",
        prompt_template=(
            "You are grading a phone call between a caller and an AI agent.\n\n"
            "Call transcript (TOOL lines are the agent's tool calls and their results):\n{{output}}\n\n"
            "Requirement:\n{{expected}}\n\n"
            "Does the agent meet the requirement?\n(A) Yes\n(B) No"
        ),
        choice_scores={"A": 1, "B": 0},
        model=JUDGE_MODEL,
        use_cot=True,
    )


def run(label: str, build: Build, work: list[tuple[str, Case]], project: str, llm: OpenAI, local: bool, base_name: str | None):
    data = [
        {
            "input": {"agent": agent, "turns": case.turns, "tools": case.tools},
            "expected": case.rubric,
            "metadata": {"case": case.id, "agent": agent, "must_not_say": case.must_not_say, "prompt_hash": build.agents[agent].hash},
        }
        for agent, case in work
    ]
    prompts = {agent: build.agents[agent].prompt for agent, _ in work}
    tools = {agent: build.agents[agent].tools for agent, _ in work}
    # Braintrust runs scorers async, in a new event loop per Eval, so each run gets a fresh async client.
    autoevals.init(client=client(AsyncOpenAI), is_async=True)
    return Eval(
        project,
        experiment_name=label,
        data=data,
        task=lambda input: call_agent(llm, prompts[input["agent"]], tools[input["agent"]], input["tools"], input["turns"]),
        scores=[no_forbidden_phrases, rubric_judge()],
        trial_count=TRIALS,
        base_experiment_name=base_name,
        no_send_logs=local,
        metadata={"model": MODEL},
    )


def mean_scores(result) -> tuple[dict[tuple[str, str], float], set[tuple[str, str]]]:
    """(agent, case) → average of all scores across trials, plus the cases where a call or scorer failed."""
    totals: dict[tuple[str, str], list[float]] = {}
    failed: set[tuple[str, str]] = set()
    for r in result.results:
        key = (r.metadata["agent"], r.metadata["case"])
        scores = list(r.scores.values())
        if r.error or len(scores) < 2 or any(s is None for s in scores):
            failed.add(key)
        totals.setdefault(key, []).extend(s for s in scores if s is not None)
    return {k: sum(v) / len(v) for k, v in totals.items() if v}, failed


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--sops", default="sops", help="the OpenSOP root")
    ap.add_argument("--cases", default="evals/cases.yaml")
    ap.add_argument("--base", default="origin/main", help="git ref to compare against")
    ap.add_argument("--project", default="opensop-evals", help="Braintrust project name")
    ap.add_argument("--summary", help="write a Markdown summary here (for the PR comment)")
    ap.add_argument("--dry-run", action="store_true", help="show what would run, call no models")
    args = ap.parse_args()

    head = render_workspace(load_workspace(args.sops))
    base_files = files_at_ref(Path(args.sops), args.base)
    base = render_workspace(load_workspace_files(base_files)) if base_files else None
    plan = make_plan(snapshot(base) if base else {}, snapshot(head))

    cases = [Case(**c) for c in yaml.safe_load(Path(args.cases).read_text())]
    changed = [c.agent_id for c in plan.changes if c.status != "removed"]
    work = [
        (agent, case)
        for agent in changed
        for case in cases
        if case.applies_to(agent, {s.id for s in head.agents[agent].sops})
    ]
    # Cases for brand-new agents have no "before" to compare with; they run on the new prompt only.
    comparable = [(a, c) for a, c in work if base and a in base.agents]

    out = ["## OpenSOP evals", "", plan.text(diffs=False).strip(), ""]
    print("\n".join(out))
    if not work:
        out.append("No eval cases apply to the changed agents.")
        _write(args.summary, out)
        print("No eval cases apply to the changed agents.")
        return 0
    print(f"{len(work)} agent/case pairs, {TRIALS} trial(s) each, model {MODEL}:")
    for agent, case in work:
        print(f"  {agent:20} {case.id}")
    if args.dry_run:
        return 0

    llm = client()
    local = not os.environ.get("BRAINTRUST_API_KEY")
    sha = os.environ.get("GITHUB_SHA", "local")[:7]
    base_name = f"{args.base} ({sha} base)"
    before, failed_before = mean_scores(run(base_name, base, comparable, args.project, llm, local, None)) if comparable else ({}, set())
    after_result = run(f"{sha} head", head, work, args.project, llm, local, base_name if comparable else None)
    after, failed_after = mean_scores(after_result)
    failed = failed_before | failed_after

    rows, regressions = [], []
    for agent, case in work:
        new = after.get((agent, case.id))
        old = before.get((agent, case.id))
        regressed = old is not None and new is not None and new < old - REGRESSION_MARGIN
        if regressed:
            regressions.append((agent, case.id))
        errored = (agent, case.id) in failed
        if regressed:
            status = "❌ regressed" + (" (⚠️ some runs errored)" if errored else "")
        elif errored:
            status = "⚠️ eval error"
        else:
            status = "🆕" if old is None else "✅"
        rows.append(f"| {agent} | {case.id} | {_fmt(old)} | {_fmt(new)} | {status} |")

    out += ["| Agent | Case | Before | After | |", "|---|---|---|---|---|", *rows, ""]
    url = after_result.summary.experiment_url
    if url:
        out.append(f"Side-by-side results in Braintrust: {url}")
    if regressions:
        out += ["", f"**{len(regressions)} case(s) got worse with this change.** Merge is blocked until they pass."]
        out += _failing_transcripts(after_result, set(regressions))
    if failed:
        out += ["", f"**{len(failed)} case(s) couldn't be scored** (model or judge error). Rerun the check."]
    _write(args.summary, out)
    print("\n".join(out[out.index("| Agent | Case | Before | After | |"):]))
    return 1 if regressions or failed else 0


def _failing_transcripts(result, keys: set[tuple[str, str]]) -> list[str]:
    """One failing call per regressed case, so the evidence stays on the PR."""
    out, shown = [], set()
    for r in result.results:
        key = (r.metadata["agent"], r.metadata["case"])
        if key in keys and key not in shown and min((v for v in r.scores.values() if v is not None), default=1) < 1:
            shown.add(key)
            out += ["", f"<details><summary>{key[0]} / {key[1]}: a failing call</summary>", "", "```", str(r.output), "```", "</details>"]
    return out


def _fmt(score: float | None) -> str:
    return "–" if score is None else f"{score:.2f}"


def _write(path: str | None, lines: list[str]) -> None:
    if path:
        Path(path).write_text("\n".join(lines) + "\n")


if __name__ == "__main__":
    sys.exit(main())
