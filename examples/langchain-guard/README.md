# LangChain + Cordum Safety Guard

Add a safety layer to any LangGraph agent in 4 lines of code.

## Quick Start

```bash
# 1. Start Cordum (writes ../../.env and ../../certs/, starts the stack)
(cd ../.. && ./tools/scripts/quickstart.sh)

# 2. Install deps
pip install -r requirements.txt

# 3. Set keys, and trust the stack's dev CA: the guard and the OpenAI
#    client both read one CA bundle from SSL_CERT_FILE
source ../../.env && export CORDUM_API_KEY
export OPENAI_API_KEY="sk-..."
cat "$(python -m certifi)" ../../certs/ca/ca.crt > cordum-ca-bundle.pem
export SSL_CERT_FILE="$PWD/cordum-ca-bundle.pem"

# 4. Load safety.yaml as the policy bundle secops/financial_ops
jq -n --rawfile content safety.yaml '{content: $content, enabled: true}' |
  curl -X PUT https://localhost:8081/api/v1/policy/bundles/secops~financial_ops \
    --cacert ../../certs/ca/ca.crt \
    -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
    -H "Content-Type: application/json" --data-binary @-

# 5. Run the unguarded agent (everything is allowed)
python agent.py

# 6. Run the guarded agent (Cordum enforces safety policies)
python agent_guarded.py
```

## What's In This Example

| File | Description |
|------|-------------|
| `agent.py` | Baseline LangGraph agent — no safety layer |
| `agent_guarded.py` | Same agent with Cordum safety guard |
| `safety.yaml` | Safety policy (deny destructive ops, approve large transfers) |

## Full Tutorial

See [Secure Your LangGraph Agent with Cordum in 10 Minutes](../../docs/tutorials/langchain-guard.md) for a detailed walkthrough.
