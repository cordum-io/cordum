from enum import Enum


class McpStatusTransport(str, Enum):
    SSE = "sse"
    STDIO = "stdio"

    def __str__(self) -> str:
        return str(self.value)
