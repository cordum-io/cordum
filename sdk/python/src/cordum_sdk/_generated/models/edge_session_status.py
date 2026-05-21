from enum import Enum


class EdgeSessionStatus(str, Enum):
    DEGRADED = "degraded"
    ENDED = "ended"
    FAILED = "failed"
    RUNNING = "running"
    STARTING = "starting"
    WAITING_FOR_APPROVAL = "waiting_for_approval"

    def __str__(self) -> str:
        return str(self.value)
