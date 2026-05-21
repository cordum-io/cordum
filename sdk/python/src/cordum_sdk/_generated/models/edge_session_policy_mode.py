from enum import Enum


class EdgeSessionPolicyMode(str, Enum):
    ENFORCE = "enforce"
    ENTERPRISE_STRICT = "enterprise-strict"
    OBSERVE = "observe"

    def __str__(self) -> str:
        return str(self.value)
