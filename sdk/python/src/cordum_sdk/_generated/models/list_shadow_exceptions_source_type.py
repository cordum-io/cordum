from enum import Enum


class ListShadowExceptionsSourceType(str, Enum):
    CI = "ci"
    KUBERNETES = "kubernetes"
    LOCAL = "local"
    NETWORK = "network"

    def __str__(self) -> str:
        return str(self.value)
