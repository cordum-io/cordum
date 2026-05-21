from enum import Enum


class EvalEntryResultStatus(str, Enum):
    ERROR = "error"
    FAIL = "fail"
    PASS = "pass"
    REGRESSION = "regression"

    def __str__(self) -> str:
        return str(self.value)
