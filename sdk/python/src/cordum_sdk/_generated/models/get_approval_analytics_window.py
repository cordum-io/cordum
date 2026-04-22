from enum import Enum

class GetApprovalAnalyticsWindow(str, Enum):
    VALUE_0 = "24h"
    VALUE_1 = "7d"
    VALUE_2 = "30d"

    def __str__(self) -> str:
        return str(self.value)
