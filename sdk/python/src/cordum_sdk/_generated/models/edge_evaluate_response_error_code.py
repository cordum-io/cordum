from enum import Enum


class EdgeEvaluateResponseErrorCode(str, Enum):
    SAFETY_UNAVAILABLE = "safety_unavailable"

    def __str__(self) -> str:
        return str(self.value)
