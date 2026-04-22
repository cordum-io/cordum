from enum import Enum

class EvalEntryResultDriftDirection(str, Enum):
    ESCALATED = "escalated"
    RELAXED = "relaxed"
    UNCHANGED = "unchanged"

    def __str__(self) -> str:
        return str(self.value)
