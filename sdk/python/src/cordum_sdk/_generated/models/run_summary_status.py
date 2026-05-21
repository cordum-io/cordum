from enum import Enum


class RunSummaryStatus(str, Enum):
    CANCELLED = "cancelled"
    DENIED = "denied"
    FAILED = "failed"
    PENDING = "pending"
    RUNNING = "running"
    SUCCEEDED = "succeeded"
    TIMED_OUT = "timed_out"
    WAITING = "waiting"

    def __str__(self) -> str:
        return str(self.value)
