from enum import Enum


class GetConfigScope(str, Enum):
    ORG = "org"
    STEP = "step"
    SYSTEM = "system"
    TEAM = "team"
    WORKFLOW = "workflow"

    def __str__(self) -> str:
        return str(self.value)
