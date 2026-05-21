from enum import Enum


class EdgeApprovalDecision(str, Enum):
    APPROVE = "approve"
    EXPIRE = "expire"
    INVALIDATE = "invalidate"
    REJECT = "reject"
    VALUE_0 = ""

    def __str__(self) -> str:
        return str(self.value)
