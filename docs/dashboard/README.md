# coretexOS Dashboard

Production dashboard for coretexOS. Built with React 18, TypeScript, and Tailwind.

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
  "tenantId": "default"
}
```

## Runtime Configuration

The production container writes `config.json` at startup from environment variables:

- `CORETEX_API_BASE_URL` (empty = same origin)
- `CORETEX_API_KEY`
- `CORETEX_TENANT_ID`

If you host the dashboard on a different origin than the gateway, set `CORETEX_ALLOWED_ORIGINS` on the gateway to allow the dashboard origin.

## Docker Build

```bash
docker build -t coretex-dashboard -f dashboard/Dockerfile dashboard
```

Run the container and point it at the API gateway:

```bash
docker run --rm -p 8082:8080 \
  -e CORETEX_API_BASE_URL=http://localhost:8081 \
  -e CORETEX_API_KEY=super-secret-key \
  coretex-dashboard
```

## Compose

`docker-compose.yml` includes `coretex-dashboard` on port `8082`.

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

## Decision Audit Log

Job detail displays a safety decision history using:

- `GET /api/v1/jobs/{job_id}/decisions`

## Policy Rules

The policy rules list is sourced from:

- `GET /api/v1/policy/rules`

## Policy Bundles + Snapshots

The policy diff view uses bundle snapshots stored in the config service:

- `GET /api/v1/policy/bundles`
- `GET /api/v1/policy/bundles/snapshots`
- `POST /api/v1/policy/bundles/snapshots` with `{ "note": "..." }`
- `GET /api/v1/policy/bundles/snapshots/{id}`

## DLQ Pagination

The DLQ list can be paginated via:

- `GET /api/v1/dlq/page?limit=100&cursor=<unix>`

## Kubernetes

`deploy/k8s/base.yaml` and `deploy/k8s/ingress.yaml` include the dashboard deployment and routing. The ingress maps `/` to the dashboard service and `/api/v1` to the gateway.
