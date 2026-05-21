from enum import Enum


class EdgeEvaluateResponseWaitStrategy(str, Enum):
    BACKOFF = "backoff"
    MANUAL_APPROVAL = "manual_approval"
    RETRY = "retry"

    def __str__(self) -> str:
        return str(self.value)
