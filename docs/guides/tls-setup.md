# TLS Setup Guide

Cordum enables TLS by default for all environments. Dev uses auto-generated
self-signed certificates with full verification. Production uses
operator-provided certificates. The code paths are identical.

## Architecture Overview

Cordum uses a single CA trust chain for all internal communication:

```
                    CA Certificate
                    /     |      \
              Server    Server    Client
              Cert      Cert      Cert
              (gateway,  (NATS,   (all services
               safety,   Redis)   as NATS/Redis
               context)            clients)
```

### What Gets Encrypted

| Connection | Protocol | Scheme |
|-----------|----------|--------|
| Gateway HTTP API | TLS | `https://` |
| Gateway gRPC | TLS | gRPC with TLS credentials |
| Safety kernel gRPC | TLS | gRPC with TLS credentials |
| Context engine gRPC | TLS | gRPC with TLS credentials |
| NATS messaging | mTLS | `tls://` |
| Redis state store | TLS | `rediss://` |
| Dashboard → Gateway (nginx proxy) | TLS | `https://` upstream |

### Certificate Layout

```
certs/
├── ca/
│   ├── ca.crt          # CA certificate (shared trust root)
│   └── ca.key          # CA private key (keep secure)
├── server/
│   ├── tls.crt         # Server certificate (SANs: localhost, service names)
│   └── tls.key         # Server private key
└── client/
    ├── tls.crt         # Client certificate
    └── tls.key         # Client private key
```

Certificates are EC P-256 with PKCS8-encoded keys. Serial numbers use 128-bit
`crypto/rand` values.

### Default SANs

Auto-generated server certificates include these Subject Alternative Names:

- `localhost`
- `nats`, `redis`
- `cordum-api-gateway`, `api-gateway`
- `safety-kernel`, `cordum-safety-kernel`
- `scheduler`, `cordum-scheduler`
- `workflow-engine`, `cordum-workflow-engine`
- `context-engine`, `cordum-context-engine`
- `dashboard`, `cordum-dashboard`

## Dev Setup

### Automatic (Recommended)

TLS certificates are generated automatically when you start the stack:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
cordumctl up        # or: cordumctl dev
```

`cordumctl up` and `cordumctl dev` call `ensureDevCerts()` before starting
Docker Compose. If `certs/ca/ca.crt` does not exist, certificates are generated
into `./certs/`. If certificates already exist, the step is skipped.

### Manual Generation

To generate certificates explicitly:

```bash
cordumctl generate-certs
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `./certs` | Output directory |
| `--force` | `false` | Overwrite existing certificates |
| `--days` | `365` | Certificate validity in days |

```bash
# Custom directory
cordumctl generate-certs --dir /path/to/certs

# Regenerate expired certificates
cordumctl generate-certs --force
```

### Project Initialization

`cordumctl init` also generates certificates as part of project scaffolding:

```bash
cordumctl init my-project
# Creates my-project/certs/ with full CA chain
```

### Docker Compose Wiring

The default `docker-compose.yml` mounts `./certs` into all containers and sets
TLS environment variables automatically. No manual compose file editing is
needed.

Key compose settings:

- NATS uses `config/nats.dev-tls.conf` with `verify: true`
- Redis uses `--tls-port 6379` with `--port 0` (TLS-only)
- All services use `tls://nats:4222` and `rediss://redis:6379`
- Gateway, safety kernel, and context engine serve TLS on their gRPC/HTTP ports

## Production Setup

Production uses operator-provided certificates instead of auto-generated ones.

### Docker Compose

1. Obtain certificates from your CA (or use `cordumctl generate-certs` for
   testing).

2. Mount certificates and start with `docker-compose.release.yml`:

```bash
export CORDUM_API_KEY=$(openssl rand -hex 32)
export REDIS_PASSWORD=$(openssl rand -hex 32)
docker compose -f docker-compose.release.yml up -d
```

See [production-deployment.md](production-deployment.md) for the full Docker
Compose production guide.

### Kubernetes

1. Create TLS secrets:

```bash
kubectl -n cordum create secret tls cordum-server-tls \
  --cert=server.crt --key=server.key

kubectl -n cordum create secret generic cordum-client-tls \
  --from-file=ca.crt \
  --from-file=tls.crt=client.crt \
  --from-file=tls.key=client.key
```

2. Apply the production overlay:

```bash
kubectl apply -k deploy/k8s/production/
```

The production overlay (`deploy/k8s/production/patches/tls-env.yaml`) patches
all deployments with TLS environment variables and volume mounts.

### Helm

```bash
helm install cordum ./cordum-helm -n cordum --create-namespace \
  --set global.production=true \
  --set global.tls.enabled=true \
  --set global.tls.serverCertSecret=cordum-server-tls
```

See [production-deployment.md](production-deployment.md) for complete Helm
TLS instructions.

## Environment Variable Reference

### Client TLS (NATS)

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_TLS_CA` | — | CA certificate path |
| `NATS_TLS_CERT` | — | Client certificate path |
| `NATS_TLS_KEY` | — | Client private key path |
| `NATS_TLS_SERVER_NAME` | — | TLS server name override |
| `NATS_TLS_INSECURE` | — | Skip TLS verification (not recommended) |

### Client TLS (Redis)

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_TLS_CA` | — | CA certificate path |
| `REDIS_TLS_CERT` | — | Client certificate path |
| `REDIS_TLS_KEY` | — | Client private key path |
| `REDIS_TLS_SERVER_NAME` | — | TLS server name override |
| `REDIS_TLS_INSECURE` | — | Skip TLS verification (not recommended) |

### Server TLS (Gateway)

| Variable | Description |
|----------|-------------|
| `GATEWAY_HTTP_TLS_CERT` | HTTP server certificate |
| `GATEWAY_HTTP_TLS_KEY` | HTTP server private key |
| `GRPC_TLS_CERT` | gRPC server certificate |
| `GRPC_TLS_KEY` | gRPC server private key |

### Server TLS (Safety Kernel)

| Variable | Description |
|----------|-------------|
| `SAFETY_KERNEL_TLS_CERT` | gRPC server certificate |
| `SAFETY_KERNEL_TLS_KEY` | gRPC server private key |

### Server TLS (Context Engine)

| Variable | Description |
|----------|-------------|
| `CONTEXT_ENGINE_TLS_CERT` | gRPC server certificate |
| `CONTEXT_ENGINE_TLS_KEY` | gRPC server private key |

### Dashboard TLS

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_UPSTREAM_SCHEME` | `http` | Set to `https` when gateway uses TLS |

### CLI TLS

| Variable | Description |
|----------|-------------|
| `CORDUM_TLS_CA` | CA certificate path for CLI connections |
| `CORDUM_TLS_INSECURE` | Skip TLS verification (dev/debug only) |

### Global

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_TLS_MIN_VERSION` | `1.2` (dev) / `1.3` (prod) | Minimum TLS version |

## Troubleshooting

### x509: certificate signed by unknown authority

The client does not trust the CA that signed the server certificate.

**Fix:** Ensure `NATS_TLS_CA`, `REDIS_TLS_CA`, or `--cacert` points to the
correct CA certificate file (`certs/ca/ca.crt`).

```bash
# CLI
cordumctl status --cacert ./certs/ca/ca.crt

# curl
curl --cacert ./certs/ca/ca.crt https://localhost:8081/api/v1/status
```

### x509: certificate has expired or is not yet valid

The certificate validity period has elapsed or the system clock is wrong.

**Fix:** Regenerate certificates:

```bash
cordumctl generate-certs --force --days 365
```

Then restart the stack.

### connection refused on TLS port

The service is not listening with TLS, or the port mapping is wrong.

**Fix:** Check that the service has TLS cert/key env vars set and the
certificates exist at the specified paths. Check container logs:

```bash
docker compose logs api-gateway | grep -i tls
```

### tls: bad certificate

The client certificate is rejected by the server (mTLS failure).

**Fix:** Ensure the client certificate was signed by the same CA the server
trusts. For NATS with `verify: true`, client certs must be present.

### NATS connection timeout with TLS

NATS expects `tls://` scheme when TLS is enabled.

**Fix:** Set `NATS_URL=tls://nats:4222` (not `nats://`).

### Redis connection error with TLS

Redis expects `rediss://` scheme (double 's') for TLS connections.

**Fix:** Set `REDIS_URL=rediss://redis:6379` (not `redis://`).

### Dashboard shows "Bad Gateway" after enabling TLS

The nginx reverse proxy cannot reach the gateway over HTTPS.

**Fix:** Set `CORDUM_API_UPSTREAM_SCHEME=https` on the dashboard container and
mount the CA certificate at `/etc/cordum/tls/ca/ca.crt`.

## Verification

Run the E2E TLS verification script after starting the stack:

```bash
export CORDUM_API_KEY="your-key"
./tools/scripts/tls_verify.sh
```

This runs 7 checks:

1. Gateway HTTPS reachable with `--cacert`
2. WebSocket upgrade works over TLS
3. CLI connects with `--cacert`
4. CLI fails without `--cacert` (proves real verification)
5. Mock-bank worker connects to NATS over TLS
6. NATS rejects unauthenticated connections (mTLS)
7. No Redis TLS errors in service logs

## See Also

- [production-deployment.md](production-deployment.md) — Full production deployment guide
- [configuration-reference.md](../configuration-reference.md) — Complete env var reference
- [cli-reference.md](../cli-reference.md) — CLI command reference
- [DOCKER.md](../DOCKER.md) — Docker Compose deployment
