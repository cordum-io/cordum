from enum import Enum


class SubmitJobRequestPriority(str, Enum):
    BATCH = "batch"
    CRITICAL = "critical"
    INTERACTIVE = "interactive"

    def __str__(self) -> str:
        return str(self.value)
