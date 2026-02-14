# LangChain + Cordum Safety Guard

Add a safety layer to any LangGraph agent in 4 lines of code.

## Quick Start

```bash
# 1. Start Cordum
cd ../.. && docker compose up -d && cd examples/langchain-guard

# 2. Install deps
pip install -r requirements.txt

# 3. Set keys
export CORDUM_API_KEY="your-api-key"
export OPENAI_API_KEY="sk-..."

# 4. Run the unguarded agent (everything is allowed)
python agent.py

# 5. Run the guarded agent (Cordum enforces safety policies)
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
