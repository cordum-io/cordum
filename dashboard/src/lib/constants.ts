export const API_BASE_URL =
  import.meta.env.VITE_API_URL || "/api/v1";

export const WS_BASE_URL =
  import.meta.env.VITE_WS_URL ||
  `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}/api/v1/stream`;

export const API_PATHS = {
  stream: "/api/v1/stream",
  jobs: "/jobs",
  workflows: "/workflows",
  workflowRuns: "/workflows/runs",
  workers: "/workers",
  pools: "/pools",
  approvals: "/approvals",
  policy: "/policy",
  policyBundles: "/policy/bundles",
  policyRules: "/policy/rules",
  policySnapshots: "/policy/snapshots",
  policyAudit: "/policy/audit",
  config: "/config",
  schemas: "/schemas",
  packs: "/packs",
  artifacts: "/artifacts",
  locks: "/locks",
  memory: "/memory",
  traces: "/traces",
  dlq: "/dlq",
  auth: "/auth",
  edge: {
    sessions: "/edge/sessions",
    executions: "/edge/executions",
    approvals: "/edge/approvals",
    evaluate: "/edge/evaluate",
    events: "/edge/events",
    eventsBatch: "/edge/events/batch",
  },
} as const;

export const APP_TITLE =
  import.meta.env.VITE_APP_TITLE || "Cordum Dashboard";
