from enum import Enum


class PolicyCheckResponseDecision(str, Enum):
    ALLOW = "ALLOW"
    DENY = "DENY"
    QUARANTINE = "QUARANTINE"
    REQUIRE_APPROVAL = "REQUIRE_APPROVAL"

    def __str__(self) -> str:
        return str(self.value)
