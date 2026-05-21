from enum import Enum


class ShadowEvidencePointerRetentionClass(str, Enum):
    AUDIT = "audit"
    SHORT = "short"
    STANDARD = "standard"

    def __str__(self) -> str:
        return str(self.value)
