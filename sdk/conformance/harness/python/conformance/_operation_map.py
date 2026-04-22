"""operationId -> (HTTP method, URL path) lookup.

Byte-identical to the Go harness's `operation_map.go` — divergence
between the two is a blocker enforced by the step-9 parity runner.
"""

OPERATION_MAP: dict[str, tuple[str, str]] = {
    # Agents
    "createAgent": ("POST", "/api/v1/agents"),
    "listAgents":  ("GET",  "/api/v1/agents"),
    "getAgent":    ("GET",  "/api/v1/agents/{id}"),
    "updateAgent": ("PUT",  "/api/v1/agents/{id}"),
    "deleteAgent": ("DELETE", "/api/v1/agents/{id}"),

    # Jobs
    "submitJob": ("POST", "/api/v1/jobs"),
    "listJobs":  ("GET",  "/api/v1/jobs"),
    "getJob":    ("GET",  "/api/v1/jobs/{id}"),
    "cancelJob": ("POST", "/api/v1/jobs/{id}/cancel"),

    # Workflows
    "createWorkflow":   ("POST",   "/api/v1/workflows"),
    "getWorkflow":      ("GET",    "/api/v1/workflows/{id}"),
    "listWorkflows":    ("GET",    "/api/v1/workflows"),
    "deleteWorkflow":   ("DELETE", "/api/v1/workflows/{id}"),
    "startWorkflowRun": ("POST",   "/api/v1/workflows/{id}/runs"),

    # Policies
    "listPolicyBundles": ("GET",  "/api/v1/policy/bundles"),
    "getPolicyBundle":   ("GET",  "/api/v1/policy/bundles/{id}"),
    "publishPolicy":     ("POST", "/api/v1/policy/publish"),
    "rollbackPolicy":    ("POST", "/api/v1/policy/rollback"),
    "getPolicyAudit":    ("GET",  "/api/v1/policy/audit"),

    # Auth
    "login":         ("POST", "/api/v1/auth/login"),
    "getSession":    ("GET",  "/api/v1/auth/session"),
    "getAuthConfig": ("GET",  "/api/v1/auth/config"),

    # Streaming
    "streamJob": ("GET", "/api/v1/stream"),
}
