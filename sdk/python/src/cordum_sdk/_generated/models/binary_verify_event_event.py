from enum import Enum


class BinaryVerifyEventEvent(str, Enum):
    BINARY_VERIFY_FAIL = "binary-verify-fail"
    BINARY_VERIFY_OK = "binary-verify-ok"

    def __str__(self) -> str:
        return str(self.value)
