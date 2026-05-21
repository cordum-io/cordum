from enum import Enum


class AuditVerifyResultStatus(str, Enum):
    COMPROMISED = "compromised"
    OK = "ok"
    PARTIAL = "partial"

    def __str__(self) -> str:
        return str(self.value)
