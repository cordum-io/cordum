from enum import Enum


class ListDelegationsForAgentStatus(str, Enum):
    ACTIVE = "active"
    ALL = "all"
    EXPIRED = "expired"
    REVOKED = "revoked"

    def __str__(self) -> str:
        return str(self.value)
