import { http, HttpResponse } from "msw";

export const baseHandlers = [
  // Dashboard 2 — Rules surface list (unified Backend-1 Rule shape).
  // Default empty so PoliciesPage renders the empty-state CTA without
  // per-test setup; tests override with populated/paginated responses.
  // The drawer (Dashboard 3A) sources existing rules from this list cache
  // / endpoint — no `/policy/rules/:id` detail route exists in the current
  // dashboard/core contract (cordum-api.yaml:2609 + gateway.go:1415).
  http.get("*/api/v1/policy/rules", () =>
    HttpResponse.json({ items: [], total: 0 }),
  ),
  // Dashboard 5 — Bundle Studio list (unified Backend-1.5 Bundle shape).
  // Default empty so BundlesPage renders the empty-state CTA without
  // per-test setup; tests override with `server.use(...)` as needed.
  http.get("*/api/v1/policy/bundles", () =>
    HttpResponse.json({ items: [], total: 0 }),
  ),
  // Bundle detail / versions / deployments — minimal-but-renderable
  // defaults for Dashboard 5 step-4 BundleDetailPage tests.
  http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
    HttpResponse.json({
      id: String(params.id),
      name: String(params.id),
      rule_ids: [],
      scope_binding: { kind: "global" },
      versions: [],
    }),
  ),
  http.get("*/api/v1/policy/bundles/:id/versions", () =>
    HttpResponse.json({ items: [] }),
  ),
  http.get("*/api/v1/policy/bundles/:id/versions/:version", ({ params }) =>
    HttpResponse.json({
      version: String(params.version),
      rule_snapshot: [],
      deployed_at: "2026-05-09T10:00:00Z",
    }),
  ),
  http.get("*/api/v1/policy/bundles/:id/deployments", () =>
    HttpResponse.json({ items: [] }),
  ),
  // Promote / Rollback default OK for Dashboard 5 step-7 Deployments tab
  // tests; per-test overrides via `server.use(...)` inject 4xx/5xx paths.
  http.post("*/api/v1/policy/bundles/:id/deploy", () => HttpResponse.json({})),
  http.post("*/api/v1/policy/bundles/:id/rollback", () => HttpResponse.json({})),
  http.get("*/api/v1/approvals", () =>
    HttpResponse.json({ items: [], next_cursor: null }),
  ),
  http.get("*/api/v1/mcp/approvals", () =>
    HttpResponse.json({ items: [] }),
  ),
  http.get("*/api/v1/mcp/approvals/:id", ({ params }) =>
    HttpResponse.json({
      id: String(params.id),
      tenant: "default",
      agent_id: "agent-test",
      tool_name: "test.tool",
      args_hash: "hash-test",
      status: "pending",
      created_at: 0,
      expires_at: 0,
    }),
  ),
  http.get("*/api/v1/copilot/sessions/:sessionId", ({ params }) =>
    HttpResponse.json({
      session: {
        id: String(params.sessionId),
        title: "Test Copilot Session",
        userId: "test-user",
        createdAt: "2026-04-26T07:00:00Z",
        updatedAt: "2026-04-26T07:00:00Z",
        messages: [],
        metadata: {},
      },
      jobs: [],
      decisions: [],
      truncated: false,
    }),
  ),
  // Agent identity defaults — empty list keeps render paths from crashing
  // when a page consumes useAgentIdentities without per-test override.
  http.get("*/api/v1/agents", () =>
    HttpResponse.json({ items: [], cursor: null }),
  ),
  http.get("*/api/v1/agents/:id", () =>
    HttpResponse.json({}, { status: 404 }),
  ),
  http.get("*/api/v1/agents/:id/stats", ({ params }) =>
    HttpResponse.json({
      agent_id: String(params.id),
      total_jobs_7d: 0,
      denied_7d: 0,
      last_active: 0,
    }),
  ),
  // License default — enterprise plan with the agentIdentity entitlement
  // so pages that gate on it render the unlocked surface by default.
  // Per-test overrides via server.use() can dial in community/team variants.
  http.get("*/api/v1/license", () =>
    HttpResponse.json({
      plan: "enterprise",
      entitlements: {
        sso: true,
        saml: true,
        scim: true,
        rbac: true,
        audit: true,
        audit_export: true,
        siem_export: true,
        legal_hold: true,
        velocity_rules: true,
        agent_identity: true,
      },
      rights: null,
      license: null,
    }),
  ),
  // Workers default — empty list. Pages consuming useWorkers (AgentsPage)
  // render the empty grid; tests that need worker fixtures override.
  http.get("*/api/v1/workers", () => HttpResponse.json({ items: [] })),
  // Policy audit default — empty page so AuditLogPage renders EmptyState
  // without per-test handler. Per-test overrides via server.use() inject
  // fixtures, error responses, or 1000-row virtualization stress data.
  http.get("*/api/v1/policy/audit", () =>
    HttpResponse.json({ items: [], total: 0, has_more: false, offset: 0 }),
  ),
];
