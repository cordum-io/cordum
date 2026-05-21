from enum import Enum


class LockMode(str, Enum):
    EXCLUSIVE = "EXCLUSIVE"
    SHARED = "SHARED"

    def __str__(self) -> str:
        return str(self.value)
