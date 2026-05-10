import { http, HttpResponse } from "msw";
import { fixturePolicyDecisions } from "./fixtures/decisions";

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
  // Backend 5c — POST /api/v1/policy/rules. Default returns the request
  // body echoed with `version=v1` + a deterministic `audit` envelope so
  // create-flow tests render the same artifact every run. Tests that
  // need the duplicate-409 path override with `server.use(...)`.
  http.post("*/api/v1/policy/rules", async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    return HttpResponse.json(
      {
        ...body,
        version: "v1",
        status: "draft",
        audit: {
          created_at: "2026-05-10T12:00:00Z",
          updated_at: "2026-05-10T12:00:00Z",
          created_by: "alice",
          updated_by: "alice",
        },
      },
      {
        status: 201,
        headers: {
          Location: `/api/v1/policy/rules/${String(body.id ?? "")}`,
        },
      },
    );
  }),
  // Backend 5c — PUT /api/v1/policy/rules/:id. The handler reads
  // `If-Match` so the dashboard's reload-banner test can drive the
  // stale-409 branch by sending an outdated version. Default 200 path
  // bumps version v1 -> v2 to reflect the server-side increment.
  http.put("*/api/v1/policy/rules/:id", async ({ request, params }) => {
    const ifMatch = request.headers.get("If-Match");
    if (!ifMatch) {
      return HttpResponse.json(
        { error: "If-Match header required" },
        { status: 428 },
      );
    }
    if (ifMatch !== "v1") {
      return HttpResponse.json(
        {
          error: "stale_version",
          current_version: "v2",
          current_audit_hash: "sha256:msw-current",
        },
        { status: 409 },
      );
    }
    const body = (await request.json()) as Record<string, unknown>;
    return HttpResponse.json({
      ...body,
      id: String(params.id),
      version: "v2",
      audit: {
        created_at: "2026-05-10T12:00:00Z",
        updated_at: "2026-05-10T12:01:00Z",
        created_by: "alice",
        updated_by: "alice",
      },
    });
  }),
  // Backend 5c — POST /api/v1/policy/bundles/:id/rules. Default 200
  // appends `rule_id` to a stub bundle; tests override to drive the two
  // 404 paths (`bundle_not_found` vs `rule_not_found`) for the
  // dashboard's disambiguation copy.
  http.post(
    "*/api/v1/policy/bundles/:id/rules",
    async ({ request, params }) => {
      const body = (await request.json()) as { rule_id?: string };
      return HttpResponse.json({
        id: String(params.id),
        name: String(params.id),
        rule_ids: [body.rule_id ?? ""],
        scope_binding: { kind: "global" },
        versions: [],
      });
    },
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
  // /api/v1/policy/decisions (Backend 5b) — default returns the canonical
  // 12-row fixture covering all 7 DecisionTypes + both Source values so D8
  // (Decisions list) + D9 (replay) page tests render meaningful state
  // without per-test setup. Per-test overrides apply server.use(...).
  http.get("*/api/v1/policy/decisions", () =>
    HttpResponse.json({
      items: fixturePolicyDecisions,
      has_more: false,
      next_cursor: "",
    }),
  ),
];
