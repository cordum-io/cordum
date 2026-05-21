from enum import Enum


class ListShadowAgentFindingsStatus(str, Enum):
    DETECTED = "detected"
    RESOLVED = "resolved"
    SUPPRESSED = "suppressed"

    def __str__(self) -> str:
        return str(self.value)
