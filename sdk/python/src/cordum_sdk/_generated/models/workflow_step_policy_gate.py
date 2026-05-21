from enum import Enum


class WorkflowStepPolicyGate(str, Enum):
    ALLOW = "allow"
    DENY = "deny"
    REQUIRE_APPROVAL = "require_approval"

    def __str__(self) -> str:
        return str(self.value)
