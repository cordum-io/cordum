# Troubleshooting

## "CORDUM_API_KEY is not set"

- Export `CORDUM_API_KEY` before starting Compose or running scripts.
- Example: `export CORDUM_API_KEY="$(openssl rand -hex 32)"`.

## 401 Unauthorized

- Ensure `X-API-Key` is set and matches the gateway config.
- If using `CORDUM_API_KEYS_PATH`, confirm the file is readable by the gateway.

## 403 Tenant ID required

- Add `X-Tenant-ID` to requests.
- Ensure scripts export `CORDUM_TENANT_ID`.

## Gateway not reachable

- Check `docker compose ps` and `docker compose logs cordum-api-gateway`.
- Verify the gateway is bound to `:8081`.

## Dashboard cannot reach the API

- If the dashboard is on a different origin, set `CORDUM_ALLOWED_ORIGINS` on the gateway.
- Confirm `CORDUM_API_BASE_URL` in `config.json`.

## "Permission denied" when running scripts

- Some filesystems (like `/tmp`) may be mounted `noexec`.
- Run scripts with `bash`, e.g. `bash ./tools/scripts/platform_smoke.sh`.

## NATS/Redis connection errors

- Check service health and connection URLs.
- For TLS, confirm certs and CA bundles are mounted correctly.
