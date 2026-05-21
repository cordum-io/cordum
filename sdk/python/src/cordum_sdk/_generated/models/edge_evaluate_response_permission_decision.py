from enum import Enum


class EdgeEvaluateResponsePermissionDecision(str, Enum):
    ALLOW = "allow"
    DENY = "deny"

    def __str__(self) -> str:
        return str(self.value)
