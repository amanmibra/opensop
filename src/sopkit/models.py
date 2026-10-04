"""The sopkit spec: bases, SOPs, agents and workspace config.

These models are the source of truth for the format. `spec/*.schema.json` is
generated from them (see `sopkit.schema`) so other languages and editors can
validate files. Field descriptions here become the schema's documentation.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

Platform = Literal["livekit", "vapi", "elevenlabs"]
PLATFORMS: tuple[Platform, ...] = ("livekit", "vapi", "elevenlabs")


class _Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")


class Targeting(_Strict):
    agents: Literal["*"] | list[str] = Field(
        default_factory=list,
        description='Agents this block applies to without them opting in. "*" for every agent, or a list of agent '
        'ids (file names in agents/) or platform refs like "vapi:asst_9f3e". Empty means only agents that '
        "inherit it (bases) or none (SOPs).",
    )
    exclude: list[str] = Field(
        default_factory=list, description="Agent ids or platform refs to leave out, even when `agents` matches them."
    )


class Base(Targeting):
    """Prompt text that isn't a procedure: identity, brand voice, context, policy.

    Written as bases/<id>.md: optional YAML front matter, then the text.
    """

    id: str = Field(description="The file name without .md. Set automatically.")
    inherits: list[str] = Field(
        default_factory=list,
        description="Other bases rendered before this one, wherever this one is used. Parents come first.",
    )
    locked: bool = Field(
        False, description="If true, no agent can exclude this base. Use for text that must be in every prompt it targets."
    )
    position: Literal["top", "bottom"] = Field(
        "top", description='"top" renders before the agent\'s instructions; "bottom" renders after the SOPs (e.g. a call closing).'
    )
    text: str = Field(description="The markdown body below the front matter. Set automatically.")


class Step(_Strict):
    """A step, forbidden action or warning sign that names a tool."""

    text: str = Field(description="What the agent should do (or never do, or watch for).")
    tool: str | None = Field(
        None,
        description="Name of a tool the agent has (e.g. a LiveKit function tool). Rendered as an instruction to use it, "
        "and checked against the call's tool log.",
    )
    required: bool = Field(
        False, description="Steps only: the tool call must happen whenever this SOP applies, even if the goal was met another way."
    )


StepLike = str | Step


class SOP(Targeting):
    """A procedure the agent follows. Written as procedures/<id>.yaml."""

    id: str = Field(description="The file name without .yaml. Set automatically; if you write it, it must match.")
    name: str = Field(description='Heading shown in the prompt, e.g. "Allergen check".')
    locked: bool = Field(False, description="If true, no agent can exclude this SOP.")
    delivery: Literal["prompt", "auto", "tool"] = Field(
        "prompt",
        description='"prompt": the whole SOP is in the prompt. "auto": name, scope, forbidden actions and warning '
        'signs are in the prompt; the agent fetches the rest with the get_sop tool. "tool": only name and scope '
        "are in the prompt.",
    )
    description: str = Field(
        "",
        description="The goal: the outcome that means this SOP succeeded. QA uses it to go easier on skipped steps "
        "when the goal was still met.",
    )
    scope: str = Field("", description="When this SOP applies: the situation that should trigger it.")
    guidance: str = Field(
        "",
        description="Free text that isn't a step, forbidden action or warning sign: context, nuance, examples.",
    )
    procedureSteps: list[StepLike] = Field(
        default_factory=list, description="Ordered steps. Each is a string, or {text, tool, required}."
    )
    forbiddenActions: list[StepLike] = Field(
        default_factory=list, description="Things the agent must never do. Each is a string, or {text, tool}."
    )
    warningSigns: list[StepLike] = Field(
        default_factory=list,
        description="Situations that need special handling, usually an escalation. Each is a string, or {text, tool}.",
    )


class Agent(_Strict):
    """One voice agent, identified by its platform's own id. Written as agents/<id>.yaml."""

    id: str = Field(description="Alias used everywhere else in sopkit: the file name without .yaml. Set automatically.")
    livekit: str | None = Field(None, description="LiveKit agent_name. Set exactly one of livekit, vapi, elevenlabs.")
    vapi: str | None = Field(None, description="Vapi assistant id. Set exactly one of livekit, vapi, elevenlabs.")
    elevenlabs: str | None = Field(None, description="ElevenLabs agent_id. Set exactly one of livekit, vapi, elevenlabs.")
    inherits: list[str] = Field(default_factory=list, description="Base ids to include, in order. Their parents come first.")
    exclude: list[str] = Field(
        default_factory=list, description="Ids of bases or SOPs that target this agent but shouldn't apply. Locked ones can't be excluded."
    )
    variables: dict[str, str] = Field(
        default_factory=dict, description="Values for {{name}} placeholders. Override the defaults in sopkit.yaml."
    )
    instructions: str = Field("", description="Text only this agent gets, rendered after the top bases and before the SOPs.")

    @model_validator(mode="after")
    def _one_platform(self) -> Agent:
        refs = [p for p in PLATFORMS if getattr(self, p)]
        if len(refs) != 1:
            raise ValueError(f"agent '{self.id}' must set exactly one of {', '.join(PLATFORMS)}")
        return self

    @property
    def platform(self) -> Platform:
        return next(p for p in PLATFORMS if getattr(self, p))

    @property
    def platform_ref(self) -> str:
        return f"{self.platform}:{getattr(self, self.platform)}"


class WorkspaceConfig(_Strict):
    """sopkit.yaml at the root of a sopkit folder."""

    version: Literal[1] = Field(1, description="Format version. Always 1 for now.")
    variables: dict[str, str] = Field(default_factory=dict, description="Default values for {{name}} placeholders, for every agent.")
    sops_heading: str = Field("## Procedures", description="Heading rendered above the SOPs in each prompt.")
    sop_order: list[str] = Field(
        default_factory=list, description="SOP ids to render first, in this order. The rest follow alphabetically."
    )


class Workspace(_Strict):
    config: WorkspaceConfig
    bases: dict[str, Base]
    sops: dict[str, SOP]
    agents: dict[str, Agent]
