from enum import Enum


class ShadowRemediationPlanSeverity(str, Enum):
    HIGH = "high"
    INFO = "info"
    LOW = "low"
    MEDIUM = "medium"

    def __str__(self) -> str:
        return str(self.value)
