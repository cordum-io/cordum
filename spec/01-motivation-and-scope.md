# Motivation and Scope

## Problem Statement
- AI workloads are increasingly multi-agent and multi-node. A single model calling a handful of tools is not enough for tasks that require parallelism, specialization, or long-running execution.
- Existing interfaces (e.g., model-specific tool-calling protocols) do not cover scheduling, safety, memory fabrics, or cluster-wide state. They assume a single runtime and do not address bus-level interoperability.
- Platform teams need a portable, vendor-neutral way to define how agents exchange work, report health, and respect policy across a cluster.

## Goals
- Define a **job protocol** for AI agents in distributed systems.
- Standardize envelopes, heartbeats, job states, and pointers so schedulers and workers can interoperate.
- Remain portable across buses (NATS, Kafka, others) and memory backends (Redis, object storage, etc.).
- Make safety and policy checks first-class by specifying a hook for a Safety Kernel.
- Support orchestrators and workflows while keeping the same core job abstraction.

## Non-goals
- CAP is not a model API; it is agnostic to LLM vendors and prompt formats.
- CAP is not a UI or a product; it is a wire-level specification plus examples.
- CAP does not mandate a single transport. NATS is a natural fit, but CAP can be implemented on any pub/sub system with subjects/topics and competing consumers.
