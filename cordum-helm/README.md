# Cordum Helm Chart

This chart deploys the Cordum control plane (gateway, scheduler, safety kernel,
workflow engine, context engine, dashboard) plus Redis and NATS by default.
Cordum Edge P0 backend APIs are served by the API Gateway; the developer-side
`cordumctl`, `cordum-agentd`, `cordum-hook`, and `cordum-claude` binaries are
installed on user machines rather than as Kubernetes pods.

## Required Values

| Value | Required | Description |
|-------|----------|-------------|
| `secrets.apiKey` | Yes | API authentication key (`openssl rand -hex 32`) |
| `redis.auth.password` | Yes (when `redis.auth.enabled=true`) | Redis password (`openssl rand -hex 32`) |
| `secrets.adminPassword` | When `gateway.env.userAuthEnabled=true` | Admin user password |

## Install (local chart)

```bash
helm install cordum ./cordum-helm -n cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set gateway.env.tenantId=default \
  --set dashboard.env.tenantId=default
```

## Install (published chart)

```bash
helm repo add cordum https://charts.cordum.io
helm repo update
helm install cordum cordum/cordum -n cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set gateway.env.tenantId=default \
  --set dashboard.env.tenantId=default
```

Note: the chart defaults to the image tag in `values.yaml` (currently `1.0.0`)
and pulls from GHCR. The release workflow rewrites this default to the pushed
tag before packaging the OCI chart. Override `global.image.tag` if your
registry uses a different tag.

## Configuration

Common overrides:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set global.image.tag=1.0.0 \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set gateway.env.tenantId=default \
  --set dashboard.env.tenantId=default \
  --set ingress.enabled=true
```

Cordum Edge P0 backend tuning:

```bash
helm upgrade --install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set gateway.env.edgeSessionRetentionTTL=168h \
  --set gateway.env.edgeSessionSweepInterval=10m \
  --set gateway.env.edgeMaxExecutionsPerSession=500 \
  --set gateway.env.edgeExportMaxBytes=52428800
```

These values set the Gateway environment variables used by Edge session
retention, sweeps, execution caps, and evidence export limits. Policy behavior
still comes from Safety Kernel/global policy; install or merge the Edge policy
pack/rules you want for allow/deny/human-approval demos.

Use external Redis/NATS:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set nats.enabled=false \
  --set redis.enabled=false \
  --set external.natsUrl=nats://nats.example.com:4222 \
  --set external.redisUrl=redis://redis.example.com:6379
```

Use an external safety kernel:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set safetyKernel.enabled=false \
  --set external.safetyKernelAddr=safety-kernel.example.com:50051
```

Tune JetStream durability (fsync cadence):

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set nats.jetstream.syncInterval=1s
```

Enable user/password authentication:

```bash
helm install cordum ./cordum-helm \
  -n cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set gateway.env.userAuthEnabled=true \
  --set secrets.adminPassword=<secure-password> \
  --set gateway.env.adminUsername=admin \
  --set gateway.env.adminEmail=admin@example.com
```

This creates a default admin user on first startup. Users can then:
- Login via the dashboard with username/password
- Change their password via `POST /api/v1/auth/password`
- Admins can create new users via `POST /api/v1/users`

## Local dev (kind + local images)

If you are installing from a local clone and do not have published images,
build and load images into kind, then override tags:

```bash
docker compose build

for svc in api-gateway scheduler safety-kernel workflow-engine context-engine; do
  docker tag "cordum-cordum-${svc}:latest" "ghcr.io/cordum-io/cordum/control-plane:dev-${svc}"
done
docker tag cordum-cordum-dashboard:latest ghcr.io/cordum-io/cordum/dashboard:dev

kind load docker-image --name cordum \
  ghcr.io/cordum-io/cordum/control-plane:dev-api-gateway \
  ghcr.io/cordum-io/cordum/control-plane:dev-scheduler \
  ghcr.io/cordum-io/cordum/control-plane:dev-safety-kernel \
  ghcr.io/cordum-io/cordum/control-plane:dev-workflow-engine \
  ghcr.io/cordum-io/cordum/control-plane:dev-context-engine \
  ghcr.io/cordum-io/cordum/dashboard:dev

helm upgrade --install cordum ./cordum-helm -n cordum --create-namespace \
  --set global.image.tag=dev \
  --set dashboard.image.tag=dev
```

## Access (port-forward)

Services are ClusterIP by default. For local access:

```bash
kubectl -n cordum port-forward svc/cordum-api-gateway 8081:8081
kubectl -n cordum port-forward svc/cordum-dashboard 8082:8080
```

Dashboard: `http://localhost:8082`
HTTP requests must include `X-API-Key` and `X-Tenant-ID` (use `gateway.env.tenantId` as the default tenant).

## Security Configuration

### Redis Authentication

Redis authentication is enabled by default (`redis.auth.enabled=true`). You must
provide a password:

```bash
--set redis.auth.password=$(openssl rand -hex 32)
```

To use an existing Kubernetes secret instead:

```bash
--set redis.auth.existingSecret=my-redis-secret \
--set redis.auth.existingSecretKey=REDIS_PASSWORD
```

### Pod and Container Security Contexts

Security contexts are applied by default to all pods and containers:

- **Pod-level**: `runAsNonRoot`, UID/GID 65532, `RuntimeDefault` seccomp profile
- **Container-level**: `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, drop all capabilities

NATS and Redis containers override `readOnlyRootFilesystem: false` since they
write to data directories. To disable security contexts entirely:

```bash
--set podSecurityContext=null \
--set containerSecurityContext=null
```

### Network Policies

Network policies are disabled by default. Enable them for production:

```bash
--set networkPolicy.enabled=true
```

When enabled, a default-deny policy is applied to all Cordum pods, and
per-component allow rules permit only the required traffic:

- Redis accepts connections from control-plane services only
- NATS accepts connections from scheduler, gateway, workflow-engine only
- Gateway accepts ingress from the ingress controller and dashboard
- Dashboard egress is limited to the gateway

Configure the ingress controller label selector if not using ingress-nginx:

```bash
--set networkPolicy.ingressControllerSelector.matchLabels."app\.kubernetes\.io/name"=traefik
```

### Production Install

```bash
helm install cordum ./cordum-helm -n cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set global.production=true \
  --set networkPolicy.enabled=true \
  --set ingress.enabled=true
```

Setting `global.production=true` adds `CORDUM_ENV=production` to all services,
enabling TLS enforcement, strict security defaults, and TLS 1.3 minimum.

### Production Install with TLS

```bash
# Create server TLS secret first:
kubectl -n cordum create secret tls cordum-server-tls \
  --cert=server.crt --key=server.key

helm install cordum ./cordum-helm -n cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set global.production=true \
  --set global.tls.enabled=true \
  --set global.tls.serverCertSecret=cordum-server-tls \
  --set networkPolicy.enabled=true \
  --set ingress.enabled=true
```

When `global.tls.enabled=true`, the chart mounts server certificates and sets
TLS env vars for gateway (gRPC + HTTP), safety kernel, and context engine.
See [Production Deployment Guide](../docs/guides/production-deployment.md) for
full details.

## Migration Notes

When upgrading from a chart version that did not include these security features:

- **Redis auth**: Existing installs must set `redis.auth.password` on the next
  `helm upgrade`. If you were using Redis without a password, set
  `redis.auth.enabled=false` to opt out.
- **Security contexts**: New defaults are applied automatically. To preserve
  previous behavior (no security context), set `podSecurityContext=null` and
  `containerSecurityContext=null`.
- **Network policies**: Disabled by default (`networkPolicy.enabled=false`), so
  no breaking change on upgrade.
- **Production mode**: New `global.production` (default `false`) and
  `global.tls` settings have no effect unless explicitly enabled.

## Notes

- Gateway HTTP is exposed on the `api-gateway` service (port 8081).
- Dashboard is exposed on the `dashboard` service (port 8080).
- Update `config.safety`, `config.pools`, and `config.timeouts` in values.yaml
  to control policy and routing defaults.
