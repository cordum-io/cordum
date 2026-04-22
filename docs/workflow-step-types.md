# Workflow Step Types Reference

This document describes all workflow step types currently modeled in Cordum (`core/workflow/models.go`) and how they execute in the workflow engine (`core/workflow/engine.go`).

## 1. Overview

Cordum workflows are DAGs of `Step` nodes. A run advances when dependencies are satisfied and `scheduleReady()` dispatches ready nodes.

Run lifecycle states:

- `pending`
- `running`
- `waiting`
- `succeeded`
- `failed`
- `cancelled`
- `timed_out`

Step lifecycle states:

- `pending`
- `running`
- `waiting`
- `succeeded`
- `failed`
- `cancelled`
- `timed_out`

Execution model by type:

- Dedicated engine handlers: `worker`, `approval`, `condition`, `delay`, `notify`, `transform`, `storage`, `switch`, `parallel`, `loop`, `subworkflow`
- Generic job dispatch fallback: `llm`, `http`, `container`, `script`, `input`
- `for_each` fan-out behavior is implemented on any step with `for_each` set (creates child steps like `step_id[0]`, `step_id[1]`)

## 2. Expression Syntax Reference

Expression evaluation uses `Eval()` in `core/workflow/eval.go`.

Supported expression forms:

- Literals: numbers, booleans, quoted strings
- Dot paths: `input.customer.id`, `ctx.session.region`, `steps.validate.output.ok`
- Comparisons: `==`, `!=`, `>`, `<`, `>=`, `<=`
- Unary boolean not: `!steps.check.output`
- Functions:
  - `length(x)` for arrays, strings, maps
  - `first(x)` for arrays

Template syntax in step input values uses `${expr}` and is resolved by `evalTemplates()`.

Examples:

```text
length(input.items) > 0
steps.validate.output.ok == true
${input.customer.id}
"ticket-${input.case_id}-${first(input.tags)}"
```

## 3. Scope Variables Reference

| Variable | Available now | Meaning |
| --- | --- | --- |
| `input.*` | yes | `WorkflowRun.Input` payload |
| `ctx.*` | yes | `WorkflowRun.Context` map |
| `steps.*` | yes | `run.Context["steps"]` outputs/pointers |
| `item` | yes (for `for_each`) | Current fan-out item |
| `loop.index` | yes (`loop`) | Zero-based iteration index for loop child dispatch |
| `loop.iteration` | yes (`loop`) | One-based iteration count (`index + 1`) |
| `loop.previous_output` | yes (`loop`) | Previous iteration output (inline output or result pointer) |

## 4. Implemented Step Types (Dedicated Handlers)

### 4.1 `worker`

Purpose:

- Default worker/job dispatch step.

Key config fields:

- `topic`
- `worker_id`
- `input`
- `route_labels`
- `timeout_sec`
- `retry`

Execution behavior:

- Dispatches a `JobRequest` on `sys.job.submit`.
- Stores step payload in memory and sets `ContextPtr`.

Output format:

- On success, step output stores `result_ptr` and optional inlined decoded payload in `run.Context.steps[step_id]`.

Dashboard UI:

- Rendered as `agent-task` (or `pack-action` / `tool-call` depending on metadata).
- Node details show job link, safety decision, request/result payload.

YAML example:

```yaml
steps:
  classify:
    type: worker
    topic: job.support.classify
    worker_id: support-pool
    input:
      ticket_id: "${input.ticket_id}"
      body: "${input.message}"
    route_labels:
      pool: support
    timeout_sec: 60
    retry:
      max_retries: 2
      initial_backoff_sec: 1
      max_backoff_sec: 8
      multiplier: 2
    output_path: ctx.classification
```

### 4.2 `approval`

Purpose:

- Human gate before workflow continues.

Key config fields:

- `input` (optional context for reviewers)
- `output_path` (optional)

Execution behavior:

- Step enters `waiting`.
- Run enters `waiting`.
- Resumed by `ApproveStep` API (`approved=true|false`).

Output format:

- Status only; approval metadata is reflected in run/step state.

Dashboard UI:

- Dedicated approval detail panel with approve/reject actions.

YAML example:

```yaml
steps:
  manual_review:
    type: approval
    depends_on: [classify]
    input:
      reason: "High impact case"
      reviewer_group: risk-ops
```

### 4.3 `condition`

Purpose:

- Branch gate based on expression result.

Key config fields:

- `condition` (required)
- `output_path` (optional)

Execution behavior:

- Evaluates expression via `evalCondition()`.
- On success stores boolean output and marks step `succeeded`.
- On missing/invalid expression marks step `failed`.

Output format:

- Boolean (`true`/`false`) in step output.

Dashboard UI:

- Dedicated condition detail panel shows expression and boolean result.
- DAG node has true/false branch handles.

YAML example:

```yaml
steps:
  should_escalate:
    type: condition
    condition: "length(input.flags) > 0"
    output_path: ctx.gates.should_escalate
    depends_on: [classify]
```

### 4.4 `delay`

Purpose:

- Wait for a relative or absolute time.

Key config fields:

- `delay_sec`
- `delay_until` (RFC3339)
- `timeout_sec` (fallback delay when others not set)

Execution behavior:

- Computes delay via `delayForStep()`.
- Schedules internal timer and transitions from `running` to `succeeded` when due.

Output format:

- No custom payload; state/timing fields indicate completion.

Dashboard UI:

- Dedicated delay detail panel shows configured delay and elapsed/remaining time.

YAML example:

```yaml
steps:
  cool_down:
    type: delay
    depends_on: [manual_review]
    delay_sec: 300
```

### 4.5 `notify`

Purpose:

- Emit a workflow event alert.

Key config fields:

- `input.level`
- `input.message`
- `input.code`
- `input.component`

Execution behavior:

- Evaluates template input.
- Publishes `BusPacket_Alert` on `sys.workflow.event`.

Output format:

- No result pointer; completion state plus timeline event.

Dashboard UI:

- Generic detail panel (status/output).
- `notify` icon available in DAG nodes.

YAML example:

```yaml
steps:
  alert_ops:
    type: notify
    depends_on: [manual_review]
    input:
      level: INFO
      component: workflow-engine
      code: REVIEW_COMPLETED
      message: "Review completed for ${input.ticket_id}"
```

## 5. Newer Step Types (Mixed Implementation Status)

These step types exist as constants in `models.go`. `switch`, `parallel`, `loop`, `subworkflow`, and `storage` now have dedicated handlers. The remaining types in this section still use generic dispatch fallback unless noted otherwise.

### 5.1 `switch`

Purpose:

- Multi-branch case routing with inline engine evaluation.

Key config fields:

- `condition` (required switch expression)
- `input.cases`
- `input.default` (or `input.default_step`)

Current behavior:

- Evaluates `condition` via `Eval()`.
- Matches first case where value equals `match`/`when`/`value`.
- Uses default branch when no case matches.
- Marks non-selected branches as `cancelled` with reason `switch_branch_not_taken`.
- Completes inline with output `{ matched_case, matched_value, target_step }`.
- Emits timeline event `step_switch_evaluated`.

Output format:

- Inline output object stored in `run.Context.steps[step_id].output`.

Dashboard UI:

- Switch config with expression, cases list, and default branch selector.
- DAG detail panel shows case rows, matched branch highlighting, and fallback branch state.

YAML example:

```yaml
steps:
  route_by_priority:
    type: switch
    input:
      cases:
        - when: "input.priority == 'critical'"
          next: urgent_path
      default: normal_path
```

### 5.2 `parallel`

Purpose:

- Concurrent execution of multiple child step definitions with completion strategies.

Key config fields:

- `input.steps` (array of child step IDs to execute)
- `input.strategy` (`all` | `any` | `n_of_m`)
- `input.required` (required success count when strategy is `n_of_m`)
- `max_parallel` (optional dispatch throttle)

Execution behavior:

- Parent step initializes child step runs from `input.steps`.
- Child jobs are dispatched concurrently (or throttled by `max_parallel`).
- Completion strategies:
  - `all`: succeed only when all children succeed; fail on first child failure.
  - `any`: succeed when first child succeeds; cancel remaining children.
  - `n_of_m`: succeed when `required` children succeed; fail when threshold becomes unreachable.
- Parent output aggregates child outputs as `map[child_step_id] -> output entry`.
- Timeline events:
  - `step_parallel_started`
  - `step_parallel_completed`

Output format:

- Parent step writes aggregated map to `run.Context.steps[parallel_step_id].output`.
- Child step outputs are still stored under their own step IDs.

Dashboard UI:

- Parallel node type in the workflow editor.
- Config supports child step multi-select, completion strategy, required count (`n_of_m`), and max concurrency.
- DAG details panel shows strategy, progress (`N/M`), and child status list.

YAML examples:

```yaml
steps:
  parallel_all:
    type: parallel
    max_parallel: 3
    input:
      strategy: all
      steps: [check_a, check_b, check_c]

  parallel_any:
    type: parallel
    input:
      strategy: any
      steps: [provider_a, provider_b, provider_c]

  parallel_threshold:
    type: parallel
    max_parallel: 2
    input:
      strategy: n_of_m
      required: 2
      steps: [scan_a, scan_b, scan_c]
```

### 5.3 `loop`

Purpose:

- Iterative execution of a body step with safety guards and expression-based stop conditions.

Key config fields:

- `input.body_step` (recommended body step ID; `input.body` alias supported)
- `input.max_iterations` (default `100`; hard safety cap)
- `input.condition` (`while` behavior, continue while truthy; `input.while` alias supported)
- `input.until` (stop when truthy)

Execution behavior:

- Parent loop step enters `running` and manages child iterations as virtual step IDs:
  - `loop_step_id[0]`, `loop_step_id[1]`, ...
- Before each iteration, scope includes:
  - `loop.index`
  - `loop.iteration`
  - `loop.previous_output`
- Child job env also includes:
  - `loop_index`
  - `loop_iteration`
  - `loop_previous_output`
- Stop rules:
  - If `condition` is set and becomes false, loop completes successfully.
  - If `until` is set and becomes true, loop completes successfully.
  - If neither is set, loop runs exactly `max_iterations`.
  - If `condition`/`until` still require continuation at `max_iterations`, loop fails with a max-iteration error.
- Loop body step definitions referenced by `body_step` are orchestrated by loop parent and excluded from top-level scheduling.
- Timeline events:
  - `step_loop_iteration`
  - `step_loop_completed`

Output format:

- Parent step output object:
  - `iterations`
  - `last_output`

Dashboard UI:

- Loop node type is available in workflow builder.
- Loop config includes body step, max iterations, `condition`, and `until`.
- DAG node details include loop progress and expandable per-iteration child status.

YAML examples:

```yaml
steps:
  retry_exactly_three_times:
    type: loop
    input:
      body_step: run_scan
      max_iterations: 3
```

```yaml
steps:
  retry_until_clean:
    type: loop
    input:
      body_step: run_scan
      max_iterations: 1000
      until: "steps.run_scan.output.clean == true"
```

```yaml
steps:
  while_budget_available:
    type: loop
    input:
      body_step: process_chunk
      max_iterations: 20
      condition: "loop.index < 5"
```

### 5.4 `transform`

Purpose:

- Inline data shaping/mapping between steps — no job dispatch, executes instantly in the engine.

Key config fields:

- `input` — map of key/value pairs where each value is an expression using `${...}` template syntax
- `output_path` — optional dot-separated path in `run.Context` to write the result

Current behavior:

- **Dedicated inline handler** (no job dispatch).
- Evaluates ALL keys in `step.Input` as template expressions via `evalTemplates()`.
- Each key/value pair in `input` becomes a key/value in the output map.
- Validates output schema if `OutputSchema` or `OutputSchemaID` is defined.
- Writes result to `OutputPath` in run context via `recordStepInlineOutput()`.
- Marks step as `succeeded` immediately.
- If any expression evaluation fails, step is marked `failed` with error message including the expression error.
- If input is `nil` (no mappings), succeeds with empty output map `{}`.
- Emits timeline event `step_transform_completed` or `step_transform_failed`.

Output format:

- Inline output map stored in `run.Context.steps[step_id].output`.
- If `output_path` is set, also written to `run.Context` at the specified path.

Dashboard UI:

- `Code` icon in STEP_ICONS.
- `TransformDetail` panel in NodeDetailPanel shows:
  - Status badge and duration.
  - Output path display.
  - Key/value table showing input expressions alongside their evaluated output values (when step is succeeded).
  - Error card if step failed.
  - Collapsible full output JSON.

YAML example:

```yaml
steps:
  reshape_payload:
    type: transform
    depends_on: [fetch_data]
    input:
      case_id: "${input.ticket_id}"
      summary: "${steps.fetch_data.output.title}"
      item_count: "${steps.fetch_data.output.items}"
    output_path: ctx.transformed
```

Expression syntax notes:

- Use `${expr}` for template expressions (NOT `{{ }}`).
- A value with a single `${...}` expression preserves the original type (number, boolean, map).
- A value with mixed text and `${...}` is stringified: `"Hello ${input.name}"` → `"Hello world"`.
- Nested maps in `input` are recursively evaluated.
- Missing references resolve to `nil` (no error) — use explicit validation if strict references are needed.

### 5.5 `storage`

Purpose:

- Inline read/write/delete operations on the workflow run context. No job dispatch — executes instantly in the engine.

Key config fields:

- `input.operation` (`read` | `write` | `delete`)
- `input.key` (dot-separated path into `run.Context`, e.g. `data.user.name`)
- `input.value` (required for `write`, supports `${expr}` templates)
- `output_path` (optional; write step output to run context)

Execution behavior:

- **Dedicated inline handler** (no job dispatch).
- Evaluates ALL fields in `step.Input` as template expressions via `evalTemplates()`.
- Operations:
  - `read`: Looks up `key` in `run.Context` using dot-path navigation. Fails if key not found.
  - `write`: Sets `key` in `run.Context` to `value` (creating intermediate maps as needed). Value supports `${expr}` templates resolved against run scope.
  - `delete`: Removes `key` from `run.Context` using dot-path navigation. Silent no-op if path doesn't exist.
- Validates output schema if `OutputSchema` or `OutputSchemaID` is defined.
- Writes result to `OutputPath` in run context via `recordStepInlineOutput()`.
- Marks step as `succeeded` immediately.
- If expression evaluation fails, step is marked `failed`.
- Emits timeline events `step_storage_completed` or `step_storage_failed`.

Output format:

- `read`: `{ "operation": "read", "key": "<key>", "value": <resolved_value> }`
- `write`: `{ "operation": "write", "key": "<key>", "value": <written_value> }`
- `delete`: `{ "operation": "delete", "key": "<key>" }`
- Inline output stored in `run.Context.steps[step_id].output`.

Dashboard UI:

- `Database` icon in STEP_ICONS and DAG overlay nodes.
- `StorageDetail` panel in NodeDetailPanel shows:
  - Status badge and duration.
  - Operation badge (read=info, write=success, delete=danger).
  - Target badge (context).
  - Key path display.
  - Value display (for read/write results when step succeeded).
  - Output path display.
  - Error card if step failed.
  - Collapsible full output JSON.
- Workflow editor config: operation selector, key path input with dot-notation hint, value expression textarea (write only), output path input (read only).

When to use Storage vs Transform:

- **Storage** = persistent read/write/delete of individual values in run context by key path.
- **Transform** = reshaping/mapping multiple values from step outputs into new structure.

YAML examples:

```yaml
steps:
  # Write a value to run context
  save_greeting:
    type: storage
    input:
      operation: write
      key: "data.greeting"
      value: "Hello ${input.name}"

  # Read a value back
  read_greeting:
    type: storage
    depends_on: [save_greeting]
    input:
      operation: read
      key: "data.greeting"
    output_path: ctx.greeting_result

  # Delete a temporary value
  cleanup_temp:
    type: storage
    depends_on: [read_greeting]
    input:
      operation: delete
      key: "data.greeting"
```

Nested key path example:

```yaml
steps:
  set_theme:
    type: storage
    input:
      operation: write
      key: "user.preferences.theme"
      value: "dark"
```

Expression syntax notes:

- Use `${expr}` for template expressions in `value` field.
- Key paths use dot-separated segments: `a.b.c` navigates into nested maps.
- Missing intermediate keys in `write` are created automatically.
- `read` on a missing key fails the step with an error message.

### 5.6 `subworkflow`

Purpose:

- Trigger a child workflow run, wait for completion, and propagate mapped output back to parent step.

Key config fields:

- `input.workflow_id` (required)
- `input.input_mapping` (optional; evaluated template/object for child run input)
- `input.output_mapping` (optional; evaluated when child completes, with child scope available)
- `output_path` (optional parent context write path)

Execution behavior:

- On first evaluation:
  - Validates target workflow exists.
  - Evaluates `input_mapping` into child run input (defaults to parent run input when omitted).
  - Creates child run in same store.
  - Inherits parent org/team/dry-run context.
  - Stores `parent_run_id`, `parent_step_id`, and `call_stack` metadata on child run.
  - Starts child run and marks parent step `running`.
- On re-evaluation:
  - Reads child run by tracked child run ID.
  - If child is still `pending/running/waiting`, parent remains `running`.
  - If child `succeeded`, evaluates `output_mapping` (or emits default child summary output), validates output, and marks parent `succeeded`.
  - If child `failed/cancelled/timed_out`, parent propagates terminal error and marks failed/timed_out/cancelled accordingly.
- Circular reference guard:
  - Uses `call_stack` metadata to detect cycles before creating nested child run.
  - Fails fast with `circular workflow reference detected` when cycle is found.
  - `call_stack` metadata also provides the basis for operational nested-depth limits.
- Timeline events:
  - `step_subworkflow_started`
  - `step_subworkflow_completed`

Output format:

- Default parent output includes:
  - `child_run_id`
  - `child_workflow_id`
  - `child_status`
  - child output summary (or mapped fields when `output_mapping` is provided)

Dashboard UI:

- Sub-workflow node type in workflow builder.
- Config supports workflow selector + input/output mapping + output path.
- DAG details panel shows child workflow/run links, child status badge, and mapping payloads.

YAML example:

```yaml
steps:
  invoke_child:
    type: subworkflow
    input:
      workflow_id: wf-remediation
      input_mapping:
        ticket_id: "${input.ticket_id}"
      output_mapping:
        remediation_status: "${child.status}"
        remediation_result_ptr: "${child.steps.remediate.result_ptr}"
    output_path: ctx.remediation
```

## 6. Worker-Dispatch Step Types (Generic Dispatch Today)

These are modeled constants and currently execute through generic job dispatch in `scheduleReady()`.

### 6.1 `llm`

Purpose:

- LLM completion/inference task.

Key config fields:

- `topic` (for example `job.llm.generate`)
- `input.prompt`
- `input.budget` (token limits), `meta.capability`

Execution behavior:

- Generic job dispatch fallback.

Output format:

- Job result pointer + optional inlined payload in `run.Context.steps[step_id]`.

Dashboard UI:

- Usually normalized to `agent-task` or `tool-call`.

YAML example:

```yaml
steps:
  summarize:
    type: llm
    topic: job.llm.generate
    input:
      prompt: "Summarize: ${input.message}"
```

### 6.2 `http`

Purpose:

- HTTP call step executed by worker.

Key config fields:

- `topic` (for example `job.http.request`)
- `input.method`
- `input.url`
- `input.headers` / `input.body`

Execution behavior:

- Generic job dispatch fallback.

Output format:

- Job result pointer + optional inlined payload in `run.Context.steps[step_id]`.

Dashboard UI:

- `http` icon in run overlay.

YAML example:

```yaml
steps:
  fetch_profile:
    type: http
    topic: job.http.request
    input:
      method: GET
      url: "https://api.example.com/users/${input.user_id}"
```

### 6.3 `container`

Purpose:

- Containerized execution step.

Key config fields:

- `topic` (for example `job.container.run`)
- `input.image`
- `input.args` / `input.env`

Execution behavior:

- Generic job dispatch fallback.

Output format:

- Job result pointer + optional inlined payload in `run.Context.steps[step_id]`.

Dashboard UI:

- Rendered as generic job/agent-task style.

YAML example:

```yaml
steps:
  run_scanner:
    type: container
    topic: job.container.run
    input:
      image: ghcr.io/acme/scanner:latest
      args: ["--target", "${input.repo}"]
```

### 6.4 `script`

Purpose:

- Script execution step.

Key config fields:

- `topic` (for example `job.script.run`)
- `input.language`
- `input.source`

Execution behavior:

- Generic job dispatch fallback.

Output format:

- Job result pointer + optional inlined payload in `run.Context.steps[step_id]`.

Dashboard UI:

- Rendered as generic job/agent-task style.

YAML example:

```yaml
steps:
  sanitize_text:
    type: script
    topic: job.script.run
    input:
      language: python
      source: "print('ok')"
```

### 6.5 `input`

Purpose:

- User/input collection step via worker.

Key config fields:

- `topic` (for example `job.input.collect`)
- `input.form_id`
- `input.schema`

Execution behavior:

- Generic job dispatch fallback.

Output format:

- Job result pointer + optional inlined payload in `run.Context.steps[step_id]`.

Dashboard UI:

- Rendered as generic job/agent-task style.

YAML example:

```yaml
steps:
  collect_feedback:
    type: input
    topic: job.input.collect
    input:
      form_id: feedback-v1
```

## 7. Common Step Fields and Semantics

Fields from `Step` model that apply across types:

- `depends_on`: step dependencies (all must succeed)
- `condition`: pre-gate for non-`condition` steps
- `for_each`: fan-out expression, must evaluate to array
- `max_parallel`: throttle concurrent children for `for_each` and `parallel`
- `input`: payload with template support via `${expr}`
- `input_schema` / `input_schema_id`: input validation
- `output_schema` / `output_schema_id`: output validation
- `output_path`: write output into `run.Context` path
- `timeout_sec`: job budget deadline (ms in request budget)
- `retry`: retry policy (`max_retries`, `initial_backoff_sec`, `max_backoff_sec`, `multiplier`)
- `on_error`: modeled jump target field (documented, minimal runtime handling today)
- `route_labels`: propagated to job labels for pool/worker routing
- `meta`: job metadata overrides (`actor_id`, `actor_type`, `idempotency_key`, `pack_id`, `capability`, `risk_tags`, `requires`, `labels`)

`output_path` behavior:

- Job-backed steps: stores inline result (if small and decodable) or pointer string.
- Inline steps (for example, `condition`): stores computed value directly.

## 8. Dashboard Mapping Notes

The dashboard normalizes backend step types in `dashboard/src/api/transform.ts`:

- `job` backend steps become `agent-task`, `pack-action`, or `tool-call` based on metadata
- step with `for_each` is rendered as `fan-out`
- unknown backend type is preserved in `config.backendType` and shown as `agent-task`

DAG node and detail coverage:

- Dedicated detail panels: `job`/`agent-task`, `approval`, `condition`, `delay`, `fan-out`, `parallel`, `switch`, `loop`, `transform`, `storage`, `subworkflow`
- Generic detail panel: `notify` and other types
- Icon mappings include `parallel`, `switch`, `loop`, `transform`, `storage`, `http`, `sub-workflow`

## 9. Cross-References

- [System Overview](./system_overview.md)
- [API Overview](./api.md)
- Workflow model: `core/workflow/models.go`
- Workflow engine: `core/workflow/engine.go`
- Expression evaluator: `core/workflow/eval.go`
- Dashboard DAG detail: `dashboard/src/components/workflows/dag/NodeDetailPanel.tsx`
- Dashboard run overlay: `dashboard/src/components/workflows/dag/RunOverlayNode.tsx`
