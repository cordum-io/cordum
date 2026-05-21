from enum import Enum


class EdgeSessionCreateRequestPolicyMode(str, Enum):
    ENFORCE = "enforce"
    ENTERPRISE_STRICT = "enterprise-strict"
    OBSERVE = "observe"

    def __str__(self) -> str:
        return str(self.value)
