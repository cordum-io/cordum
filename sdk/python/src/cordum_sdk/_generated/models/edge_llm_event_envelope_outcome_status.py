from enum import Enum

class EdgeLLMEventEnvelopeOutcomeStatus(str, Enum):
    DEGRADED = "degraded"
    FAILED = "failed"
    OK = "ok"
    VALUE_0 = ""

    def __str__(self) -> str:
        return str(self.value)
