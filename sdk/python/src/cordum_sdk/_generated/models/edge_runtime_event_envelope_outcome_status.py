from enum import Enum


class EdgeRuntimeEventEnvelopeOutcomeStatus(str, Enum):
    DEGRADED = "degraded"
    FAILED = "failed"
    OK = "ok"
    VALUE_0 = ""

    def __str__(self) -> str:
        return str(self.value)
