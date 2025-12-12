# coretexOS K8s Starter (sample)

Minimal manifests to run the control plane + workers in a single namespace. This is a starter, not production-hardened.

## Components
- NATS (bus)
- Redis (ctx/res + JobStore)
- Safety kernel
- Scheduler
- API gateway (HTTP :8081, gRPC :8080, Prom :9092)
- Workers: echo, chat, chat-advanced, code-llm, orchestrator, planner (repo pipeline not included in this starter)
- ConfigMaps: pools.yaml, timeouts.yaml (add safety.yaml if you need custom policy)

## Apply
```bash
kubectl create namespace coretex
kubectl apply -n coretex -f deploy/k8s/base.yaml
```

## Notes
- Images assume `coretex-<service>` tags; adjust `image:` as needed (e.g., from your registry).
- API key is injected via `API_KEY`; planner is off by default (`USE_PLANNER=false`).
- Probes are basic HTTP/TCP; adjust for your environment.
- Example HPA for chat worker targets CPU.
