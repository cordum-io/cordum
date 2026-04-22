from enum import Enum

class GetMcpUsageGroupBy(str, Enum):
    METHOD = "method"
    SUBJECT = "subject"
    TOOL = "tool"

    def __str__(self) -> str:
        return str(self.value)
