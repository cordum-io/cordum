# Dashboard

The Cordum dashboard is a React UI for workflows, jobs, packs, and policies.

## Run locally (dev)

```bash
cd dashboard
npm install
npm run dev
```

Update `dashboard/public/config.json` to point to your gateway:

```json
{
  "apiBaseUrl": "http://localhost:8081",
  "apiKey": "",
  "tenantId": "default",
  "principalId": "dashboard",
  "principalRole": "admin"
}
```

## Run in Docker

```bash
docker build -t cordum-dashboard -f dashboard/Dockerfile dashboard

docker run --rm -p 8082:8080 \
  -e CORDUM_API_BASE_URL=http://localhost:8081 \
  -e CORDUM_TENANT_ID=default \
  cordum-dashboard
```

## Runtime configuration

The container writes `config.json` at startup from environment variables:

- `CORDUM_API_BASE_URL` (empty = same origin)
- `CORDUM_API_KEY` (embedded only when `CORDUM_DASHBOARD_EMBED_API_KEY=1`)
- `CORDUM_TENANT_ID`
- `CORDUM_PRINCIPAL_ID`
- `CORDUM_PRINCIPAL_ROLE`

For security, the dashboard does not persist API keys in localStorage. Keys live
in memory unless you explicitly embed them in `config.json`.

The default Compose stack embeds the API key into the dashboard config for local
development (`CORDUM_DASHBOARD_EMBED_API_KEY=true`). Remove that variable in
shared environments to require manual auth.
