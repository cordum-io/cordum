# Cordum Dashboard

Production dashboard for Cordum. Built with React 18, TypeScript, and Tailwind.

## Local Development

```bash
cd dashboard
npm install
npm run dev
```

By default, the dashboard uses `/config.json` from `dashboard/public`. To point at a local gateway, update the API base URL:

```json
{
  "apiBaseUrl": "http://localhost:8081",
  "apiKey": "super-secret-key",
  "tenantId": "default",
  "principalId": "dashboard",
  "principalRole": "admin"
}
```

## Runtime Configuration

The production container writes `config.json` at startup from environment variables:

- `CORDUM_API_BASE_URL` (empty = same origin)
- `CORDUM_API_KEY`
- `CORDUM_TENANT_ID`
- `CORDUM_PRINCIPAL_ID`
- `CORDUM_PRINCIPAL_ROLE` (set to `admin` to edit/publish policy bundles when RBAC is enforced)

If you host the dashboard on a different origin than the gateway, set `CORDUM_ALLOWED_ORIGINS` on the gateway to allow the dashboard origin.

The live bus stream (`/api/v1/stream`) authenticates via WebSocket subprotocols:
`Sec-WebSocket-Protocol: cordum-api-key, <base64url>` (the dashboard sets this automatically when an API key is configured).

## Caching Headers

The dashboard nginx config sets `index.html` and `config.json` to no-cache to
avoid stale asset references after deploys. The `/assets/` bundle is served with
long-lived immutable cache headers.

## System Config: Observability + Alerting

The System page writes observability and alerting settings into the config service
(scope `system`, scope_id `default`) via `POST /api/v1/config`. Admin role is required
when enterprise RBAC is enabled.

Example:

```json
{
  "observability": {
    "otel": {
      "enabled": true,
      "endpoint": "otel-collector:4317",
      "protocol": "grpc",
      "headers": {},
      "resource_attributes": {
        "service.name": "cordum-gateway"
      }
    },
    "grafana": {
      "base_url": "https://grafana.example.com",
      "dashboards": {
        "system_overview": "d/abcd/system-overview",
        "workflow_performance": "d/wxyz/workflow-performance"
      }
    }
  },
  "alerting": {
    "pagerduty": {
      "enabled": true,
      "integration_key": "pd-key",
      "severity": "critical"
    },
    "slack": {
      "enabled": true,
      "webhook_url": "https://hooks.slack.com/services/...",
      "severity": "error"
    }
  }
}
```

## Docker Build

```bash
docker build -t cordum-dashboard -f dashboard/Dockerfile dashboard
```

Run the container and point it at the API gateway:

```bash
docker run --rm -p 8082:8080 \
  -e CORDUM_API_BASE_URL=http://localhost:8081 \
  -e CORDUM_API_KEY=super-secret-key \
  cordum-dashboard
```

## Compose

`docker-compose.yml` includes `cordum-dashboard` on port `8082`.

## Workflow Authoring

The dashboard includes a JSON workflow editor to create/update workflows via `POST /api/v1/workflows`.

## Run Listing

Runs are fetched from the gateway using the paginated endpoint:

- `GET /api/v1/workflow-runs?limit=50&cursor=<unix>&status=running&workflow_id=<id>`

## Jobs + Search

The dashboard includes `/jobs` for paginated job inspection and `/search` for global lookup.

- `GET /api/v1/jobs?limit=50&cursor=<micros>&state=RUNNING&topic=job.default`
- `GET /api/v1/workflow-runs?limit=50` (used by search)
- `GET /api/v1/workflows`, `GET /api/v1/packs`

## Packs Management

The dashboard can manage pack lifecycle using the gateway APIs:

- `GET /api/v1/packs`
- `POST /api/v1/packs/install` (multipart field `bundle`; optional fields `force`, `upgrade`, `inactive`)
- `POST /api/v1/packs/{id}/verify`
- `POST /api/v1/packs/{id}/uninstall` (JSON body `{ "purge": true }`)

## Approvals

Bulk approval/rejection supports optional `reason` + `note` payloads:

- `POST /api/v1/approvals/{job_id}/approve` with `{ "reason": "...", "note": "..." }`
- `POST /api/v1/approvals/{job_id}/reject` with `{ "reason": "...", "note": "..." }`

Approve/Reject buttons appear in the Policy inbox, the Job detail view, and the Run detail Overview/Jobs tabs when a job is marked `approval_required`.

## Remediations

When Safety Kernel returns remediations, the Job detail view shows suggested alternatives and can apply them:

- `POST /api/v1/jobs/{job_id}/remediate` (optional body `{ "remediation_id": "..." }`)

Applied remediations create a new job with updated topic/capability/labels.

## Decision Audit Log

Job detail displays a safety decision history using:

- `GET /api/v1/jobs/{job_id}/decisions`

## Policy Rules

The policy rules list is sourced from:

- `GET /api/v1/policy/rules`

## Policy Bundles + Snapshots

The policy diff view uses bundle snapshots stored in the config service:

- `GET /api/v1/policy/bundles`
- `GET /api/v1/policy/bundles/{id}`
- `PUT /api/v1/policy/bundles/{id}` (requires admin role when enterprise RBAC is enabled)
- `POST /api/v1/policy/bundles/{id}/simulate`
- `GET /api/v1/policy/bundles/snapshots`
- `POST /api/v1/policy/bundles/snapshots` with `{ "note": "..." }`
- `GET /api/v1/policy/bundles/snapshots/{id}`
- `POST /api/v1/policy/publish` (requires admin role when enterprise RBAC is enabled)
- `POST /api/v1/policy/rollback` (requires admin role when enterprise RBAC is enabled)
- `GET /api/v1/policy/audit`

Bundle IDs include `/` (e.g. `secops/workflows`). When calling the REST endpoints, replace `/` with `~`
in the `{id}` path segment or use the `bundle_id` query parameter.

The Policy Studio editor is read-only unless `principalRole` (or `X-Principal-Role`) is set to `admin`.

Backend enforcement of admin-only actions is provided by the enterprise auth provider.

## DLQ Pagination

The DLQ list can be paginated via:

- `GET /api/v1/dlq/page?limit=100&cursor=<unix>`

## Kubernetes

`deploy/k8s/base.yaml` and `deploy/k8s/ingress.yaml` include the dashboard deployment and routing. The ingress maps `/` to the dashboard service and `/api/v1` to the gateway.
