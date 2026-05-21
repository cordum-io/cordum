from enum import Enum


class EdgeExecutionCreateRequestAdapter(str, Enum):
    CLAUDE_CODE_HOOK = "claude-code-hook"
    LLM_PROXY = "llm-proxy"
    MCP_GATEWAY = "mcp-gateway"
    RUNTIME_SIDECAR = "runtime-sidecar"
    SDK_RUNNER = "sdk-runner"

    def __str__(self) -> str:
        return str(self.value)
