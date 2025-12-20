# coretexOS: Platform Philosophy Context

You are building **coretexOS** - a platform for AI workflow orchestration. Before writing any code, understand this critical distinction:

**We are building a PLATFORM, not a PRODUCT.**

## What this means

A **product** solves specific problems with pre-built features:
- "Here's our code reviewer"
- "Here's our document summarizer"
- "Here's our email responder"

A **platform** provides primitives that users compose to solve ANY problem:
- "Here's how you register YOUR code reviewer"
- "Here's how you connect YOUR tools into workflows"
- "Here's how you orchestrate YOUR AI agents"

## The core architecture

1. **Generic primitives** (we build these):
   - Workflow engine (DAG execution, loops, conditions, error handling)
   - Worker registry (Docker, HTTP, Lambda, Script endpoints)
   - Expression language for data flow between steps
   - Configuration hierarchy (org → team → workflow → step)
   - Safety kernel, rate limiting, cost tracking

2. **User's workers** (users bring these):
   - Their LLM prompts wrapped as workers
   - Their APIs as HTTP workers
   - Their scripts as Script workers
   - Their containers as Docker workers

## Agents vs workers (not the same)

- A **worker** is an executable runtime (process/container) that subscribes to job topics and does work. It’s what you see in heartbeats (worker id, capabilities, max parallel, region/pool).
- An **agent** is configuration for an LLM-style step (system prompt, model label, temperature, default topic/adapter). It’s data, not a running service.
- Relationship: **many agents can be served by one worker**, and **a worker can expose multiple topics/capabilities**. Workflows dispatch steps to topics; some steps may reference an agent definition, but execution still happens in a worker.

## Why platform > product

- **Lock-in through value**: Users accumulate workers, workflows, team knowledge → switching cost is their own investment
- **Infinite use cases**: We don't limit what users can build
- **Network effects**: Teams share workers → org-wide value compounds
- **We stay lean**: No maintaining dozens of pre-built integrations

## The user journey

1. Register workers (wrap their LLMs, APIs, scripts)
2. Compose workflows (connect workers with expressions)
3. Run & monitor (execute, observe, iterate)
4. Share & scale (team templates, org-wide workers)

## When coding, ask yourself

- "Am I building a generic primitive, or a specific feature?"
- "Could a user achieve this by composing existing primitives?"
- "Does this increase platform flexibility or limit it?"

## The mantra

We build the orchestra pit, not the instruments. Users bring their own instruments and compose their own symphonies.
