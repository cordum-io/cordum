from enum import Enum


class EdgeAgentActionEventStatus(str, Enum):
    BLOCKED = "blocked"
    DEGRADED = "degraded"
    FAILED = "failed"
    OK = "ok"

    def __str__(self) -> str:
        return str(self.value)
