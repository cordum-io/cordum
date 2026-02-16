<p align="center">
  <img src="https://cordum.io/_next/image?url=%2Flogo.png&w=1200&q=75" alt="Cordum" width="200"/>
</p>

<h1 align="center">Cordum</h1>

<p align="center">
  <strong>AI Agent Governance Control Plan</strong><br/>
  Deploy autonomous agents with built-in safety, observability, and control.
</p>

<p align="center">
  <a href="https://github.com/cordum-io/cordum/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BUSL--1.1-blue" alt="License"/></a>
  <a href="https://github.com/cordum-io/cordum/releases"><img src="https://img.shields.io/github/v/release/cordum-io/cordum?sort=semver" alt="Release"/></a>
  <a href="https://discord.gg/U4NpXtjP"><img src="https://img.shields.io/badge/discord-join-5865F2?logo=discord&logoColor=white" alt="Discord"/></a>
  <a href="https://github.com/cordum-io/cap"><img src="https://img.shields.io/badge/protocol-CAP%20v2-green" alt="CAP Protocol"/></a>
</p>

---

## The Problem

AI agents are powerful. They're also unpredictable.

Teams deploying agents in production face the **Trust Gap**: the distance between what an agent *can* do and what you're *confident* letting it do unsupervised.

Without governance, you're flying blind:
- No visibility into what agents are doing
- No safety rails before dangerous actions
- No audit trail when things go wrong
- No way to require human approval for sensitive operations

## The Solution

Cordum is a **control plane for AI agents** that closes the Trust Gap.

```
┌─────────────────────────────────────────────────────────────────┐
│                         Cordum                                  │
│                                                                 │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────────┐ │
│  │   API    │──▶│ Scheduler│──▶│  Safety  │──▶│ Worker Pools │ │
│  │ Gateway  │   │          │   │  Kernel  │   │              │ │
│  └──────────┘   └──────────┘   └──────────┘   └──────────────┘ │
│       │              │              │                │         │
│       ▼              ▼              ▼                ▼         │
│  [Dashboard]    [Workflows]    [Policies]      [Your Agents]   │
└─────────────────────────────────────────────────────────────────┘
```

**What Cordum does:**

- **Safety Kernel** — Policy checks (allow/deny/throttle/human-approve) before any job runs
- **Workflow Engine** — Orchestrate multi-step agent workflows with retries, approvals, and timeouts
- **Job Routing** — Distribute work across agent pools with capability-based routing
- **Observability** — Full audit trail, traces, and real-time dashboard
- **Human-in-the-Loop** — Require approval for sensitive operations

## Quickstart

**Prerequisites:** Docker, Docker Compose, Go 1.24+

```bash
# Clone the repo
git clone https://github.com/cordum-io/cordum.git
cd cordum

# Set an API key
export CORDUM_API_KEY="$(openssl rand -hex 32)"

# Start everything (auto-generates TLS certs on first run)
go run ./cmd/cordumctl up

# Open dashboard
open http://localhost:8082
```

Or use the quickstart script:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
./tools/scripts/quickstart.sh
```

That's it. You have a running Cordum instance with API, scheduler, safety kernel, dashboard, and TLS enabled by default. System configuration is auto-bootstrapped on first startup.

## How It Works

Cordum uses [CAP (Cordum Agent Protocol)](https://github.com/cordum-io/cap) for all agent communication:

1. **Submit** — Client submits a job via API
2. **Safety Check** — Scheduler asks Safety Kernel: allow, deny, throttle, or require approval?
3. **Dispatch** — Approved jobs route to the right worker pool via NATS
4. **Execute** — Your agent runs the job (using MCP, LangChain, whatever)
5. **Result** — Agent returns result; Cordum updates state and notifies client

```
Client ──▶ API ──▶ Scheduler ──▶ Safety Kernel ──▶ NATS ──▶ Agent Pool
                       │                              │
                       ▼                              ▼
                  [Redis State]                 [Your Agents]
```

**Key design choices:**
- **Payloads stay off the bus** — `context_ptr` and `result_ptr` reference Redis/S3, keeping the message bus lean
- **Protocol-first** — CAP is an independent spec; Cordum is the reference implementation
- **Workers are external** — Cordum is the control plane; your agents run wherever you want

## Key Features

| Feature | Description |
|---------|-------------|
| **Safety Policies** | Define rules for what agents can/can't do. Enforce before execution. |
| **Output Safety** | Evaluate completed job outputs and allow, redact, or quarantine unsafe results. |
| **Human Approval** | Flag sensitive jobs for manual review before they run. |
| **Workflows** | Multi-step DAGs with fan-out, retries, delays, and conditions. |
| **Pool Routing** | Route jobs by capability, region, or custom tags. |
| **Heartbeats** | Know which agents are alive, their capacity, and load. |
| **Audit Trail** | Every job, decision, and result logged and queryable. |
| **Dashboard** | Real-time UI for workflows, jobs, approvals, and policies. |
| **Multi-tenant** | API keys with RBAC for teams and environments. |

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

## Protocol: CAP

Cordum implements [CAP (Cordum Agent Protocol)](https://github.com/cordum-io/cap) — an open protocol for distributed AI agent orchestration.

**CAP vs MCP:**
- **MCP** = tool-calling protocol for a single model
- **CAP** = job protocol for distributed agent clusters

They're complementary. Use CAP for orchestration, MCP inside your agents for tools.

Read more: [MCP vs CAP: Why Your AI Agents Need Both Protocols](https://dev.to/yaron_torgeman_104570d968/-mcp-vs-cap-why-your-ai-agents-need-both-protocols-3g4l)

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

## Community

- **Discord:** [Join the conversation](https://discord.gg/HGGHbU26)
- **GitHub Discussions:** [Ask questions](https://github.com/cordum-io/cordum/discussions)
- **Twitter/X:** [@coraboratedai](https://x.com/Cordum_io)
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

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Licensed under [Business Source License 1.1 (BUSL-1.1)](LICENSE). 

Free for self-hosted and internal use. Not permitted for competing hosted/managed offerings. See LICENSE for details and Change Date.

---

<p align="center">
  <strong>Ready to govern your AI agents?</strong><br/>
  <a href="https://cordum.io">cordum.io</a> · <a href="https://github.com/cordum-io/cap">CAP Protocol</a> · <a href="https://discord.gg/U4NpXtjP">Discord</a>
</p>

<p align="center">
  ⭐ Star this repo if Cordum helps you deploy agents safely
</p>
