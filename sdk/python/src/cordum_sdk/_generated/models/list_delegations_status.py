from enum import Enum


class ListDelegationsStatus(str, Enum):
    ACTIVE = "active"
    ALL = "all"
    EXPIRED = "expired"
    REVOKED = "revoked"

    def __str__(self) -> str:
        return str(self.value)
