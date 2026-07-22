from enum import Enum

class EdgeLLMEventDecisionDecision(str, Enum):
    RECORD = "record"
    REDACT = "redact"

    def __str__(self) -> str:
        return str(self.value)
