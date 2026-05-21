from enum import Enum


class JsonRpcRequestJsonrpc(str, Enum):
    VALUE_0 = "2.0"

    def __str__(self) -> str:
        return str(self.value)
