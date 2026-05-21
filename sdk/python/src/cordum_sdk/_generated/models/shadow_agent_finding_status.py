from enum import Enum


class ShadowAgentFindingStatus(str, Enum):
    DETECTED = "detected"
    MANAGED_SKIP = "managed_skip"
    RESOLVED = "resolved"
    SUPPRESSED = "suppressed"

    def __str__(self) -> str:
        return str(self.value)
