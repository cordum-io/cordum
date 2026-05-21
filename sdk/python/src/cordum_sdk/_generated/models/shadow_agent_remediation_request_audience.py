from enum import Enum


class ShadowAgentRemediationRequestAudience(str, Enum):
    BOTH = "both"
    DEV = "dev"
    ENTERPRISE = "enterprise"

    def __str__(self) -> str:
        return str(self.value)
