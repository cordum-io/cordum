---
sidebar_position: 13
title: "Redis Security"
slug: /operations/redis-security
---

# Redis Security Configuration

Cordum uses Redis for job state, workflow runs, config, locks, rate limiting, and bus idempotency. This document covers the security configuration for production deployments.

## TLS

All Redis connections use TLS (rediss:// scheme). Mutual TLS is enforced in production:

- **Server certificates**: Mounted at `/etc/cordum/tls/server/`
- **Client certificates**: Mounted at `/etc/cordum/tls/client/`
- **CA certificate**: Mounted at `/etc/cordum/tls/ca/ca.crt`

Environment variables per service:

| Variable | Purpose |
|----------|---------|
| `REDIS_TLS_CA` | Path to CA certificate |
| `REDIS_TLS_CERT` | Path to client certificate |
| `REDIS_TLS_KEY` | Path to client private key |
| `REDIS_TLS_SERVER_NAME` | Expected server name in cert |
| `REDIS_TLS_INSECURE` | Skip verification (blocked in production) |

The `--tls-auth-clients yes` flag on the Redis server requires clients to present a valid certificate signed by the CA.

## Authentication

Redis requires a password on all connections. The password is set via `REDIS_PASSWORD` environment variable.

- **Development**: Default `cordum-dev` in docker-compose.yml
- **Production**: Must be set explicitly — `docker-compose.release.yml` uses `${REDIS_PASSWORD:?required}`
- **Quickstart**: `tools/scripts/quickstart.sh` generates a random password

## ACL Configuration

Redis ACL restricts each service to its required key patterns and commands.

### Files

| File | Purpose |
|------|---------|
| `config/redis/acl-dev.conf` | Development — all users have full access |
| `config/redis/acl-prod.conf` | Production — minimum-privilege per service |

### Production Users

| User | Key Patterns | Purpose |
|------|-------------|---------|
| `gateway` | `apikey:*`, `job:*`, `cfg:*`, `cordum:rl:*`, `dlq:*`, `lock:*`, `trace:*`, `sys:*`, `session:*`, `worker:*` | API gateway, auth, rate limiting |
| `scheduler` | `job:*`, `lock:*`, `worker:*`, `cordum:bus:*`, `cfg:*` | Job scheduling, bus idempotency |
| `safety-kernel` | `job:*`, `cfg:*`, `lock:*` | Safety evaluation |
| `workflow-engine` | `wf:*`, `cordum:wf:*`, `job:*`, `cfg:*`, `lock:*` | Workflow execution |
| `context-engine` | `cfg:*`, `ctx:*`, `res:*`, `art:*`, `lock:*` | Context and artifact storage |
| `admin` | `*` (all keys, all commands) | Ops and maintenance |

### Denied Commands (All Non-Admin Users)

`FLUSHALL`, `FLUSHDB`, `DEBUG`, `CONFIG`, `KEYS`, `SHUTDOWN`, `BGSAVE`, `BGREWRITEAOF`, `SLAVEOF`, `REPLICAOF`, `CLUSTER`, `MIGRATE`

### Switching to Production ACL

1. Generate unique passwords for each service user:
   ```bash
   for user in gateway scheduler safety-kernel workflow-engine context-engine admin; do
     echo "${user}: $(openssl rand -base64 32)"
   done
   ```

2. Update `config/redis/acl-prod.conf` — replace `CHANGE_ME_*` passwords.

3. Update service environment variables to use the per-user password in `REDIS_URL`:
   ```
   REDIS_URL=rediss://gateway:<password>@redis:6379
   ```

4. Set `CORDUM_REDIS_ACL_FILE` to point to your prod ACL file, or mount directly.

5. Restart Redis and all services.

### Password Rotation

1. Add new password to the user's ACL entry (Redis supports multiple passwords per user).
2. Update services to use the new password and restart them.
3. Remove the old password from the ACL entry.
4. Run `ACL SAVE` to persist.

## Verifying Configuration

```bash
# Check mutual TLS is enforced (should fail without client cert):
redis-cli --tls --cacert ca.crt -a "$REDIS_PASSWORD" ping
# Expected: Error (no client cert)

# Check with client cert (should succeed):
redis-cli --tls --cacert ca.crt --cert client.crt --key client.key -a "$REDIS_PASSWORD" ping
# Expected: PONG

# Verify ACL restricts dangerous commands:
redis-cli --tls ... -a "$GATEWAY_PASSWORD" --user gateway FLUSHALL
# Expected: NOPERM
```
