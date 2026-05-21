from enum import Enum


class ShadowComparisonEntryDiff(str, Enum):
    APPROVAL_DIFFER = "approval_differ"
    ESCALATED = "escalated"
    RELAXED = "relaxed"
    UNCHANGED = "unchanged"

    def __str__(self) -> str:
        return str(self.value)
