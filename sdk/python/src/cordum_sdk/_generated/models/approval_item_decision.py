from enum import Enum


class ApprovalItemDecision(str, Enum):
    APPROVED = "APPROVED"
    PENDING = "PENDING"
    REJECTED = "REJECTED"

    def __str__(self) -> str:
        return str(self.value)
