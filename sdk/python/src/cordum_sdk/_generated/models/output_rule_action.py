from enum import Enum


class OutputRuleAction(str, Enum):
    BLOCK = "BLOCK"
    LOG = "LOG"
    REDACT = "REDACT"
    WARN = "WARN"

    def __str__(self) -> str:
        return str(self.value)
