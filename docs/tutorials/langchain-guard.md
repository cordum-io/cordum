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
./tools/scripts/quickstart.sh
source .env && export CORDUM_API_KEY
```

`quickstart.sh` writes `.env` with a generated `CORDUM_API_KEY`, creates the
TLS certificates under `./certs/` and starts the stack (see
[quickstart.md](../quickstart.md)). The gateway serves HTTPS with a
development CA, so clients pass it with `--cacert`.

Verify it's running:

```bash
curl -sS --cacert ./certs/ca/ca.crt https://localhost:8081/healthz
# {"status":"ok","checks":{"goroutines":"ok"}}
```

The dashboard is available at [http://localhost:8082](http://localhost:8082).

## Step 2: Install Dependencies (30 seconds)

```bash
cd examples/langchain-guard
pip install -r requirements.txt
```

This installs LangChain, LangGraph, and the `cordum-guard` SDK with LangChain integration.

The tutorial needs `cordum-guard` 2.17.1 or later. `CordumToolGuard` 2.17.0
runs the tool on `REQUIRE_APPROVAL` and sends no tool arguments, so the
`arg.amount` threshold below never matches and a large transfer would go
through.

## Step 3: Define Your Safety Policy (2 minutes)

Open `safety.yaml` in this directory. It defines three rules:

```yaml
version: "1"
rules:
  # Block destructive operations entirely. Match conditions are ANDed, so
  # this matches on the capability alone: agent_guarded.py tags every tool
  # "financial", never "destructive".
  - id: no-destructive-ops
    match:
      capabilities: [delete_account]
    decision: deny
    reason: "Destructive operations are blocked by policy."

  # Large transfers require human approval. CordumToolGuard sends each
  # scalar tool argument as an arg.<name> label, so the transfer amount
  # arrives as arg.amount. label_threshold matches when it is a number above
  # 10000, and also when it is not a plain number (such as "15,000").
  - id: financial-approval
    match:
      capabilities: [transfer_funds]
      label_threshold:
        arg.amount: 10000
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

Load the policy into Cordum as a policy bundle with
`PUT /api/v1/policy/bundles/{id}`. Bundle IDs must start with `secops/`, and
the URL path writes `/` as `~`, so `secops/financial_ops` becomes
`secops~financial_ops`. The body is `{"content": "<policy YAML>", "enabled":
true}`, and `jq` builds it from the file:

```bash
jq -n --rawfile content safety.yaml '{content: $content, enabled: true}' |
  curl -X PUT https://localhost:8081/api/v1/policy/bundles/secops~financial_ops \
    --cacert ../../certs/ca/ca.crt \
    -H "X-API-Key: $CORDUM_API_KEY" \
    -H "X-Tenant-ID: default" \
    -H "Content-Type: application/json" \
    --data-binary @-
```

The key needs policy write access (the `admin` role). The gateway rejects
YAML that fails policy validation, and the Safety Kernel applies the bundle
on its next policy reload.

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

Now run the same agent with Cordum. The guard and the OpenAI client both use
httpx, which reads a single CA bundle from `SSL_CERT_FILE`, so append the
stack's development CA to certifi's bundle and point both at it:

```bash
cat "$(python -m certifi)" ../../certs/ca/ca.crt > cordum-ca-bundle.pem
export SSL_CERT_FILE="$PWD/cordum-ca-bundle.pem"
python agent_guarded.py
```

Expected output:

```
[CORDUM] Connected to https://localhost:8081
[CORDUM] Guarded 3 tools: ['lookup_balance', 'transfer_funds', 'delete_account']

============================================================
  Banking Agent (with Cordum safety layer)
============================================================

> What is Alice's account balance?
  Account 'alice' balance: $5,200.00                ← ALLOWED

> Transfer $15,000 from Bob to Alice.
  [APPROVAL REQUIRED] Transfers over $10,000        ← NOT RUN — needs approval
  require human approval. (rule financial-approval)

> Delete Charlie's account.
  [BLOCKED] delete_account: Destructive operations  ← DENIED by policy
  are blocked by policy.
```

The Safety Kernel intervened:
- **Balance lookup** passed through (safe operation)
- **$15k transfer** did not run. The guard raised `CordumApprovalRequiredError`,
  LangGraph's default `ToolNode` re-raised it from `agent.invoke`, and
  `agent_guarded.py` catches it and prints the reason
- **Account deletion** was denied outright. The model gets
  `[BLOCKED] delete_account: ...` as the tool result, so its reply wording varies

## Step 6: Send Approvals to the Dashboard

The tool guard stops at the verdict. `/api/v1/policy/evaluate` creates no
approval, so the $15k transfer does not show up under **Approvals**. To get a
human decision, submit the action as a job with the same labels: the job waits
under **Approvals** in the dashboard at
[http://localhost:8082](http://localhost:8082) until someone clicks
**Approve** or **Reject**. The `@guard` decorator (below) submits that job on
`require_approval` and waits for its outcome.

## The Key Difference: 4 Lines of Code

The only difference between the unguarded and guarded agent:

```python
from cordum_guard import CordumClient
from cordum_guard.langchain import CordumToolGuard

client = CordumClient("https://localhost:8081", api_key=os.getenv("CORDUM_API_KEY"))
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

client = CordumClient("https://localhost:8081", api_key="your-key")

@guard(client, policy="financial_ops", risk_tags=["financial", "write"])
def execute_transfer(amount: float, to_account: str):
    bank_api.transfer(amount, to_account)
```

Works with both sync and async functions. When a call needs approval,
`@guard` files an approval job on the `job.guard` topic and waits for a
human. The shipped pools config registers `job.guard`; if your topic
registry predates that, register it by hand or the gateway rejects the
job with `unknown_topic` (see
[Approval-only topics](../topics/registry.md#approval-only-topics)).

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
