import { HttpResponse, http, type HttpHandler } from "msw";

export interface IntegrationState {
  readonly validApiKey: string;
  readonly validBearerToken: string;
  jobSequence: number;
  workflowSequence: number;
  runSequence: number;
  loginCount: number;
  currentSessionToken?: string;
  firstSessionToken?: string;
  rejectSessionTokensOnce: Set<string>;
  jobs: Map<string, Record<string, unknown>>;
  workflows: Map<string, Record<string, unknown>>;
  bundles: Map<string, Record<string, unknown>>;
  agents: Array<Record<string, unknown>>;
  schemas: Array<Record<string, unknown>>;
  dlq: Array<Record<string, unknown>>;
}

export function createIntegrationState(): IntegrationState {
  const jobs = new Map<string, Record<string, unknown>>();
  jobs.set("job-seed-1", { id: "job-seed-1", topic: "job.default", state: "queued" });

  const workflows = new Map<string, Record<string, unknown>>();
  workflows.set("wf-seed-1", { id: "wf-seed-1", name: "Seed workflow" });

  const bundles = new Map<string, Record<string, unknown>>();
  bundles.set("bundle-1", { id: "bundle-1", enabled: true, rule_count: 1 });

  return {
    validApiKey: "test-api-key",
    validBearerToken: "test-bearer-token",
    jobSequence: 2,
    workflowSequence: 2,
    runSequence: 1,
    loginCount: 0,
    rejectSessionTokensOnce: new Set<string>(),
    jobs,
    workflows,
    bundles,
    agents: [{ id: "worker-1", status: "online" }],
    schemas: [{ id: "schema-1", version: "1.0.0" }],
    dlq: [{ id: "dlq-1", job_id: "job-seed-1", reason: "transient_failure" }],
  };
}

export function createIntegrationHandlers(getState: () => IntegrationState): HttpHandler[] {
  return [
    http.post("https://cordum.test/api/v1/auth/login", async ({ request }) => {
      const state = getState();
      state.loginCount += 1;
      const body = (await request.json()) as { username?: string; password?: string };
      if (!body.username || !body.password) {
        return json(400, { message: "missing credentials" });
      }

      const token = createToken(`session-${state.loginCount}`);
      if (!state.firstSessionToken) {
        state.firstSessionToken = token;
        state.rejectSessionTokensOnce.add(token);
      }
      state.currentSessionToken = token;

      return json(200, {
        token,
        expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
      });
    }),

    http.get("https://cordum.test/api/v1/jobs", ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const state = getState();
      const cursor = new URL(request.url).searchParams.get("cursor");
      const allJobs = Array.from(state.jobs.values());
      if (!cursor && allJobs.length > 1) {
        return json(200, { items: allJobs.slice(0, 1), next_cursor: "page-2" });
      }
      if (cursor === "page-2") {
        return json(200, { items: allJobs.slice(1), next_cursor: null });
      }
      return json(200, { items: allJobs, next_cursor: null });
    }),

    http.post("https://cordum.test/api/v1/jobs", async ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const state = getState();
      const body = (await request.json()) as Record<string, unknown>;
      const id = `job-${state.jobSequence++}`;
      const job = { id, state: "queued", ...body };
      state.jobs.set(id, job);
      return json(200, { id, accepted: true, body: job });
    }),

    http.get("https://cordum.test/api/v1/jobs/:id", ({ params, request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const state = getState();
      const job = state.jobs.get(String(params.id));
      return job ? json(200, job) : json(404, { message: "missing job" }, "req-job-missing");
    }),

    http.post("https://cordum.test/api/v1/jobs/:id/cancel", ({ params, request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const state = getState();
      const job = state.jobs.get(String(params.id));
      if (!job) {
        return json(404, { message: "missing job" });
      }
      const updated = { ...job, state: "cancelled" };
      state.jobs.set(String(params.id), updated);
      return json(200, updated);
    }),

    http.get("https://cordum.test/api/v1/workflows", ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }
      return json(200, Array.from(getState().workflows.values()));
    }),

    http.post("https://cordum.test/api/v1/workflows", async ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const state = getState();
      const body = (await request.json()) as Record<string, unknown>;
      const id = `wf-${state.workflowSequence++}`;
      const workflow = { id, ...body };
      state.workflows.set(id, workflow);
      return json(200, workflow);
    }),

    http.get("https://cordum.test/api/v1/workflows/:id", ({ params, request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const workflow = getState().workflows.get(String(params.id));
      return workflow ? json(200, workflow) : json(404, { message: "missing workflow" });
    }),

    http.post("https://cordum.test/api/v1/workflows/:id/runs", ({ params, request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const state = getState();
      return json(200, {
        id: `run-${state.runSequence++}`,
        workflow_id: params.id,
        status: "queued",
      });
    }),

    http.get("https://cordum.test/api/v1/policy/bundles/:id", ({ params, request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const bundle = getState().bundles.get(String(params.id));
      return bundle ? json(200, bundle) : json(404, { message: "missing bundle" });
    }),

    http.post("https://cordum.test/api/v1/policy/evaluate", ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }
      return json(200, { decision: "allow", request_id: request.headers.get("x-request-id") ?? null });
    }),

    http.post("https://cordum.test/mcp/message", ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }
      return json(200, { verified: true, signature: request.headers.get("X-Signature") ?? "mocked" });
    }),

    http.get("https://cordum.test/api/v1/workers", ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }
      return json(200, getState().agents);
    }),

    http.get("https://cordum.test/api/v1/schemas", ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }
      return json(200, getState().schemas);
    }),

    http.get("https://cordum.test/api/v1/dlq", ({ request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }
      return json(200, getState().dlq);
    }),

    http.post("https://cordum.test/api/v1/dlq/:job_id/retry", ({ params, request }) => {
      const unauthorized = requireAuth(request, getState());
      if (unauthorized) {
        return unauthorized;
      }

      const state = getState();
      state.dlq = state.dlq.filter((entry) => entry.job_id !== params.job_id);
      return json(200, { redriven: true, job_id: params.job_id });
    }),
  ];
}

function requireAuth(request: Request, state: IntegrationState): Response | undefined {
  const apiKey = request.headers.get("X-API-Key");
  if (apiKey === state.validApiKey) {
    return undefined;
  }

  const authorization = request.headers.get("Authorization");
  if (!authorization?.startsWith("Bearer ")) {
    return json(401, { message: "unauthorized" }, "req-auth-missing");
  }

  const token = authorization.slice("Bearer ".length);
  if (token === state.validBearerToken) {
    return undefined;
  }
  if (token === state.currentSessionToken) {
    if (state.rejectSessionTokensOnce.has(token)) {
      state.rejectSessionTokensOnce.delete(token);
      return json(401, { message: "session expired" }, "req-session-refresh");
    }
    return undefined;
  }

  return json(401, { message: "unauthorized" }, "req-auth-invalid");
}

function createToken(subject: string): string {
  const header = Buffer.from(JSON.stringify({ alg: "none", typ: "JWT" }), "utf8").toString("base64url");
  const payload = Buffer.from(
    JSON.stringify({ sub: subject, exp: Math.floor(Date.now() / 1000) + 3600 }),
    "utf8",
  ).toString("base64url");
  return `${header}.${payload}.signature`;
}

function json(status: number, body: unknown, requestId = `req-${status}`): Response {
  return HttpResponse.json(body as any, {
    status,
    headers: {
      "X-Request-Id": requestId,
    },
  });
}
