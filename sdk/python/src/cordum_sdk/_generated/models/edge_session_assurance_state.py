from enum import Enum


class EdgeSessionAssuranceState(str, Enum):
    COMPATIBILITY = "compatibility"
    EXECUTOR_ENFORCED = "executor-enforced"
    HOOK_MEDIATED = "hook-mediated"
    STALE = "stale"
    UNSUPPORTED = "unsupported"

    def __str__(self) -> str:
        return str(self.value)
