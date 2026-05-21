from enum import Enum


class EdgeSessionPrincipalType(str, Enum):
    HUMAN = "human"
    SERVICE = "service"
    UNKNOWN = "unknown"

    def __str__(self) -> str:
        return str(self.value)
