# Security

Cordum is designed for secure-by-default deployments. Use this checklist before
production.

## Baseline requirements

- **API key required**: every request must include `X-API-Key` and `X-Tenant-ID`.
- **Tenant enforcement**: empty tenant IDs are rejected.
- **Fail-closed auth**: missing or invalid keys return 401.
- **No insecure auth bypass**: `CORDUM_ALLOW_INSECURE_NO_AUTH=1` is dev-only and blocked in production.

## Production mode

Set `CORDUM_ENV=production` (or `CORDUM_PRODUCTION=true`) to enable strict
security checks. Production mode requires:

- TLS for HTTP and gRPC.
- TLS for Redis and NATS clients.
- A configured policy verification key.

## Policy signature verification

Signed policies prevent tampering. Configure:

- `SAFETY_POLICY_PUBLIC_KEY` (base64 encoded)
- `SAFETY_POLICY_SIGNATURE` or `SAFETY_POLICY_SIGNATURE_PATH`

If production mode is enabled and no public key is configured, the Safety Kernel
fails to start.

## Metrics exposure

Metrics endpoints bind to loopback in production by default. To expose them:

- `GATEWAY_METRICS_PUBLIC=1`
- `SCHEDULER_METRICS_PUBLIC=1`

## Key management

- Rotate API keys regularly.
- Store secrets in a KMS or secret manager.
- Use `CORDUM_API_KEYS_PATH` for hot-reload without restarts.

## Client-side security

The dashboard does not persist API keys in localStorage. Avoid embedding keys in
`config.json` unless the UI is restricted to trusted operators. If you do
embed them, use `CORDUM_DASHBOARD_EMBED_API_KEY=1` and disable it in shared
environments.
