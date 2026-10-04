from __future__ import annotations

from dataclasses import dataclass
from typing import Literal


@dataclass(frozen=True)
class Issue:
    code: str
    message: str
    path: str = ""
    severity: Literal["error", "warning"] = "error"

    def __str__(self) -> str:
        where = f"{self.path}: " if self.path else ""
        return f"{where}{self.severity} [{self.code}] {self.message}"


class OpenSOPError(Exception):
    def __init__(self, issues: list[Issue]):
        self.issues = issues
        super().__init__("\n".join(str(i) for i in issues))
