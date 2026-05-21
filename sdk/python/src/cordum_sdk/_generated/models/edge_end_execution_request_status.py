from enum import Enum


class EdgeEndExecutionRequestStatus(str, Enum):
    CANCELLED = "cancelled"
    FAILED = "failed"
    SUCCEEDED = "succeeded"
    TIMEOUT = "timeout"

    def __str__(self) -> str:
        return str(self.value)
