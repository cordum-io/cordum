from enum import Enum


class AdoptRetentionAnchorResultPreviousAnchorState(str, Enum):
    HMAC_MISMATCH = "hmac_mismatch"
    MALFORMED = "malformed"
    NONE = "none"
    UNLINKED = "unlinked"

    def __str__(self) -> str:
        return str(self.value)
