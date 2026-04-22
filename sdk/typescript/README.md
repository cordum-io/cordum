# @cordum/sdk

Typed TypeScript SDK for the Cordum control plane.

`@cordum/sdk` is generated from `docs/api/openapi/cordum-api.yaml` and wrapped with a small ergonomic facade for:

- full request / response types
- API key, bearer, and session auth helpers
- retry + backoff with idempotency safeguards
- async pagination helpers
- server-sent event streaming
- Node.js 18+ and modern browser support

## Install

```bash
npm install @cordum/sdk
```

## Node quickstart

```ts
import { CordumClient } from "@cordum/sdk";

const client = new CordumClient({ baseUrl: "https://api.cordum.example", auth: "cordum_api_key", tenantId: "tenant-123" });
const jobs = await client.jobs.list();
console.log(jobs.items?.map((job) => `${job.id}: ${job.state}`));
client.close();
```

## Browser quickstart

```html
<script type="module">
  import { CordumClient } from "https://esm.sh/@cordum/sdk";

  const client = new CordumClient({ baseUrl: "https://api.cordum.example", auth: "cordum_api_key", tenantId: "tenant-123" });
  const jobs = await client.jobs.list();
  document.querySelector("#jobs").textContent = JSON.stringify(jobs.items ?? [], null, 2);
</script>
```

> Browser calls require CORS to allow your app origin and the Cordum auth headers (`X-API-Key`, `Authorization`, `X-Tenant-ID`). For local, repo-backed browser demos see `examples/browser.html`.

## Authentication

### API key

```ts
import { CordumClient } from "@cordum/sdk";

const client = new CordumClient({
  baseUrl: "https://api.cordum.example",
  auth: "cordum_api_key",
  tenantId: "tenant-123",
});
```

### Bearer token

```ts
import { BearerTokenAuth, CordumClient } from "@cordum/sdk";

const client = new CordumClient({
  baseUrl: "https://api.cordum.example",
  auth: new BearerTokenAuth("bearer-token"),
  tenantId: "tenant-123",
});
```

### Session login + automatic refresh

```ts
import { CordumClient, SessionAuth } from "@cordum/sdk";

const auth = new SessionAuth({
  baseUrl: "https://api.cordum.example",
  email: "operator@example.com",
  password: "correct-horse-battery-staple",
});

const client = new CordumClient({
  baseUrl: "https://api.cordum.example",
  auth,
  tenantId: "tenant-123",
});
```

`SessionAuth` logs in lazily, caches the JWT, refreshes when the token is about to expire, and retries once after a 401.

## Retries + Idempotency-Key

Retries are enabled by default for idempotent methods (`GET`, `HEAD`, `PUT`, `DELETE`, `OPTIONS`) plus `POST` **only** when you provide an `Idempotency-Key` header.

```ts
import { CordumClient } from "@cordum/sdk";

const client = new CordumClient({ baseUrl: "https://api.cordum.example", auth: "cordum_api_key", tenantId: "tenant-123" });
const job = await client.jobs.create(
  { topic: "incident.triage", prompt: "Summarize the latest alerts" },
  { idempotencyKey: crypto.randomUUID() },
);
```

Why this matters:

- `GET` / `PUT` / `DELETE` retries are safe by default.
- `POST` retries are **disabled** unless you opt in with `Idempotency-Key`.
- `Retry-After` is honored for 429 / 503 responses.
- Jitter uses `crypto.getRandomValues`, not `Math.random`.

Customize the policy with the `retryPolicy` constructor option:

```ts
import { CordumClient } from "@cordum/sdk";

const client = new CordumClient({
  baseUrl: "https://api.cordum.example",
  auth: "cordum_api_key",
  retryPolicy: { maxRetries: 5, initialBackoffMs: 250, maxBackoffMs: 10_000 },
  tenantId: "tenant-123",
});
```

## Pagination

```ts
import { CordumClient } from "@cordum/sdk";

const client = new CordumClient({ baseUrl: "https://api.cordum.example", auth: "cordum_api_key", tenantId: "tenant-123" });

for await (const job of client.jobs.paginate()) {
  console.log(job.id, job.state);
}
```

## Event streaming

```ts
import { CordumClient } from "@cordum/sdk";

const client = new CordumClient({ baseUrl: "https://api.cordum.example", auth: "cordum_api_key", tenantId: "tenant-123" });

for await (const event of client.streamEvents({ maxReconnects: 2 })) {
  console.log(event.event, event.data);
}
```

The streaming helper understands the current gateway SSE event names:

- `job.status_changed`
- `worker.heartbeat`
- `audit.entry_created`
- `workflow.run_event`
- `policy.decision`

## Error taxonomy

| Error | Status / cause |
| --- | --- |
| `AuthenticationError` | 401 |
| `AuthorizationError` | 403 |
| `NotFoundError` | 404 |
| `ConflictError` | 409 |
| `ValidationError` | 400 / 422 |
| `RateLimitError` | 429 |
| `ServerError` | 5xx |
| `NetworkError` | transport failure |
| `TimeoutError` | request timeout |
| `RetryExhaustedError` | retry budget exhausted |

```ts
import { NotFoundError } from "@cordum/sdk";

try {
  await client.jobs.get("job-missing");
} catch (error) {
  if (error instanceof NotFoundError) {
    console.error(error.requestId, error.payload);
  }
}
```

## TypeScript tips

The package re-exports the generated OpenAPI types for custom integrations:

```ts
import { createClient, type components, type paths } from "@cordum/sdk";

type WorkflowDefinition = components["schemas"]["WorkflowDefinition"];
type ListJobsPath = paths["/api/v1/jobs"];

const raw = createClient({ baseUrl: "https://api.cordum.example" });
```

Use `createClient()` when you want the raw `openapi-fetch` surface for endpoints that have not yet been wrapped by a convenience namespace.

## Compatibility

| Environment | Support |
| --- | --- |
| Node.js | 18 / 20 / 22 |
| Chromium | current through current - 2 |
| Firefox | current through current - 2 |
| Safari / WebKit | current through current - 2 |

## Examples

- `docs/quickstart.md` — submit a workflow run, stream events, fetch final state
- `examples/node.ts` — repo-local Node example against `http://localhost:8080`
- `examples/browser.html` — repo-local browser example against `http://localhost:8080`

## Regenerating from the spec

```bash
npm install
npm run generate
npm run check:generated
npm run build
```

Generated files live under `src/_generated/`. Do not edit them by hand.
