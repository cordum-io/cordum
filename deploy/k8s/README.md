# coretexOS K8s Starter (sample)

Minimal manifests to run the control plane + workers in a single namespace. This is a starter, not production-hardened.

## Components
- NATS (bus)
- Redis (ctx/res + JobStore)
- Safety kernel
- Scheduler
- API gateway (HTTP :8081, gRPC :8080, Prom :9092)
- Context engine (chat/RAG memory)
- Workers: echo, chat, chat-advanced, code-llm, orchestrator, planner (repo pipeline not included in this starter)
- ConfigMaps: pools.yaml, timeouts.yaml (add safety.yaml if you need custom policy)

## Apply
```bash
kubectl create namespace coretex
kubectl apply -n coretex -f deploy/k8s/base.yaml
kubectl apply -n coretex -f deploy/k8s/dashboard.yaml
# Optional (requires an Ingress controller):
kubectl apply -n coretex -f deploy/k8s/ingress.yaml
```

## Notes
- Images assume `coretex-<service>` tags; adjust `image:` as needed (e.g., from your registry).
- API key is stored in the `coretex-api-key` Secret (key: `API_KEY`). Default in this starter is `super-secret-key` (change it for real deployments).
- The gateway reads the API key from `CORETEX_SUPER_SECRET_API_TOKEN`, `CORETEX_API_KEY`, or `API_KEY`, wired from the same Secret.
- The dashboard reads the API key from `CORETEX_DASHBOARD_API_KEY`, wired from the same Secret.
- Probes are basic HTTP/TCP; adjust for your environment.
- Example HPA for chat worker targets CPU.
- Dashboard UI is in `web/dashboard/`; `deploy/k8s/dashboard.yaml` runs it as a Deployment/Service.
- If you use an Ingress, `deploy/k8s/ingress.yaml` routes `/api/v1/*` and `/health` to `coretex-api-gateway`, and routes `/` to `coretex-dashboard`.
