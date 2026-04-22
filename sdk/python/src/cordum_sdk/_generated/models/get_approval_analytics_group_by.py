from enum import Enum

class GetApprovalAnalyticsGroupBy(str, Enum):
    AGENT = "agent"
    OVERALL = "overall"
    RULE = "rule"
    TOPIC = "topic"

    def __str__(self) -> str:
        return str(self.value)
