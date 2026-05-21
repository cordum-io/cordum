from enum import Enum


class EdgeEndSessionRequestStatus(str, Enum):
    ENDED = "ended"
    FAILED = "failed"

    def __str__(self) -> str:
        return str(self.value)
