from enum import Enum


class CreateShadowAgentFindingRequestRetentionClass(str, Enum):
    SHADOW_DEFAULT = "shadow_default"
    SHADOW_LONG = "shadow_long"
    SHADOW_SHORT = "shadow_short"

    def __str__(self) -> str:
        return str(self.value)
