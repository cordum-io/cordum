from enum import Enum


class EdgeArtifactPointerRetentionClass(str, Enum):
    AUDIT = "audit"
    SHORT = "short"
    STANDARD = "standard"

    def __str__(self) -> str:
        return str(self.value)
