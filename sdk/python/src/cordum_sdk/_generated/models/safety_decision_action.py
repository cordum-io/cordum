from enum import Enum


class SafetyDecisionAction(str, Enum):
    ALLOW = "ALLOW"
    ALLOW_WITH_CONSTRAINTS = "ALLOW_WITH_CONSTRAINTS"
    DENY = "DENY"
    REQUIRE_APPROVAL = "REQUIRE_APPROVAL"
    THROTTLE = "THROTTLE"
    UNAVAILABLE = "UNAVAILABLE"

    def __str__(self) -> str:
        return str(self.value)
