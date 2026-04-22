# Production hardening profile and TLS 1.3 enforcement

Repo: cordum

## Problem
`SECURITY.md` claims TLS 1.3 for all network traffic, but HTTP/gRPC and NATS/Redis TLS are optional and min versions are TLS 1.2.

## Proposed
- Introduce a production mode env (e.g., CORDUM_ENV=production) that enforces TLS for HTTP/gRPC and safety kernel clients.
- Add TLS config for HTTP server and stricter min TLS version settings.
- Enforce TLS requirement for NATS/Redis when production mode is enabled.

## Acceptance
- Production mode refuses to start if TLS config is missing for external endpoints.
- TLS min version defaults to 1.3 in production mode.
- Docs updated with new env vars.

## References
- core/controlplane/gateway/gateway.go
- core/controlplane/scheduler/safety_client.go
- core/infra/bus/nats.go
- core/infra/redisutil/redisutil.go
- SECURITY.md
