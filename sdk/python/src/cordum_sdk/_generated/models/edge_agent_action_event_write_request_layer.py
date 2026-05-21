from enum import Enum


class EdgeAgentActionEventWriteRequestLayer(str, Enum):
    HOOK = "hook"
    LLM = "llm"
    MCP = "mcp"
    RUNTIME = "runtime"
    SYSTEM = "system"
    WORKFLOW = "workflow"

    def __str__(self) -> str:
        return str(self.value)
