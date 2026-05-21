from enum import Enum


class ShadowExceptionScopeSourceType(str, Enum):
    CI = "ci"
    KUBERNETES = "kubernetes"
    LOCAL = "local"
    NETWORK = "network"

    def __str__(self) -> str:
        return str(self.value)
