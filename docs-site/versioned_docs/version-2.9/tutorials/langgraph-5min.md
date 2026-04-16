---
sidebar_position: 2
title: "Govern Your LangGraph Agent in 5 Minutes"
description: "Add safety policies, PII detection, and audit trails to a LangGraph agent using Cordum."
---

# Govern Your LangGraph Agent in 5 Minutes

> **Requires:** Cordum v2.9+ · Docker Compose v2+ · Python 3.10+

Your LangGraph agent calls external APIs, processes user queries, and makes decisions autonomously. How do you prevent it from accessing unauthorized data, leaking PII, or running unchecked in production?

Cordum sits between job submission and your agent — every request passes through the Safety Kernel before reaching your code. Denied requests never arrive. No SDK changes needed in your agent logic.

## What you'll build

A LangGraph research agent governed by Cordum:

- **Safety policy** blocks queries containing PII (SSN, credit card numbers)
- **Allow rules** permit research queries on approved topics
- **Audit trail** logs every decision in the dashboard

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose v2+
- Python 3.10 or later
- `openssl` (for API key generation)

## Step 1: Initialize the project

```bash
# Install cordumctl (or use a pre-built binary)
go install github.com/cordum/cordum/cmd/cordumctl@latest

# Scaffold a LangGraph project with Cordum governance
cordumctl init --framework langchain my-agent
cd my-agent
```

This generates:

```
my-agent/
├── config/
│   ├── pools.yaml          # Worker pool configuration
│   ├── safety.yaml         # Safety policy (deny-default + rules)
│   └── timeouts.yaml       # Job timeout settings
├── worker/
│   ├── agent.py            # LangGraph agent with Cordum integration
│   ├── requirements.txt    # Python dependencies
│   └── Dockerfile          # Container image for the worker
├── workflows/
│   └── hello.json          # Sample workflow definition
├── docker-compose.yml      # Full Cordum stack + your worker
└── README.md
```

## Step 2: Review the safety policy

Open `config/safety.yaml`. The scaffold generates a deny-default policy:

```yaml
default_decision: deny
output_policy:
  enabled: true
  fail_mode: closed
default_tenant: default
tenants:
  default:
    allow_topics:
      - "job.default"
    deny_topics:
      - "sys.*"
rules:
  # Allow research queries
  - name: allow-research
    match:
      topics: ["job.default"]
    decision: allow

input_rules:
  - id: deny-pii-queries
    severity: high
    match:
      topics: ["job.default"]
      scanners: ["pii"]
    decision: deny
    reason: "Query contains PII (SSN or credit card number)"
```

Key points:
- **`default_decision: deny`** — unmatched jobs are rejected. Fail-closed.
- **`allow-research`** — permits jobs on the `job.default` topic.
- **`deny-pii-queries`** — blocks any job whose prompt contains SSN or credit card patterns. This rule is evaluated *before* your agent sees the request.

## Step 3: Review the agent code

Open `worker/agent.py`. The key integration is the `cap.runtime.Agent` class:

```python
from cap.runtime import Agent, Context

agent = Agent(
    sender_id=os.getenv("CORDUM_WORKER_ID", "langchain-worker"),
    pool=os.getenv("CORDUM_POOL", "default"),
)

@agent.job("job.default")
async def handle_research(ctx: Context, input: Any) -> dict:
    """Jobs that reach this handler have already passed the Safety Kernel."""
    query = input.get("query", "") if isinstance(input, dict) else str(input)

    # Your LangGraph agent runs here — only on approved requests.
    result = app.invoke({"query": query})
    return {
        "answer": result.get("result", ""),
        "sources": result.get("sources", []),
    }

if __name__ == "__main__":
    asyncio.run(agent.run())
```

Your agent code doesn't need to know about safety policies. Cordum handles enforcement at the platform layer.

## Step 4: Start the stack

```bash
# Generate an API key
export CORDUM_API_KEY="$(openssl rand -hex 32)"

# Start Cordum + your LangGraph worker
docker compose up -d

# Wait for services to be healthy
docker compose ps
```

You should see all services running: NATS, Redis, Safety Kernel, Scheduler, API Gateway, Workflow Engine, Dashboard, and your LangGraph worker.

## Step 5: Submit a job

Send a research query through Cordum:

```bash
# Submit a permitted research query
curl -s http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "job.default",
    "prompt": "What are the key features of LangGraph?"
  }'
```

Expected response — job accepted and dispatched:

```json
{
  "job_id": "job-abc123",
  "status": "SUBMITTED",
  "topic": "job.default"
}
```

Now try a request that contains PII:

```bash
# Submit a query containing a Social Security Number
curl -s http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "job.default",
    "prompt": "Look up records for SSN 123-45-6789"
  }'
```

Expected response — blocked by the Safety Kernel:

```json
{
  "error": "job denied by safety policy",
  "reason": "Query contains PII (SSN or credit card number)",
  "rule": "deny-pii-queries"
}
```

The request never reached your agent. Cordum blocked it at the Safety Kernel.

## Step 6: View the audit trail

Open the Cordum dashboard at [http://localhost:8082](http://localhost:8082).

Navigate to **Jobs** to see:
- The approved research query with status `SUCCEEDED`
- The denied PII query with status `DENIED` and the reason from your safety policy

Every decision is logged with the rule that matched, the tenant, and a timestamp. This audit trail is what compliance teams need.

## What just happened?

```
User submits job → API Gateway → Safety Kernel evaluates policy
                                       │
                              ┌────────┴────────┐
                              │                  │
                           ALLOWED            DENIED
                              │                  │
                     Scheduler dispatches    Response returned
                     to LangGraph worker     with deny reason
                              │
                     Agent processes query
                              │
                     Result returned to user
```

1. The API Gateway received your job and forwarded it to the Safety Kernel.
2. The Safety Kernel evaluated the job against `config/safety.yaml`.
3. For the research query: the `allow-research` rule matched — job dispatched to your LangGraph worker.
4. For the PII query: the `deny-pii-queries` rule matched first — job rejected immediately.
5. Your agent code only ran for the approved request.

## Next steps

- **Add more rules** — require approval for specific topics, rate-limit by worker, or match on job metadata labels
- **Connect a real LLM** — replace the example nodes in `agent.py` with actual LangChain LLM calls
- **Deploy to production** — see the [Deployment Guide](/operations/deployment) for Kubernetes setup
- **Add workflows** — chain multiple governed steps with the [Workflow Engine](/concepts/workflow-step-types)
