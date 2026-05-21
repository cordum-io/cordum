from enum import Enum


class EdgeSessionCreateRequestPrincipalType(str, Enum):
    HUMAN = "human"
    SERVICE = "service"
    UNKNOWN = "unknown"

    def __str__(self) -> str:
        return str(self.value)
