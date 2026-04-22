# Production Deployment Guide

This guide covers deploying Cordum in production mode with TLS enforcement,
covering Docker Compose, Kubernetes (kustomize), and Helm.

## Production Mode

Setting `CORDUM_ENV=production` (or `CORDUM_PRODUCTION=1`) activates
production-only hardening across all services:

- **TLS 1.3 minimum** for all connections (configurable via `CORDUM_TLS_MIN_VERSION`)
- **Server TLS required** on gateway (gRPC + HTTP), safety kernel, and context engine.
  Services fail fast at startup if cert/key files are missing.
- **Client TLS enforced** for NATS and Redis connections (insecure rejected)
- **gRPC reflection disabled** by default
- **Anonymous auth blocked** (`CORDUM_ALLOW_INSECURE_NO_AUTH` ignored)
- **Header principal override disabled** (`CORDUM_ALLOW_HEADER_PRINCIPAL` ignored)

## Environment Variables Reference

### All Services

| Variable | Required | Description |
|----------|----------|-------------|
| `CORDUM_ENV` | Yes | Set to `production` |
| `CORDUM_TLS_MIN_VERSION` | No | `1.3` (default in prod) or `1.2` |

### API Gateway (server TLS)

| Variable | Required in prod | Description |
|----------|-----------------|-------------|
| `GRPC_TLS_CERT` | Yes | Path to gRPC server certificate |
| `GRPC_TLS_KEY` | Yes | Path to gRPC server private key |
| `GATEWAY_HTTP_TLS_CERT` | Yes | Path to HTTP server certificate |
| `GATEWAY_HTTP_TLS_KEY` | Yes | Path to HTTP server private key |

### Safety Kernel (server TLS)

| Variable | Required in prod | Description |
|----------|-----------------|-------------|
| `SAFETY_KERNEL_TLS_CERT` | Yes | Path to gRPC server certificate |
| `SAFETY_KERNEL_TLS_KEY` | Yes | Path to gRPC server private key |

### Context Engine (server TLS)

| Variable | Required in prod | Description |
|----------|-----------------|-------------|
| `CONTEXT_ENGINE_TLS_CERT` | Yes | Path to gRPC server certificate |
| `CONTEXT_ENGINE_TLS_KEY` | Yes | Path to gRPC server private key |

### Client TLS (NATS and Redis)

| Variable | Description |
|----------|-------------|
| `NATS_TLS_CA` | CA certificate for NATS |
| `NATS_TLS_CERT` | Client certificate for NATS |
| `NATS_TLS_KEY` | Client key for NATS |
| `REDIS_TLS_CA` | CA certificate for Redis |
| `REDIS_TLS_CERT` | Client certificate for Redis |
| `REDIS_TLS_KEY` | Client key for Redis |

## Docker Compose (Release Profile)

The `docker-compose.release.yml` file sets `CORDUM_ENV=production` on all
control-plane services by default. Server TLS env vars are commented out
as documentation. To enable TLS:

### 1. Generate or obtain certificates

For production, use certificates from your organization's CA or a public CA.

For testing with self-signed certificates, use `cordumctl generate-certs`:

```bash
cordumctl generate-certs --dir ./certs
```

This generates a full CA chain (CA, server, client) with EC P-256 keys and
correct SANs for all Cordum services. See [tls-setup.md](tls-setup.md) for
details.

Alternatively, generate with openssl:

```bash
mkdir -p certs
openssl req -x509 -newkey rsa:4096 -keyout certs/server.key \
  -out certs/server.crt -days 365 -nodes \
  -subj "/CN=cordum-local"
```

### 2. Mount certificates and uncomment TLS env vars

Add volume mounts and uncomment the TLS variables in
`docker-compose.release.yml`:

```yaml
api-gateway:
  environment:
    - CORDUM_ENV=production
    - GRPC_TLS_CERT=/etc/cordum/tls/server.crt
    - GRPC_TLS_KEY=/etc/cordum/tls/server.key
    - GATEWAY_HTTP_TLS_CERT=/etc/cordum/tls/server.crt
    - GATEWAY_HTTP_TLS_KEY=/etc/cordum/tls/server.key
  volumes:
    - ./certs:/etc/cordum/tls:ro

safety-kernel:
  environment:
    - CORDUM_ENV=production
    - SAFETY_KERNEL_TLS_CERT=/etc/cordum/tls/server.crt
    - SAFETY_KERNEL_TLS_KEY=/etc/cordum/tls/server.key
  volumes:
    - ./certs:/etc/cordum/tls:ro

context-engine:
  environment:
    - CORDUM_ENV=production
    - CONTEXT_ENGINE_TLS_CERT=/etc/cordum/tls/server.crt
    - CONTEXT_ENGINE_TLS_KEY=/etc/cordum/tls/server.key
  volumes:
    - ./certs:/etc/cordum/tls:ro
```

### 3. Start the stack

```bash
export CORDUM_API_KEY=$(openssl rand -hex 32)
export REDIS_PASSWORD=$(openssl rand -hex 32)
docker compose -f docker-compose.release.yml up -d
```

Services will fail fast with a clear error if TLS certificates are missing
in production mode.

## Kubernetes (Kustomize)

The production overlay at `deploy/k8s/production/` applies `CORDUM_ENV=production`
and full TLS wiring via the `tls-env.yaml` patch.

### 1. Create TLS secrets

```bash
# Server TLS secret (used by gateway, safety-kernel, context-engine)
kubectl -n cordum create secret tls cordum-server-tls \
  --cert=server.crt --key=server.key

# Client TLS secret (used by all services for NATS/Redis)
kubectl -n cordum create secret generic cordum-client-tls \
  --from-file=ca.crt --from-file=tls.crt=client.crt --from-file=tls.key=client.key
```

### 2. Set required secrets

```bash
kubectl -n cordum create secret generic cordum-api-key \
  --from-literal=API_KEY=$(openssl rand -hex 32)

kubectl -n cordum create secret generic cordum-redis-secret \
  --from-literal=REDIS_PASSWORD=$(openssl rand -hex 32)
```

### 3. Apply the production overlay

```bash
kubectl apply -k deploy/k8s/production/
```

The overlay patches all deployments to:
- Set `CORDUM_ENV=production`
- Use `tls://` NATS URLs and `rediss://` Redis URLs
- Mount client TLS certificates from `cordum-client-tls` secret
- Mount server TLS certificates from `cordum-server-tls` secret
- Set per-service server TLS env vars

## Helm

### Basic production install

```bash
helm install cordum ./cordum-helm -n cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set global.production=true
```

This sets `CORDUM_ENV=production` on all services. Services will fail fast
if TLS certificates are not provided.

### Production install with TLS

```bash
# Create the server TLS secret first
kubectl -n cordum create secret tls cordum-server-tls \
  --cert=server.crt --key=server.key

# Install with TLS enabled
helm install cordum ./cordum-helm -n cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set global.production=true \
  --set global.tls.enabled=true \
  --set global.tls.serverCertSecret=cordum-server-tls \
  --set networkPolicy.enabled=true \
  --set ingress.enabled=true
```

When `global.tls.enabled=true`, the chart automatically:
- Mounts the server cert secret into gateway, safety-kernel, and context-engine
- Sets `GRPC_TLS_CERT`, `GRPC_TLS_KEY`, `GATEWAY_HTTP_TLS_CERT`,
  `GATEWAY_HTTP_TLS_KEY` on the gateway
- Sets `SAFETY_KERNEL_TLS_CERT`, `SAFETY_KERNEL_TLS_KEY` on the safety kernel
- Sets `CONTEXT_ENGINE_TLS_CERT`, `CONTEXT_ENGINE_TLS_KEY` on the context engine

## Production Checklist

Before deploying to production, verify:

- [ ] `CORDUM_ENV=production` set on all control-plane services
- [ ] `CORDUM_API_KEY` set to a strong random value (`openssl rand -hex 32`)
- [ ] `REDIS_PASSWORD` set to a strong random value
- [ ] Server TLS certificates provisioned for gateway, safety-kernel, context-engine
- [ ] Client TLS certificates provisioned for NATS and Redis connections
- [ ] Network policies enabled (Helm: `networkPolicy.enabled=true`)
- [ ] Rate limits configured appropriately for expected traffic
- [ ] Pod security contexts applied (default in Helm chart)
- [ ] Ingress TLS configured (separate from service-level TLS)
- [ ] Secrets stored securely (K8s Secrets, Vault, or equivalent)
- [ ] `CORDUM_ALLOW_INSECURE_NO_AUTH` is NOT set
- [ ] `CORDUM_DASHBOARD_EMBED_API_KEY` is NOT set to `true`
- [ ] Monitoring/alerting configured for service health endpoints

## Backward Compatibility

- **Dev profile uses TLS**: The dev `docker-compose.yml` now uses TLS by
  default with auto-generated self-signed certificates. `cordumctl up` and
  `cordumctl dev` generate certificates automatically on first run.
- **Helm default unchanged**: `global.production=false` and
  `global.tls.enabled=false` are the defaults, so existing installs are
  unaffected by upgrade
- **K8s base unchanged**: `deploy/k8s/base.yaml` does not set `CORDUM_ENV`,
  only the production overlay applies it

## See Also

- [tls-setup.md](tls-setup.md) — TLS architecture, dev/prod setup, troubleshooting
