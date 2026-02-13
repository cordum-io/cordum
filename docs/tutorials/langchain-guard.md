# Secure Your LangGraph Agent with Cordum in 10 Minutes

AI agents are powerful — but without guardrails, they can delete databases, transfer money, or leak data before anyone notices. This tutorial shows you how to add a **safety layer** to any LangGraph agent using Cordum.

## What You'll Build

```
BEFORE                              AFTER
┌──────────┐    ┌──────────┐       ┌──────────┐    ┌──────────┐    ┌──────────┐
│   User   │───▶│  Agent   │       │   User   │───▶│  Agent   │───▶│  Cordum  │
│          │    │ (unsafe) │       │          │    │ (guarded)│    │  Safety  │
└──────────┘    └────┬─────┘       └──────────┘    └────┬─────┘    │  Kernel  │
                     │                                  │          └────┬─────┘
                     ▼                                  ▼               │
               [Runs anything]                    [Only runs if       │
                                                   policy says OK]◀───┘
```

**Before Cordum:** Your agent runs `delete_account()` without hesitation.
**After Cordum:** The Safety Kernel blocks destructive operations and requires human approval for high-risk actions — all without changing your agent's logic.

## Prerequisites

- **Docker** + Docker Compose (for the Cordum stack)
- **Python 3.9+**
- **OpenAI API key** (or any LangChain-compatible LLM)

## Step 1: Start Cordum (2 minutes)

```bash
git clone https://github.com/cordum-io/cordum.git
cd cordum

# Generate an API key
export CORDUM_API_KEY="$(openssl rand -hex 32)"
echo "CORDUM_API_KEY=$CORDUM_API_KEY" > .env

# Start the stack
docker compose up -d
```

Verify it's running:

```bash
curl -s http://localhost:8081/healthz
# {"status":"ok"}
```

The dashboard is available at [http://localhost:8082](http://localhost:8082).

## Step 2: Install Dependencies (30 seconds)

```bash
cd examples/langchain-guard
pip install -r requirements.txt
```

This installs LangChain, LangGraph, and the `cordum-guard` SDK with LangChain integration.

## Step 3: Define Your Safety Policy (2 minutes)

Open `safety.yaml` in this directory. It defines three rules:

```yaml
version: "1"
rules:
  # Block destructive operations entirely
  - id: no-destructive-ops
    match:
      capabilities: [delete_account]
      risk_tags: [destructive]
    decision: deny
    reason: "Destructive operations are blocked by policy."

  # Large transfers require human approval
  - id: financial-approval
    match:
      capabilities: [transfer_funds]
      metadata:
        amount_gt: 10000
    decision: require_approval
    reason: "Transfers over $10,000 require human approval."

  # Everything else is allowed
  - id: default-allow
    match: {}
    decision: allow
    reason: "Default allow for safe operations."
```

**What this means:**
| Action | Safety Kernel Decision |
|--------|----------------------|
| `lookup_balance("alice")` | **ALLOW** — read-only, no risk |
| `transfer_funds("bob", "alice", 500)` | **ALLOW** — under $10k threshold |
| `transfer_funds("bob", "alice", 15000)` | **REQUIRE_APPROVAL** — needs human sign-off |
| `delete_account("charlie")` | **DENY** — blocked, no exceptions |

Load the policy into Cordum:

```bash
curl -X POST http://localhost:8081/api/v1/policies \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "financial_ops",
    "name": "Financial Operations Policy",
    "content_yaml": "'"$(cat safety.yaml)"'"
  }'
```

## Step 4: Run the Unguarded Agent

First, see what happens without Cordum:

```bash
export OPENAI_API_KEY="sk-..."
python agent.py
```

Expected output:

```
============================================================
  Banking Agent (NO safety layer)
============================================================

> What is Alice's account balance?
  Account 'alice' balance: $5,200.00

> Transfer $15,000 from Bob to Alice.
  Transferred $15,000.00 from bob to alice.        ← No check!

> Delete Charlie's account.
  Account 'charlie' has been permanently deleted.   ← Yikes!
```

The agent happily transfers $15k and deletes an account. No questions asked.

## Step 5: Run the Guarded Agent

Now run the same agent with Cordum:

```bash
python agent_guarded.py
```

Expected output:

```
[CORDUM] Connected to http://localhost:8081
[CORDUM] Guarded 3 tools: ['lookup_balance', 'transfer_funds', 'delete_account']

============================================================
  Banking Agent (with Cordum safety layer)
============================================================

> What is Alice's account balance?
  Account 'alice' balance: $5,200.00                ← ALLOWED

> Transfer $15,000 from Bob to Alice.
  [BLOCKED] transfer_funds: Transfers over $10,000  ← BLOCKED — needs approval
  require human approval.

> Delete Charlie's account.
  [BLOCKED] delete_account: Destructive operations  ← DENIED by policy
  are blocked by policy.
```

The Safety Kernel intervened:
- **Balance lookup** passed through (safe operation)
- **$15k transfer** was blocked pending human approval
- **Account deletion** was denied outright

## Step 6: Approve from the Dashboard

Open [http://localhost:8082](http://localhost:8082) and navigate to **Approvals**.

You'll see the pending transfer waiting for sign-off. Click **Approve** to let it through, or **Reject** to deny it permanently.

## The Key Difference: 4 Lines of Code

The only difference between the unguarded and guarded agent:

```python
from cordum_guard import CordumClient
from cordum_guard.langchain import CordumToolGuard

client = CordumClient("http://localhost:8081", api_key=os.getenv("CORDUM_API_KEY"))
guard = CordumToolGuard(client, policy="financial_ops", risk_tags=["financial"])

# Wrap your existing tools
safe_tools = guard.wrap(tools)

# Use safe_tools instead of tools in your agent
agent = create_react_agent(llm, safe_tools)
```

Your agent code doesn't change. Your tools don't change. Cordum sits between the agent and the tools, enforcing your policies.

## Advanced: Custom Risk Tags

Annotate tools with specific risk tags for fine-grained policy control:

```python
guard = CordumToolGuard(
    client,
    policy="financial_ops",
    risk_tags=["financial", "pii"],  # Multiple tags
)
```

Write policies that match on any combination of tags:

```yaml
rules:
  - id: pii-access-requires-approval
    match:
      risk_tags: [pii]
    decision: require_approval
    reason: "PII access requires approval."
```

## Advanced: Using the @guard Decorator

For non-LangChain code, use the `@guard` decorator directly:

```python
from cordum_guard import CordumClient, guard

client = CordumClient("http://localhost:8081", api_key="your-key")

@guard(client, policy="financial_ops", risk_tags=["financial", "write"])
def execute_transfer(amount: float, to_account: str):
    bank_api.transfer(amount, to_account)
```

Works with both sync and async functions.

## Advanced: Async Agents

The LangChain integration supports async tools out of the box. If your tools define `_arun`, Cordum will guard those too:

```python
@tool
async def async_lookup(account: str) -> str:
    """Async balance lookup."""
    return await db.get_balance(account)

# CordumToolGuard wraps both _run and _arun
safe_tools = guard.wrap([async_lookup])
```

## What's Next?

- **Write more policies**: Cordum supports complex matching on capabilities, risk tags, metadata, and labels
- **Add workflows**: Chain tools into multi-step workflows with built-in retries and approvals
- **Monitor in production**: The Cordum dashboard shows every decision, every blocked action, and a full audit trail
- **Explore the docs**: See the [full documentation](https://github.com/cordum-io/cordum/tree/main/docs) for more

---

**Questions?** Join [Discord](https://discord.gg/HGGHbU26) or open a [GitHub Discussion](https://github.com/cordum-io/cordum/discussions).
