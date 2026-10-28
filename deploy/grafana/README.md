# Cordum Overview: local evidence demo

Tags: grafana, prometheus, local-demo, approvals, observability.

The dashboard displays actual gateway requests, scheduler dispatches and blocks,
human approvals, and saga activity. Its panels use current exporter names.
Import `cordum-overview.json` and select your Prometheus data source. The
namespace selector's All value also matches exporters without a Kubernetes
namespace label. `pod` is the exporter's instance identity, not a service filter.

## Reproduce the harmless workload

This recipe is for Docker Desktop and a host-native disposable Core stack.
It publishes Grafana only at `127.0.0.1:33000` and Prometheus at
`127.0.0.1:39091`. It uses memory-backed data and a temporary random admin
password, with anonymous **Viewer** access to the local dashboard. Do not use
this fixture configuration for production.

For the governance workload, use `cordum-packs`' draft
[PR419](https://github.com/cordum-io/cordum-packs/pull/419), exact tested commit
`e79bf7877f4919bce1bdc69fe59110cdf3460075`. Follow its
[CrewAI tutorial](https://github.com/cordum-io/cordum-packs/blob/e79bf7877f4919bce1bdc69fe59110cdf3460075/integrations/agent-adapters/docs/crewai-governance.md).
That branch is stacked on unmerged
[PR418](https://github.com/cordum-io/cordum-packs/pull/418), exact foundation
`f769393432e371b90734007491a4fb6b2b8ddb4b`; it reuses its bridge signing
compatibility and denial-envelope fixes. The CrewAI branch adds a
`--port-offset 10000` option to the existing `examples/langgraph_fixture/stack.py`.
Run that fixture against a **clean** isolated Core checkout, then follow the
CrewAI tutorial's live test commands. No model provider is invoked. Tools run
only built-in local transform workflows; deny and approval flows still pass
through real Core policy and scheduler services.

The checked demo uses Core `ebc0dd6ad210a95518efb2d7293a24fb0c1c6080`, built
with Go 1.26.5. Gateway and scheduler metrics listen at host loopback ports
29092 and 29090. `prometheus.demo.yml` adds `namespace=local-demo` at scrape time;
it does not fabricate metric samples. Docker Desktop forwards
`host.docker.internal` to these host services. Native Linux networking and
Kubernetes discovery are not verified by this recipe.

From the Core repository root, in PowerShell:

```powershell
$env:GRAFANA_DEMO_PASSWORD = [guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N')
try {
  docker compose -p cordum-grafana-evidence -f deploy/grafana/demo.compose.yaml up -d
  # Wait for /api/health and both Core exporters, then run real CrewAI jobs.
  # Allow at least two scrapes before querying a new counter series.
  python deploy/grafana/validate_demo.py grafana-evidence.json
  # View http://127.0.0.1:33000/d/cordum-overview?from=now-5m&to=now
  # Keep an approval pending for at least 30s to see its queue snapshot.
  # A separate fixture operator key approves it; observe the original job.
  Read-Host 'Press Enter after inspecting the demo'
} finally {
  docker compose -p cordum-grafana-evidence -f deploy/grafana/demo.compose.yaml down --remove-orphans
  Remove-Item Env:\GRAFANA_DEMO_PASSWORD -ErrorAction SilentlyContinue
}
```

Stop the separate Core fixture with Ctrl+C too. Its cleanup removes its own
processes, NATS/Redis containers, and temporary credential files. Do not share
`runtime.json`, service logs or a resolved Compose configuration containing
credentials. The query evidence file contains only versions, queries and real
metric series; review instance identifiers before sharing it externally.

`validate_demo.py` imports the shipped dashboard through Grafana's import API,
checks that the data source was resolved, requires two healthy real exporters,
and runs every query through **both** Prometheus and Grafana. It records empty
series explicitly. Grafana 13.3.0 and Prometheus 3.15.0 are digest-pinned in
Compose. Grafana automatic plugin installation is disabled so the tested
bundled Prometheus plugin is retained.

## What the charts mean

- A scheduler dispatch is an execution dispatch, including approved jobs released
  later. It is not a count of every Safety Kernel allow decision.
- Gateway submit-time denials can return before scheduler publication. The
  scheduler denial counter does not include them. Verify these denials with
  stored job state, zero attempts, and no workflow side effect in the live tests.
- Approval queue gauges are snapshots of a shared Redis queue. Gateway and
  scheduler may both report it and snapshots can lag. Do not sum reporters or
  add `risk_tier=all` to per-tier values.
- Approval latency is observed on resolution, with a valid request timestamp.
  Pending requests produce no resolution-latency samples. The current exporter
  uses finite histogram buckets ending at 10 seconds; longer human waits land
  in `+Inf`, so the displayed P95 may read 10 seconds even for a 45-second wait.
  Use job timestamps for actual wait duration; this chart is not exact timing.
- Unwired workflow-run counters have been removed from this dashboard. Saga
  counters remain because scheduler saga handling actually emits them.
- Empty 5xx, failure, quarantine, stale-job or DLQ series can be expected when
  the harmless scenario has not created such events. Empty data is not a
  measured zero, and this demo does not trigger dangerous failures to fill charts.

This is development CAP compatibility mode over loopback. It does not validate
production signing, private CA wiring, deployment security, worker attestation,
high availability, scale, costs or production performance. The PR418 bridge
does not support production signing/private-CA configuration. The LangGraph
changed-input guard is application logic, not proof of a Core-bound token.
Cordum Core is BUSL source-available; CAP is Apache-2.0.

## Checks

```powershell
python -m unittest discover -s deploy/grafana -p 'test_*.py' -v
go test ./core/infra/metrics/...
docker exec cordum-grafana-evidence-prometheus-1 promtool check config /etc/prometheus/prometheus.yml
```

The configuration tests run in `grafana-dashboard.yml`. They do not claim to
replace the opt-in live fixture, UI inspection, or query validation above.
