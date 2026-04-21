---
sidebar_position: 2
title: Safety Kernel
slug: /concepts/safety-kernel
---

# Safety Kernel

The Safety Kernel is Cordum's policy engine. Every job passes through it before execution.

## Policy Rules

Policies are defined in YAML with match criteria and decisions:

```yaml
rules:
  - id: fraud-review-required
    match:
      topics:
        - job.fraud-detection.process
    decision: require_approval
    reason: "Fraud detection results must be reviewed by a human."
```

## Decisions

| Decision | Effect |
|----------|--------|
| `allow` | Job proceeds to dispatch |
| `deny` | Job rejected, sent to DLQ |
| `require_approval` | Job queued for human approval |
| `allow_with_constraints` | Job allowed with runtime constraints |
| `throttle` | Job delayed and retried |

## Agent Identity Integration

Policy rules can match on agent identity attributes when workers are linked to agent identities:

```yaml
- id: critical-agent-approval
  match:
    agent_risk_tiers: [high, critical]
  decision: require_approval
  reason: "High-risk agents require human approval."
```

See [API Reference](/api-reference/full-reference) for agent identity CRUD endpoints.
