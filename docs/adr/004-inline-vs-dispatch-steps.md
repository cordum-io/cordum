# ADR-004: Workflow Engine Inline vs Dispatch Step Types

- Status: Accepted
- Date: 2026-02-01

## Context

The workflow engine supports multiple step types. Some are pure data operations
(conditions, transforms, delays) while others require external execution (jobs,
sub-workflows). Running all steps through the job dispatch pipeline adds
unnecessary latency and complexity for steps that can be resolved locally.

## Decision

Split step types into two categories:

### Inline Steps (executed synchronously in the engine)

| Step Type | Behavior |
|-----------|----------|
| `condition` | Evaluate expression, choose branch |
| `delay` | Schedule timer, mark pending |
| `approval` | Create approval request, wait for human |
| `notify` | Fire notification event |
| `transform` | Evaluate template expressions against run context |
| `storage` | Read/write workflow context paths |
| `switch` | Multi-branch condition evaluation |

These execute within the engine's scan loop — no job dispatch, no worker
involvement. Results are written directly to the run's step state in Redis.

### Dispatch Steps (require external execution)

| Step Type | Behavior |
|-----------|----------|
| `job` | Submit to scheduler for worker dispatch |
| `fan_out` | Expand into parallel job submissions |
| `parallel` | Execute multiple branches concurrently |
| `loop` | Iterate with per-iteration job dispatch |
| `sub_workflow` | Trigger a nested workflow run |

These create jobs or child runs that execute outside the engine process.

### Expression Syntax

Inline steps use `${ expression }` template syntax (not `{{ }}`). The
`evalTemplates` function recursively evaluates expressions against the run's
context scope, resolving `input.*`, `steps.<id>.output.*`, and `context.*`
references.

Key source files:
- `core/workflow/engine.go` — step handler dispatch and inline execution
- `core/workflow/models.go` — `StepType` constants
- `docs/workflow-step-types.md` — user-facing step type reference

## Consequences

Positive:
- Inline steps complete in microseconds (no network round-trip)
- Simpler error handling — no job state machine for data transforms
- Workflow engine can process data-only workflows without scheduler involvement
- Reduces load on scheduler and NATS for internal operations

Tradeoffs:
- Engine scan loop must handle inline step failures without blocking
- Two execution models to understand and debug
- Inline steps cannot leverage worker pool scaling
