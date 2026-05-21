from enum import Enum


class EdgeAgentExecutionMode(str, Enum):
    CI = "ci"
    ENTERPRISE_MANAGED = "enterprise-managed"
    LOCAL_DEV = "local-dev"
    PROD_RUNNER = "prod-runner"
    WORKFLOW = "workflow"

    def __str__(self) -> str:
        return str(self.value)
