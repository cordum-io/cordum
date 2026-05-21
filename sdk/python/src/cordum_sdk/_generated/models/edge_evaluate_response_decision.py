from enum import Enum


class EdgeEvaluateResponseDecision(str, Enum):
    ALLOW = "ALLOW"
    CONSTRAIN = "CONSTRAIN"
    DENY = "DENY"
    REQUIRE_APPROVAL = "REQUIRE_APPROVAL"
    THROTTLE = "THROTTLE"

    def __str__(self) -> str:
        return str(self.value)
