import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { setupServer } from "msw/node";
import { createClient as createRawClient } from "../../src/_generated/fetch.js";
import { CordumClient } from "../../src/client.js";
import { createIntegrationHandlers, createIntegrationState, type IntegrationState } from "./handlers.js";

export function runCrudSuite(label: string): void {
  describe(`integration CRUD (${label})`, () => {
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

    it("covers jobs, workflows, policies, MCP, agents, schemas, and DLQ flows", async () => {
      const client = new CordumClient({
        baseUrl: "https://cordum.test",
        auth: state.validApiKey,
        tenantId: "tenant-a",
      });

      const rawClient: any = createRawClient({
        baseUrl: "https://cordum.test",
        fetch: withApiKeyFetch(state.validApiKey),
      });

      const createdJob = await client.jobs.create({ topic: "job.default", prompt: "run integration" } as never);
      const listedJobs: Array<{ id?: string }> = [];
      for await (const job of client.jobs.paginate()) {
        listedJobs.push(job as { id?: string });
      }
      const loadedJob = await client.jobs.get((createdJob as { id: string }).id);
      const cancelledJob = await client.jobs.cancel((createdJob as { id: string }).id);

      const createdWorkflow = await client.workflows.create({ name: "Integration workflow" } as never);
      const listedWorkflows = await client.workflows.list();
      const loadedWorkflow = await client.workflows.get((createdWorkflow as { id: string }).id);

      const workflowRun = await rawClient.POST("/api/v1/workflows/{id}/runs", {
        params: { path: { id: (createdWorkflow as { id: string }).id } },
      });

      const bundle = await client.policies.getBundle("bundle-1");
      const decision = await client.policies.evaluate({ topic: "job.default" } as never);
      const mcp = await client.mcp.message({ payload: "signed" } as never);
      const agents = await client.agents.list();
      const schemas = await rawClient.GET("/api/v1/schemas");
      const dlq = await rawClient.GET("/api/v1/dlq");
      const redrive = await rawClient.POST("/api/v1/dlq/{job_id}/retry", {
        params: { path: { job_id: "job-seed-1" } },
      });

      expect((createdJob as { accepted?: boolean }).accepted).toBe(true);
      expect(listedJobs.some((job) => job.id === (createdJob as { id: string }).id)).toBe(true);
      expect((loadedJob as { id: string }).id).toBe((createdJob as { id: string }).id);
      expect((cancelledJob as { state?: string }).state).toBe("cancelled");

      expect((createdWorkflow as { id?: string }).id).toMatch(/^wf-/);
      expect((listedWorkflows as Array<{ id: string }>).some((workflow) => workflow.id === (createdWorkflow as { id: string }).id)).toBe(true);
      expect((loadedWorkflow as { id: string }).id).toBe((createdWorkflow as { id: string }).id);
      expect(workflowRun.data).toMatchObject({ workflow_id: (createdWorkflow as { id: string }).id, status: "queued" });

      expect(bundle).toMatchObject({ id: "bundle-1" });
      expect(decision).toMatchObject({ decision: "allow" });
      expect(mcp).toMatchObject({ verified: true });
      expect(agents).toEqual([{ id: "worker-1", status: "online" }]);
      expect(schemas.data).toEqual([{ id: "schema-1", version: "1.0.0" }]);
      expect(dlq.data).toEqual([{ id: "dlq-1", job_id: "job-seed-1", reason: "transient_failure" }]);
      expect(redrive.data).toEqual({ redriven: true, job_id: "job-seed-1" });
    });
  });
}

function withApiKeyFetch(apiKey: string): typeof fetch {
  return async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    const headers = new Headers(request.headers);
    headers.set("X-API-Key", apiKey);
    headers.set("X-Tenant-ID", "tenant-a");
    return fetch(new Request(request, { headers }));
  };
}
