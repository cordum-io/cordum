from enum import Enum


class EdgeAgentExecutionStatus(str, Enum):
    CANCELLED = "cancelled"
    DEGRADED = "degraded"
    FAILED = "failed"
    RUNNING = "running"
    SUCCEEDED = "succeeded"
    TIMEOUT = "timeout"
    WAITING_FOR_APPROVAL = "waiting_for_approval"

    def __str__(self) -> str:
        return str(self.value)
