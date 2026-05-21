from enum import Enum


class ListShadowExceptionsStatus(str, Enum):
    ACTIVE = "active"
    EXPIRED = "expired"
    REVOKED = "revoked"

    def __str__(self) -> str:
        return str(self.value)
