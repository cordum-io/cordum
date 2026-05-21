from enum import Enum


class GetPolicyShadowResultsComparisonsDiff(str, Enum):
    ALL = "all"
    APPROVAL_DIFFER = "approval_differ"
    ESCALATED = "escalated"
    RELAXED = "relaxed"
    UNCHANGED = "unchanged"

    def __str__(self) -> str:
        return str(self.value)
