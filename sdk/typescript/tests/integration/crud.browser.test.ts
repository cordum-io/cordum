// @vitest-environment jsdom
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { setupServer } from "msw/node";
import { createIntegrationHandlers, createIntegrationState, type IntegrationState } from "./handlers.js";

describe("integration CRUD (jsdom)", () => {
  let state: IntegrationState;
  const server = setupServer();

  beforeAll(() => {
    server.listen({ onUnhandledRequest: "error" });
  });

  beforeEach(() => {
    state = createIntegrationState();
    server.resetHandlers(...createIntegrationHandlers(() => state));
  });

  afterEach(() => {
    server.resetHandlers();
  });

  afterAll(() => {
    server.close();
  });

  it("covers the same CRUD hops in a browser-like runtime", async () => {
    const createdJob = await fetchJson("/api/v1/jobs", {
      method: "POST",
      body: JSON.stringify({ topic: "job.default", prompt: "browser flow" }),
    });
    const listedJobs = await collectJobs();
    const loadedJob = await fetchJson(`/api/v1/jobs/${createdJob.id}`);
    const cancelledJob = await fetchJson(`/api/v1/jobs/${createdJob.id}/cancel`, { method: "POST" });

    const createdWorkflow = await fetchJson("/api/v1/workflows", {
      method: "POST",
      body: JSON.stringify({ name: "Browser workflow" }),
    });
    const listedWorkflows = await fetchJson("/api/v1/workflows");
    const loadedWorkflow = await fetchJson(`/api/v1/workflows/${createdWorkflow.id}`);
    const workflowRun = await fetchJson(`/api/v1/workflows/${createdWorkflow.id}/runs`, { method: "POST" });

    const bundle = await fetchJson("/api/v1/policy/bundles/bundle-1");
    const decision = await fetchJson("/api/v1/policy/evaluate", {
      method: "POST",
      body: JSON.stringify({ topic: "job.default" }),
    });
    const mcp = await fetchJson("/mcp/message", {
      method: "POST",
      body: JSON.stringify({ payload: "signed" }),
    });
    const agents = await fetchJson("/api/v1/workers");
    const schemas = await fetchJson("/api/v1/schemas");
    const dlq = await fetchJson("/api/v1/dlq");
    const redrive = await fetchJson("/api/v1/dlq/job-seed-1/retry", { method: "POST" });

    expect(createdJob.accepted).toBe(true);
    expect(listedJobs.some((job: { id: string }) => job.id === createdJob.id)).toBe(true);
    expect(loadedJob.id).toBe(createdJob.id);
    expect(cancelledJob.state).toBe("cancelled");

    expect(createdWorkflow.id).toMatch(/^wf-/);
    expect(listedWorkflows.some((workflow: { id: string }) => workflow.id === createdWorkflow.id)).toBe(true);
    expect(loadedWorkflow.id).toBe(createdWorkflow.id);
    expect(workflowRun.status).toBe("queued");

    expect(bundle.id).toBe("bundle-1");
    expect(decision.decision).toBe("allow");
    expect(mcp.verified).toBe(true);
    expect(agents).toEqual([{ id: "worker-1", status: "online" }]);
    expect(schemas).toEqual([{ id: "schema-1", version: "1.0.0" }]);
    expect(dlq).toEqual([{ id: "dlq-1", job_id: "job-seed-1", reason: "transient_failure" }]);
    expect(redrive).toEqual({ redriven: true, job_id: "job-seed-1" });
  });
});

async function fetchJson(path: string, init: RequestInit = {}) {
  const response = await fetch(`https://cordum.test${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": "test-api-key",
      "X-Tenant-ID": "tenant-a",
      ...(init.headers ?? {}),
    },
  });

  return response.json();
}

async function collectJobs() {
  const collected: Array<{ id: string }> = [];
  let cursor: string | null | undefined;

  do {
    const suffix = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
    const page = await fetchJson(`/api/v1/jobs${suffix}`);
    collected.push(...(page.items as Array<{ id: string }>));
    cursor = page.next_cursor as string | null | undefined;
  } while (cursor);

  return collected;
}
