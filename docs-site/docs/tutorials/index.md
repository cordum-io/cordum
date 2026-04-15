---
sidebar_position: 1
title: Tutorials
slug: /tutorials
---

# Tutorials

Step-by-step guides for integrating AI frameworks with Cordum governance. Each tutorial takes under 10 minutes and requires zero prior Cordum experience.

## Framework Quickstarts

- **[Govern Your LangGraph Agent in 5 Minutes](/tutorials/langgraph-5min)** — Add PII detection and audit trails to a LangGraph research agent
- **[Add Safety Gates to CrewAI](/tutorials/crewai-safety-gates)** — Enforce approval workflows and content scanning on CrewAI crews
- **[Cordum + AutoGen: Multi-Agent Governance](/tutorials/autogen-multi-agent)** — Rate limiting, injection detection, and escalation controls for AutoGen agents

## Before you start

All tutorials use `cordumctl init --framework <name>` to generate a working project scaffold with:

- Docker Compose stack (Cordum services + your framework worker)
- Safety policy with deny-default rules
- Python worker code using the [CAP Python SDK](/api-reference/sdk-reference)
- Ready-to-run configuration — `docker compose up` and go
