# Secrets Manager Integration

Cordum supports resolving `secret://` references against external secrets
managers at runtime.  When configured, job payloads containing secret URIs
are resolved to their actual values before dispatch.  When no provider is
configured, references are detected and redacted (defense-in-depth).

## URI Format

```
secret://<provider>/<path>[#<key>]
```

| Provider | Scheme | Example |
|---|---|---|
| HashiCorp Vault (KV v2) | `vault` | `secret://vault/database/creds#password` |
| AWS Secrets Manager | `aws-sm` | `secret://aws-sm/prod/api-key` |
| Kubernetes (future) | `k8s` | `secret://k8s/default/my-secret#token` |

The optional `#key` fragment extracts a single field from a JSON-structured
secret value.  Without a fragment, the entire secret string is returned
(for Vault, the secret must have exactly one field).

## Configuration

### HashiCorp Vault

| Env Var | Required | Description |
|---|---|---|
| `VAULT_ADDR` | Yes | Vault server address (e.g. `https://vault.example.com:8200`) |
| `VAULT_TOKEN` | Yes | Vault authentication token |
| `VAULT_MOUNT` | No | KV v2 mount point (default: `secret`) |

```bash
export VAULT_ADDR=https://vault.example.com:8200
export VAULT_TOKEN=s.xxxxxxxxxxxxxxxxxxxxxxxx
export VAULT_MOUNT=secret
```

### AWS Secrets Manager

| Env Var | Required | Description |
|---|---|---|
| `AWS_REGION` | Yes | AWS region (e.g. `us-east-1`) |
| `AWS_ACCESS_KEY_ID` | Yes | AWS access key ID |
| `AWS_SECRET_ACCESS_KEY` | Yes | AWS secret access key |
| `AWS_SESSION_TOKEN` | No | STS session token (for temporary credentials) |
| `AWS_ENDPOINT_URL` | No | Override endpoint (for LocalStack / testing) |

```bash
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

### Cache Configuration

Resolved secret values are cached in memory to avoid excessive calls to
the backend.  The cache is per-gateway-process and is cleared on restart.

| Env Var | Default | Description |
|---|---|---|
| `SECRET_CACHE_TTL` | `5m` | Cache TTL (Go duration or seconds).  Set to `0` to disable. |

## Security Model

1. **Resolver runs only in the gateway** — workers never resolve secrets
   directly.  The gateway resolves references before dispatching job
   payloads.

2. **Fail-closed** — if a provider is configured but a secret cannot be
   resolved (not found, access denied, timeout), the job submission
   fails with an error.  Secrets are never silently replaced with empty
   strings.

3. **No secret in logs** — resolved values are never logged.  Only the
   masked path (e.g. `database/****`) appears in log messages.

4. **Cache isolation** — the in-memory cache is process-local and not
   shared across gateway replicas.  Cache entries expire after the
   configured TTL.

5. **Redaction fallback** — when no resolver is configured, the existing
   `ContainsSecretRefs` / `RedactSecretRefs` pipeline tags the job with
   `secrets_present=true` and the `secrets` risk tag, allowing the
   Safety Kernel to enforce policy (e.g. deny flows with unresolved
   secrets).

## Vault Setup Guide

### 1. Enable KV v2

```bash
vault secrets enable -version=2 -path=secret kv
```

### 2. Write a secret

```bash
vault kv put secret/database/creds \
  username=admin \
  password=$(openssl rand -hex 16)
```

### 3. Create a policy

```hcl
# cordum-gateway-policy.hcl
path "secret/data/*" {
  capabilities = ["read"]
}
```

```bash
vault policy write cordum-gateway cordum-gateway-policy.hcl
```

### 4. Create a token

```bash
vault token create -policy=cordum-gateway -period=768h
```

### 5. Key rotation

Rotate secrets in Vault (new KV version).  Cordum resolves the latest
version automatically.  To force cache refresh, restart the gateway or
set `SECRET_CACHE_TTL=0`.

## AWS IAM Requirements

The IAM identity used by the gateway needs:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:*:*:secret:*"
    }
  ]
}
```

For production, scope the `Resource` ARN to specific secrets.

## Observability

The resolver exposes Prometheus metrics:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `cordum_secrets_resolve_total` | Counter | `provider`, `status` | Total resolution attempts |
| `cordum_secrets_resolve_duration_seconds` | Histogram | `provider` | Resolution latency |
| `cordum_secrets_cache_hits_total` | Counter | — | Cache hits |
| `cordum_secrets_cache_misses_total` | Counter | — | Cache misses |

Status labels: `ok`, `not_found`, `access_denied`, `no_provider`, `error`.

## Troubleshooting

### "secrets resolver: no providers configured"

No `VAULT_ADDR` or `AWS_REGION` environment variables are set.  This is
informational — the gateway operates in redaction-only mode.

### "secrets provider partially configured"

One required env var is set but others are missing.  For example,
`VAULT_ADDR` is set but `VAULT_TOKEN` is empty.  Check the env var
table above.

### "vault: .../****:  access denied"

The Vault token lacks `read` capability on the secret path.  Verify the
token's policy includes `capabilities = ["read"]` for the path.

### "aws-sm: .../****:  secret not found"

The secret name in the URI doesn't match any secret in the configured
AWS region.  Verify the region and secret name.
