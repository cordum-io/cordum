from enum import Enum


class ShadowEvidencePointerRedactionLevel(str, Enum):
    STANDARD = "standard"
    STRICT = "strict"

    def __str__(self) -> str:
        return str(self.value)
