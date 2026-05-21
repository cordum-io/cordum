from enum import Enum


class EdgeAgentActionEventWriteRequestDecision(str, Enum):
    ALLOW = "ALLOW"
    CONSTRAIN = "CONSTRAIN"
    DENY = "DENY"
    RECORDED = "RECORDED"
    REQUIRE_APPROVAL = "REQUIRE_APPROVAL"
    THROTTLE = "THROTTLE"

    def __str__(self) -> str:
        return str(self.value)
