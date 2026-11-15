# Secret Rotation Runbook

## Overview

Cordum uses five credential groups that must be rotated periodically or after any suspected compromise. Secrets live in `.env` and the TLS certificates in `./certs/` (neither is committed — see `.gitignore`). Reference `.env.example` for the full variable catalog.

## Secrets

| Secret | Env Var | Min Length | Generate | Used By |
|--------|---------|-----------|----------|---------|
| Redis password | `REDIS_PASSWORD` | 12 chars | `openssl rand -hex 16` | All services via `REDIS_URL` |
| API key | `CORDUM_API_KEY` | 32 chars | `openssl rand -hex 32` | Gateway, dashboard |
| Admin password | `CORDUM_ADMIN_PASSWORD` | 16 chars | `openssl rand -base64 24` | Gateway (user auth) |
| TLS certificates (NATS client auth) | files in `./certs/` (gitignored) | n/a | `cordumctl generate-certs --force` | NATS, Redis, gateway (HTTPS + gRPC), safety kernel, context engine |
| License token | `CORDUM_LICENSE_TOKEN` | n/a | Issued by licensing portal | Gateway, scheduler, safety kernel, workflow engine |

## Rotation Procedures

### Redis Password

Compose starts Redis with `--requirepass "$REDIS_PASSWORD"` and passes the same value to every service in `REDIS_URL`, so `REDIS_PASSWORD` is the one place to change it. A live `ACL SETUSER` or `CONFIG SET requirepass` doesn't persist: the next time the container starts, the password reverts to `REDIS_PASSWORD`.

1. Generate new password: `openssl rand -hex 16`
2. Update `.env` with new `REDIS_PASSWORD`. A value exported in your shell overrides `.env`, so update or unset it there too.
3. Recreate the stack: `docker compose up -d`. Compose recreates Redis and every service whose configuration uses the password.
4. Restart anything outside Compose that still uses the old password, such as workers you run by hand.
5. Verify connectivity: `docker compose exec redis redis-cli --tls --cacert /etc/cordum/tls/ca/ca.crt -a '<new-password>' PING`

**Downtime:** Redis restarts with the new password, so expect a short outage while services reconnect.

### API Key

1. Generate new key: `openssl rand -hex 32`
2. Update `.env` with new `CORDUM_API_KEY`
3. Restart the gateway
4. Update all API clients (dashboard, CLI, external integrations) with the new key
5. Verify: `curl --cacert ./certs/ca/ca.crt -H 'X-API-Key: <new-key>' https://localhost:8081/api/v1/health`

**Zero-downtime:** Use `CORDUM_API_KEYS` (JSON array) to support both old and new keys during transition. Remove old key after all clients are updated.

### Admin Password

1. Generate new password: `openssl rand -base64 24`
2. Update `.env` with new `CORDUM_ADMIN_PASSWORD`
3. Restart the gateway (new password takes effect on next login)
4. Log in with new credentials to verify

### NATS Client Certificates

Under Docker Compose, NATS has no token to rotate. `config/nats.dev-tls.conf` (and `config/nats.release.conf` for `docker-compose.release.yml`) has no `authorization` block; its `tls` block sets `verify: true`, so NATS accepts any client that presents a certificate signed by the CA in `./certs/ca/`. Services present `./certs/client/tls.crt`. The server never checks the `NATS_TOKEN` that Compose passes to services, so changing it does nothing.

Rotate the certificates instead. `cordumctl generate-certs` writes one set (CA, server and client certificates) that NATS, Redis, the gateway, the safety kernel and the context engine all use, so they rotate together:

1. From the repo root, regenerate the set: `cordumctl generate-certs --force` (or `go run ./cmd/cordumctl generate-certs --force`). Without `--force` it refuses to overwrite `./certs/`; `--days N` changes the 365-day validity.
2. Restart the whole stack so every service loads the new files: `docker compose down && docker compose up -d`
3. Update anything outside Compose: restart workers you run by hand with `./certs/client`, and re-import `./certs/ca/ca.crt` wherever the old CA was trusted (OS or browser trust store, `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`).
4. Verify: `docker compose ps` shows every service healthy, and `curl --cacert ./certs/ca/ca.crt -H "X-API-Key: $CORDUM_API_KEY" https://localhost:8081/api/v1/health` succeeds.

**Note:** The new CA replaces the old one, so old and new certificates can't overlap. Plan a short restart window.

## After a Suspected Compromise

1. Rotate every credential in the [Secrets](#secrets) table immediately
2. Check audit logs for unauthorized access
3. Revoke all active sessions
4. Review recent API key usage patterns
5. Notify team via secure channel

## Validation

The gateway validates secret strength at startup when `CORDUM_ENV=production`. Weak secrets are rejected with actionable error messages. Set `CORDUM_SKIP_SECRET_VALIDATION=true` only for development.
