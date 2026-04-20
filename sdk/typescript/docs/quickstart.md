# Workflow-run quickstart

This guide shows a longer end-to-end flow for `@cordum/sdk`:

1. create a `CordumClient` with API-key auth
2. create a typed raw client for endpoints not yet wrapped by a convenience namespace
3. submit a workflow run
4. listen for the first workflow event over SSE
5. fetch the final workflow-run state

## Full example

```ts
import { CordumClient, createClient, type components, type StreamEvent } from "@cordum/sdk";

const baseUrl = "http://localhost:8080";
const apiKey = process.env.CORDUM_API_KEY ?? "dev-api-key";
const tenantId = process.env.CORDUM_TENANT_ID ?? "tenant-demo";
const workflowId = "wf-docs-demo";

const client = new CordumClient({ baseUrl, auth: apiKey, tenantId });
const raw = createClient({
  baseUrl,
  fetch: async (input, init) => {
    const request = new Request(input, init);
    const headers = new Headers(request.headers);
    headers.set("X-API-Key", apiKey);
    headers.set("X-Tenant-ID", tenantId);
    return fetch(new Request(request, { headers }));
  },
});

const definition: components["schemas"]["WorkflowDefinition"] = {
  id: workflowId,
  name: "Incident triage",
  steps: {
    classify: {
      id: "classify",
      type: "job",
      config: {
        topic: "incident.triage",
      },
    },
  },
};

const createdWorkflow = await raw.POST("/api/v1/workflows", {
  body: definition,
});
if (createdWorkflow.error || !createdWorkflow.data?.id) {
  throw new Error("Workflow creation failed");
}

const startedRun = await raw.POST("/api/v1/workflows/{id}/runs", {
  params: { path: { id: createdWorkflow.data.id } },
  body: {
    source: "docs/quickstart",
  },
});
if (startedRun.error || !startedRun.data?.run_id) {
  throw new Error("Workflow run did not start");
}

const streamController = new AbortController();
let workflowEvent: StreamEvent | undefined;

try {
  for await (const event of client.streamEvents({ signal: streamController.signal, maxReconnects: 0 })) {
    if (event.event === "workflow.run_event") {
      workflowEvent = event;
      streamController.abort();
    }
  }
} catch (error) {
  if (!(error instanceof DOMException && error.name === "AbortError")) {
    throw error;
  }
}

const finalRun = await raw.GET("/api/v1/workflow-runs/{id}", {
  params: { path: { id: startedRun.data.run_id } },
});
if (finalRun.error) {
  throw new Error("Unable to fetch workflow run detail");
}

console.log({
  workflowId: createdWorkflow.data.id,
  runId: startedRun.data.run_id,
  workflowEvent,
  finalRun: finalRun.data,
});

client.close();
```

## Notes

- `CordumClient` is the ergonomic facade for common namespaces (`jobs`, `workflows`, `policies`, `auth`, `legalHold`, `rbac`, and more).
- `createClient()` exposes the raw `openapi-fetch` surface when you need a fully typed endpoint before adding a convenience wrapper.
- Protected endpoints require the tenant header (`X-Tenant-ID`) alongside your API key or bearer token.
- `client.streamEvents()` uses the gateway SSE endpoint (`/api/v1/stream`) and reconnects with exponential backoff unless you set `maxReconnects: 0`.
- `POST` requests are only retried automatically when you supply an `Idempotency-Key` header.

## Related examples

- `../examples/node.ts`
- `../examples/browser.html`
- `../README.md`
