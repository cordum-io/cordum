from enum import Enum


class ShadowRemediationActionKind(str, Enum):
    ATTACH_MCP_GATEWAY = "attach_mcp_gateway"
    DEPLOY_MANAGED_SETTINGS = "deploy_managed_settings"
    DISABLE_UNMANAGED_CONFIG = "disable_unmanaged_config"
    INVESTIGATE_PROCESS = "investigate_process"
    MANUAL_REVIEW = "manual_review"
    ROUTE_THROUGH_LLM_PROXY = "route_through_llm_proxy"
    RUN_EDGE_DOCTOR = "run_edge_doctor"
    USE_CORDUMCTL_EDGE_CLAUDE = "use_cordumctl_edge_claude"

    def __str__(self) -> str:
        return str(self.value)
