<p align="center">
  <img src="https://cordum.io/_next/image?url=%2Flogo.png&w=1200&q=75" alt="Cordum" width="200"/>
</p>

<h1 align="center">Cordum</h1>

<p align="center">
  <a href="https://artifacthub.io/packages/helm/cordum/cordum"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/cordum" alt="Artifact Hub" /></a>
</p>

<p align="center">
  <strong>Know What Your AI Agents Are Doing. Before They Do It.</strong><br/>
  The Source-Available <strong>Agent Control Plane</strong> for Governance, Safety, and Trust.
</p>

<p align="center">
  <a href="https://github.com/cordum-io/cordum/stargazers"><img src="https://img.shields.io/github/stars/cordum-io/cordum?style=social" alt="Stars"/></a>
  <a href="https://github.com/cordum-io/cordum/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BUSL--1.1-blue" alt="License"/></a>
  <a href="https://github.com/cordum-io/cordum/releases"><img src="https://img.shields.io/github/v/release/cordum-io/cordum?sort=semver" alt="Release"/></a>
  <a href="https://github.com/cordum-io/cordum/actions/workflows/ci.yml"><img src="https://github.com/cordum-io/cordum/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="https://goreportcard.com/report/github.com/cordum-io/cordum"><img src="https://goreportcard.com/badge/github.com/cordum-io/cordum" alt="Go Report Card"/></a>
  <a href="https://discord.gg/nvHzPCcWWt"><img src="https://img.shields.io/badge/discord-join-5865F2?logo=discord&logoColor=white" alt="Discord"/></a>
  <a href="https://github.com/cordum-io/cap"><img src="https://img.shields.io/badge/protocol-CAP%20v2-green" alt="CAP Protocol"/></a>
</p>

<p align="center">
  <a href="https://cordum.io">Website</a> · <a href="https://cordum.io/docs">Docs</a> · <a href="https://cordum.io/blog">Blog</a> · <a href="https://discord.gg/nvHzPCcWWt">Discord</a> · <a href="https://github.com/cordum-io/cordum/discussions">Discussions</a>
</p>

---

## The Problem: The Agent Risk Gap

Enterprises are rushing to deploy **Autonomous AI Agents**, but they're hitting a wall of risk. According to Gartner, **74% of enterprises see AI agents as a new attack vector**, and over 40% of agentic AI projects will be canceled due to inadequate risk controls.

The current landscape leaves teams with a choice:
1. **Restrict agents** to simple, low-value read-only tasks.
2. **Accept the risk** of autonomous agents taking destructive, unmonitored actions.

Without a dedicated governance layer, you're flying blind:
- **No visibility**: You don't know what your agents are doing until *after* they do it.
- **No safety rails**: There's no way to intercept dangerous operations before they execute.
- **No human-in-the-loop**: Sensitive actions happen without manual oversight.
- **No audit trail**: When things go wrong, you can't reconstruct the chain of thought.

## The Solution: Cordum Agent Control Plane

Cordum is an **Agent Control Plane** that provides a deterministic governance layer for probabilistic AI minds. It allows you to define, enforce, and audit the behavior of your **Autonomous AI Agents** across any framework or model.

```mermaid
graph TB
    subgraph CP [AGENT CONTROL PLANE]
        direction LR
        G[API Gateway] --- S[Scheduler] --- SK[Safety Kernel]
        S --- WE[Workflow Engine]
    end
    
    subgraph AGENTS [AUTONOMOUS AGENT POOLS]
        direction LR
        A1[Financial Ops]
        A2[Data Science]
        A3[Customer Service]
    end
    
    CP -->|Governed Jobs| AGENTS
    AGENTS -->|Audit Trail| CP
```

<!-- Replace with a high-impact GIF showing a risky agent action being caught by Cordum -->
![nWwQVRVqwlZKeRbBZvkSof-img-2_1771930624000_na1fn_ZGFzaGJvYXJkLXByZXZpZXctZGFyaw](https://github.com/user-attachments/assets/6d8d4781-dff4-4d62-8e26-7ff0366b2a5d)



### Governance Across the Lifecycle

Cordum's **Before/During/Across** framework provides exhaustive control over your agent operations:

```mermaid
graph LR
    subgraph BEFORE [1. BEFORE - Governance]
        P[Policy Evaluation] --> S[Safety Gating]
        S --> H[Human Approval]
    end
    subgraph DURING [2. DURING - Safety]
        M[Real-time Monitoring] --> C[Circuit Breakers]
        C --> A[Live Approvals]
    end
    subgraph ACROSS [3. ACROSS - Observability]
        F[Fleet Health] --> T[Audit Trail]
        T --> O[Optimization]
    end
    BEFORE --> DURING
    DURING --> ACROSS
```

- **BEFORE (Governance)**: Define declarative policies that evaluate job requests *before* an agent executes. Trigger safety kernel checks, throttle risky actions, or flag operations for human approval.
- **DURING (Safety)**: Real-time visibility into active agent runs. Monitor progress, handle step-level approvals, and enforce timeouts or circuit breakers on the fly.
- **ACROSS (Observability)**: Manage your entire fleet from a single control plane. Aggregate audit trails, track capability-based routing, and observe agent pool health in real-time.

## Quickstart

**Prerequisites:** Docker (4GB+ RAM), Docker Compose, Go 1.24+

```bash
git clone https://github.com/cordum-io/cordum.git
cd cordum
./tools/scripts/quickstart.sh
```

That's it. The script auto-creates `.env`, generates API keys and Redis password, builds all services, and runs health checks. No manual configuration needed.

**Dashboard:** http://localhost:8082
**Login:** `admin` / `admin123` (change in `.env` → `CORDUM_ADMIN_PASSWORD`)

<details>
<summary>Manual setup (without quickstart script)</summary>

```bash
cp .env.example .env
# Edit .env: set CORDUM_API_KEY (or generate: openssl rand -hex 32)
export CORDUM_API_KEY="your-key-here"
go run ./cmd/cordumctl up
open http://localhost:8082
```
</details>

### Deploy to Kubernetes

```bash
helm install cordum oci://ghcr.io/cordum-io/cordum/charts/cordum \
  --namespace cordum --create-namespace \
  --set secrets.apiKey=$(openssl rand -hex 32) \
  --set redis.auth.password=$(openssl rand -hex 32) \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.api.host=api.cordum.example.com \
  --set ingress.dashboard.host=cordum.example.com
```

See [cordum-helm/](cordum-helm/) for the full Helm chart reference. Chart also available on [Artifact Hub](https://artifacthub.io/packages/helm/cordum/cordum).

**Container images** (multi-arch: amd64 + arm64):

| Image | Registry |
|-------|----------|
| `cordum/api-gateway` | [Docker Hub](https://hub.docker.com/r/cordum/api-gateway) |
| `cordum/scheduler` | [Docker Hub](https://hub.docker.com/r/cordum/scheduler) |
| `cordum/safety-kernel` | [Docker Hub](https://hub.docker.com/r/cordum/safety-kernel) |
| `cordum/workflow-engine` | [Docker Hub](https://hub.docker.com/r/cordum/workflow-engine) |
| `cordum/context-engine` | [Docker Hub](https://hub.docker.com/r/cordum/context-engine) |
| `cordum/dashboard` | [Docker Hub](https://hub.docker.com/r/cordum/dashboard) |

Also available on GHCR: `ghcr.io/cordum-io/cordum/{service}:{version}`

### Ports

| Port | Service |
|------|---------|
| 8082 | Dashboard |
| 8081 | API Gateway (HTTPS) |
| 9080 | gRPC Gateway |
| 4222 | NATS |
| 6379 | Redis |
| 9092 | Gateway Metrics |
| 9093 | Workflow Engine Health |
| 50051 | Safety Kernel (gRPC) |
| 50400 | Context Engine (gRPC) |

### After Setup

```bash
# Submit a test job
curl -sS --cacert ./certs/ca/ca.crt \
  -X POST https://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{"topic":"job.default","context":{"prompt":"hello"}}'

# Stop the stack
docker compose down

# View logs
docker compose logs -f api-gateway
```

### Troubleshooting

| Issue | Fix |
|-------|-----|
| Port already in use | `docker compose down` then retry, or check `lsof -i :8082` |
| Docker out of memory | Allocate at least 4 GB RAM to Docker Desktop |
| Can't login to dashboard | Default credentials: admin / admin123 |
| TLS/SSL cert errors | Remove `./certs/` and re-run — certs auto-regenerate |
| `openssl` not found | Not needed — quickstart.sh auto-generates keys without it |
| Go build fails | Requires Go 1.24+ — check with `go version` |
| Stale config after changes | `redis-cli DEL cfg:system:default` then restart |

For detailed troubleshooting, see [docs/troubleshooting.md](docs/troubleshooting.md).

## Key Features

<!-- Replace with a visual showing the Policy Studio and Safety Kernel in action -->

![nWwQVRVqwlZKeRbBZvkSof-img-4_1771930611000_na1fn_d29ya2Zsb3ctdmlzdWFsaXphdGlvbg](https://github.com/user-attachments/assets/ee44853d-1e89-463b-bf3c-0ba0481eee68)

| Governance Feature | Why It Matters for Enterprise |
|--------------------|--------------------------------|
| **Safety Gating** | Prevents agents from executing destructive or unauthorized actions *before* they occur. |
| **Output Quarantine** | Automatically blocks PII leaks, secrets, or hallucinated results from reaching the client. |
| **Human-in-the-Loop** | Mandates human oversight for high-risk operations (e.g., financial transfers, prod access). |
| **Pool Segmentation** | Ensures sensitive data only reaches agents in trusted environments. |
| **Deterministic Audit** | Prove exactly *why* a decision was made with a full chain-of-thought audit trail. |
| **Governance Policies** | Declarative YAML-based rules that map enterprise risk to agent behavior. |
| **Policy Simulator** | Test your governance rules against historical data before rolling them out to production. |

## Architecture

```
cordum/
├── cmd/                          # Service entrypoints + CLI
│   ├── cordum-api-gateway/       # API gateway (HTTP/WS + gRPC)
│   ├── cordum-scheduler/         # Scheduler + safety gating
│   ├── cordum-safety-kernel/     # Policy evaluation
│   ├── cordum-workflow-engine/   # Workflow orchestration
│   ├── cordum-context-engine/    # Optional context/memory service
│   └── cordumctl/                # CLI
├── core/                         # Core libraries
│   ├── controlplane/             # Gateway, scheduler, safety kernel
│   ├── context/                  # Context engine implementation
│   ├── infra/                    # Config, storage, bus, metrics
│   ├── protocol/                 # API protos + CAP aliases
│   └── workflow/                 # Workflow engine
├── dashboard/                    # React UI
├── sdk/                          # SDK + worker runtime
├── cordum-helm/                  # Helm chart
├── deploy/k8s/                   # Kubernetes manifests
└── docs/                         # Documentation
```

## Documentation

| Doc | Description |
|-----|-------------|
| [System Overview](docs/system_overview.md) | Architecture and data flow |
| [Core Reference](docs/CORE.md) | Deep technical details |
| [Docker Guide](docs/DOCKER.md) | Running with Compose |
| [Agent Protocol](docs/AGENT_PROTOCOL.md) | CAP bus + pointer semantics |
| [MCP Server](docs/mcp-server.md) | MCP stdio + HTTP/SSE integration |
| [Pack Format](docs/pack.md) | How to package agent capabilities |
| [Local E2E](docs/LOCAL_E2E.md) | Full local walkthrough |
| [Production Guide](docs/production.md) | TLS, HA, backups, incident runbooks |

## Protocol: CAP — The Open Standard for Agent Governance

Cordum implements [CAP (Cordum Agent Protocol)](https://github.com/cordum-io/cap), an open protocol specifically designed for distributed AI agent governance. CAP provides a unified interface for defining agent capabilities, submitting jobs, and enforcing safety policies across heterogeneous agent pools.

### CAP vs. MCP: Why You Need Both

While both are essential, they solve different parts of the agent stack:

| Protocol | Focus | Level | Responsibility |
|----------|-------|-------|----------------|
| **MCP** (Model Context Protocol) | **Tool Calling** | Local | How a model interacts with a tool. |
| **CAP** (Cordum Agent Protocol) | **Governance** | Network | How an agent is governed within an enterprise. |

- **MCP** is for *within* the agent — it defines how a model calls local tools.
- **CAP** is for *above* the agent — it defines the governance control plane for the entire agent fleet.

Use CAP for high-level orchestration and safety gating, and MCP inside your agents for fine-grained tool integration.

[Read the full deep dive: MCP vs CAP: Why Your AI Agents Need Both Protocols](https://dev.to/yaron_torgeman_104570d968/-mcp-vs-cap-why-your-ai-agents-need-both-protocols-3g4l)

## MCP Server

Cordum includes an MCP server framework with:

- **Standalone stdio mode** via `cmd/cordum-mcp` (for Claude Desktop/Code local integration)
- **Gateway HTTP/SSE mode** via `/mcp/message` and `/mcp/sse` (when `mcp.enabled=true`)

See [docs/mcp-server.md](docs/mcp-server.md) for setup, auth headers, and client configuration examples.

## SDK

The Go SDK makes it easy to build CAP-compatible workers:

```go
import (
    "log"

    "github.com/cordum/cordum/sdk/runtime"
)

type Input struct {
    Prompt string `json:"prompt"`
}

type Output struct {
    Summary string `json:"summary"`
}

func main() {
    agent := &runtime.Agent{Retries: 2}

    runtime.Register(agent, "job.summarize", func(ctx runtime.Context, input Input) (Output, error) {
        // Your agent logic here
        return Output{Summary: input.Prompt}, nil
    })

    if err := agent.Start(); err != nil {
        log.Fatal(err)
    }
    select {}
}
```

SDKs: **Go** (stable) | [**Python**](https://github.com/cordum-io/cap) | [**Node**](https://github.com/cordum-io/cap)

## Integration Packs

Extend Cordum with [30+ integration packs](https://github.com/cordum-io/cordum-packs) for Slack, GitHub, AWS, Jira, Terraform, Datadog, PagerDuty, and more. Each pack is a CAP-native worker with policy-gated workflows.

| Pack | Category | Description |
|------|----------|-------------|
| [Slack](https://cordum.io/integrations/slack) | Communication | Approval notifications and agent alerts |
| [GitHub](https://cordum.io/integrations/github) | DevOps | Govern agent actions on repositories |
| [AWS](https://cordum.io/integrations/aws) | Cloud | Policy-gated cloud operations |
| [Kubernetes](https://cordum.io/integrations/kubernetes-triage) | DevOps | Governed incident remediation |
| [Terraform](https://cordum.io/integrations/terraform) | DevOps | Pre-apply governance for IaC |
| [Datadog](https://cordum.io/integrations/datadog) | Monitoring | Alert-triggered governed workflows |
| [LangChain](https://cordum.io/integrations/langchain) | AI Framework | Governance for LangChain tool calls |
| [MCP Bridge](https://cordum.io/integrations/mcp-bridge) | AI Framework | Gateway governance for MCP tools |

[Browse all integrations →](https://cordum.io/integrations)

## Community

- **Discord:** [Join the conversation](https://discord.gg/nvHzPCcWWt)
- **GitHub Discussions:** [Ask questions](https://github.com/cordum-io/cordum/discussions)
- **Twitter/X:** [@Cordum_io](https://x.com/Cordum_io)
- **Email:** [admin@cordum.io](mailto:admin@cordum.io)

## Enterprise

Cordum Enterprise adds:
- SSO/SAML integration
- Advanced RBAC
- SIEM export
- Priority support

[Contact us](mailto:admin@cordum.io) for pricing.

## Governance

Cordum follows a transparent governance model with a protocol stability pledge, maintainer structure, and clear decision-making process. See [GOVERNANCE.md](GOVERNANCE.md) for details including:

- **Protocol Stability**: CAP v2 wire format frozen until February 2027
- **Security**: [SECURITY.md](SECURITY.md) for vulnerability reporting
- **Versioning**: Semantic versioning with deprecation policy

## Roadmap

See [ROADMAP.md](ROADMAP.md) for the full feature roadmap, completed milestones, and planned work.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for a detailed log of all changes by version.

## Compared To

| Feature | Cordum | Guardrails AI | NeMo Guardrails | Custom Middleware |
|---------|--------|--------------|-----------------|-------------------|
| Pre-execution policy engine | ✅ Safety Kernel | ❌ Post-generation | ⚠️ Dialog rails only | ⚠️ Manual |
| Human-in-the-loop approvals | ✅ Built-in | ❌ | ❌ | ⚠️ DIY |
| Multi-agent fleet governance | ✅ | ❌ Single model | ❌ Single model | ❌ |
| Deterministic audit trail | ✅ | ❌ | ❌ | ⚠️ Manual |
| Framework agnostic | ✅ Any via CAP | ❌ Python only | ❌ NVIDIA stack | ❌ |
| MCP governance | ✅ Bridge + Gateway | ❌ | ❌ | ❌ |

[See detailed comparisons →](https://cordum.io/compare/ai-governance-platforms)

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines. Check out our [good first issues](https://github.com/cordum-io/cordum/labels/good%20first%20issue) to get started.

## License

Licensed under [Business Source License 1.1 (BUSL-1.1)](LICENSE).

- **Self-host and use internally**: Permitted
- **Modify and contribute back**: Permitted
- **Offer as a competing hosted service**: Not permitted
- **Change Date**: January 1, 2029 — automatically converts to Apache License 2.0

See [LICENSE](LICENSE) for full terms.

---

## Star History

<a href="https://star-history.com/#cordum-io/cordum&Date">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=cordum-io/cordum&type=Date&theme=dark" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=cordum-io/cordum&type=Date" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=cordum-io/cordum&type=Date" />
 </picture>
</a>

---

<p align="center">
  <strong>Ready to govern your AI agents?</strong><br/>
  <a href="https://cordum.io">cordum.io</a> · <a href="https://github.com/cordum-io/cap">CAP Protocol</a> · <a href="https://cordum.io/integrations">Integrations</a> · <a href="https://discord.gg/nvHzPCcWWt">Discord</a>
</p>

<p align="center">
  If Cordum helps you deploy agents safely, <a href="https://github.com/cordum-io/cordum/stargazers">give it a ⭐</a>
</p>
