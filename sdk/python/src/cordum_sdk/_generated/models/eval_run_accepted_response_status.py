from enum import Enum


class EvalRunAcceptedResponseStatus(str, Enum):
    FAILED = "failed"
    PENDING = "pending"
    RUNNING = "running"

    def __str__(self) -> str:
        return str(self.value)
