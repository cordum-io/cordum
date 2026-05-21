from enum import Enum


class ListEdgeApprovalsStatus(str, Enum):
    APPROVED = "approved"
    EXPIRED = "expired"
    INVALIDATED = "invalidated"
    PENDING = "pending"
    REJECTED = "rejected"

    def __str__(self) -> str:
        return str(self.value)
