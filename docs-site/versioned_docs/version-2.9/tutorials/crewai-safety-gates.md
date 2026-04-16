---
sidebar_position: 3
title: "Add Safety Gates to CrewAI"
description: "Enforce PII redaction, approval workflows, and content scanning on CrewAI crews using Cordum."
---

# Add Safety Gates to CrewAI

> **Requires:** Cordum v2.9+ · Docker Compose v2+ · Python 3.10+

Your CrewAI crew processes customer data, generates reports, and takes actions on behalf of users. How do you enforce PII redaction before data reaches your agents? How do you require human approval for sensitive operations like deleting production records?

Cordum adds safety gates to your crew without changing your agent code. Every job passes through the Safety Kernel, which enforces content scanning, PII detection, and approval workflows — all defined in a YAML policy file.

## What you'll build

A CrewAI crew with two safety gates:

- **PII gate** — blocks jobs whose prompts contain Social Security Numbers or credit card numbers
- **Approval gate** — requires human sign-off for tasks involving sensitive operations (production deletes, admin access)

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose v2+
- Python 3.10 or later
- `openssl` (for API key generation)

## Step 1: Initialize the project

```bash
cordumctl init --framework crewai my-crew
cd my-crew
```

This generates a project with a CrewAI worker, safety policy, and full Cordum stack.

## Step 2: Understand the safety policy

Open `config/safety.yaml`. The scaffold includes three rules:

```yaml
default_decision: deny
rules:
  # Allow standard crew tasks
  - name: allow-crew-tasks
    match:
      topics: ["job.default"]
    decision: allow

input_rules:
  - id: deny-pii-input
    severity: high
    match:
      topics: ["job.default"]
      scanners: ["pii"]
    decision: deny
    reason: "Input contains PII — redact before submitting"
  - id: require-approval-sensitive
    severity: high
    match:
      topics: ["job.default"]
      keywords: ["delete production", "drop table", "admin access"]
    decision: require_approval
    reason: "Task involves sensitive operations — human approval required"
```

Three decisions, three outcomes:
- **`allow`** — job proceeds directly to your crew
- **`deny`** — job rejected immediately, reason returned to caller
- **`require_approval`** — job paused in an approval queue until a human approves or rejects it

## Step 3: Review the crew code

Open `worker/crew.py`. The Cordum integration uses the same `cap.runtime.Agent` pattern:

```python
from cap.runtime import Agent, Context

agent = Agent(
    sender_id=os.getenv("CORDUM_WORKER_ID", "crewai-worker"),
    pool=os.getenv("CORDUM_POOL", "default"),
)

@agent.job("job.default")
async def handle_crew_task(ctx: Context, input: Any) -> dict:
    """Only jobs that pass the Safety Kernel reach this handler."""
    prompt = input.get("prompt", "") if isinstance(input, dict) else str(input)

    # Build and run your CrewAI crew here.
    research_task = Task(
        description=f"Research the following topic: {prompt}",
        expected_output="A detailed factual summary",
        agent=researcher,
    )
    crew = Crew(agents=[researcher, writer], tasks=[research_task, write_task])
    result = crew.kickoff()
    return {"summary": str(result)}
```

Your crew code is unchanged — Cordum handles safety enforcement at the platform layer.

## Step 4: Start the stack

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
docker compose up -d
docker compose ps   # Verify all services are healthy
```

## Step 5: Test the safety gates

### Allowed request

```bash
curl -s http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "job.default",
    "prompt": "Summarize our Q3 revenue trends"
  }'
```

Result: job accepted and dispatched to your crew.

### PII blocked

```bash
curl -s http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "job.default",
    "prompt": "Look up customer with SSN 123-45-6789"
  }'
```

Result:

```json
{
  "error": "job denied by safety policy",
  "reason": "Input contains PII — redact before submitting",
  "rule": "deny-pii-input"
}
```

The crew never sees this request.

### Approval required

```bash
curl -s http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "job.default",
    "prompt": "Delete production database records older than 90 days"
  }'
```

Result:

```json
{
  "job_id": "job-xyz789",
  "status": "PENDING_APPROVAL",
  "reason": "Task involves sensitive operations — human approval required"
}
```

The job is paused. Your crew won't execute until a human approves it.

### Approve or reject the pending job

```bash
# List pending approvals
curl -s http://localhost:8081/api/v1/approvals \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default"

# Approve (or use cordumctl)
cordumctl approval job job-xyz789 --approve
```

After approval, the job is released to your crew for execution.

## Step 6: View the audit trail

Open [http://localhost:8082](http://localhost:8082) and navigate to **Jobs**.

You'll see three entries:
- **Q3 revenue** — `SUCCEEDED`, rule: `allow-crew-tasks`
- **SSN lookup** — `DENIED`, rule: `deny-pii-input`
- **Production delete** — `PENDING_APPROVAL` or `SUCCEEDED` (if approved), rule: `require-approval-sensitive`

Every decision includes the matched rule, timestamp, tenant, and the safety policy version that was active.

## How the gates work

```
Job submitted → Safety Kernel evaluates policy
                       │
              ┌────────┼────────────┐
              │        │            │
           ALLOWED   DENIED    NEEDS APPROVAL
              │        │            │
         Dispatched  Returned   Queued until
         to crew     with       human approves
              │      reason     or rejects
         Crew runs               │
              │           ┌──────┴──────┐
         Result          APPROVED    REJECTED
         returned            │           │
                        Dispatched   Returned
                        to crew      with reason
```

The Safety Kernel evaluates rules top-to-bottom. The first matching rule wins. Because `deny-pii-input` and `require-approval-sensitive` are listed before `allow-crew-tasks`, they take precedence when their patterns match.

## Next steps

- **Add output scanning** — the `output_policy.enabled: true` setting scans crew *output* for PII before returning results to the caller
- **Custom approval workflows** — use the [Workflow Engine](/concepts/workflow-step-types) to build multi-step approval chains
- **Per-agent policies** — assign risk tiers to [Agent Identities](/api-reference/full-reference#101-agent-identities) and match policies on `agent.risk_tier`
- **Deploy to production** — see the [Deployment Guide](/operations/deployment) for Kubernetes setup
