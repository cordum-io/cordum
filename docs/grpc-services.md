# gRPC Services Reference

This document is the gRPC reference for Cordum control plane services.

Source of truth:

- `core/protocol/proto/v1/api.proto`
- `core/protocol/proto/v1/context.proto`
- `core/protocol/proto/v1/output_policy.proto`
- `core/protocol/pb/v1/pb.go` (re-exports SafetyKernel from CAP module)
- `core/controlplane/scheduler/safety_client.go`
- `core/controlplane/scheduler/output_safety_client.go`
- `core/controlplane/safetykernel/kernel.go`

## Service Inventory

| Service | Package | RPC methods | Proto source |
| --- | --- | --- | --- |
| `CordumApi` | `cordum.v1` | `SubmitJob`, `GetJobStatus` | `core/protocol/proto/v1/api.proto` |
| `ContextEngine` | `cordum.v1` | `BuildWindow`, `UpdateMemory` | `core/protocol/proto/v1/context.proto` |
| `OutputPolicyService` | `cordum.v1` | `CheckOutput` | `core/protocol/proto/v1/output_policy.proto` |
| `SafetyKernel` | `cordum.agent.v1` | `Check`, `Evaluate`, `Explain`, `Simulate`, `ListSnapshots` | Re-exported via `core/protocol/pb/v1/pb.go` from `github.com/cordum-io/cap/v2/cordum/agent/v1` |
| `Health` | `grpc.health.v1` | `Check`, `Watch` | Standard gRPC health protocol |

Streaming coverage:

- `grpc.health.v1.Health/Watch` is server-streaming.
- All other RPCs above are unary.

## Connection and TLS

### Runtime endpoints and TLS env

| Service | Default listen addr | Server TLS env | Client TLS env |
| --- | --- | --- | --- |
| `CordumApi` (API gateway gRPC) | `:8080` | `GRPC_TLS_CERT`, `GRPC_TLS_KEY` | n/a (client-specific) |
| `SafetyKernel` + `OutputPolicyService` | `localhost:50051` | `SAFETY_KERNEL_TLS_CERT`, `SAFETY_KERNEL_TLS_KEY` | `SAFETY_KERNEL_TLS_CA`, `SAFETY_KERNEL_TLS_REQUIRED`, `SAFETY_KERNEL_INSECURE` |
| `ContextEngine` | `:50070` | `CONTEXT_ENGINE_TLS_CERT`, `CONTEXT_ENGINE_TLS_KEY` | `CONTEXT_ENGINE_TLS_CA`, `CONTEXT_ENGINE_TLS_REQUIRED`, `CONTEXT_ENGINE_INSECURE` |

Production behavior from code:

- API gateway gRPC: TLS is required in production.
- Safety kernel: TLS is required in production.
- Context engine: TLS is required in production.

### Example env config (YAML)

```yaml
services:
  cordum-api-gateway:
    environment:
      GATEWAY_GRPC_ADDR: ":8080"
      GRPC_TLS_CERT: "/certs/gateway.crt"
      GRPC_TLS_KEY: "/certs/gateway.key"

  cordum-safety-kernel:
    environment:
      SAFETY_KERNEL_ADDR: "0.0.0.0:50051"
      SAFETY_KERNEL_TLS_CERT: "/certs/safety.crt"
      SAFETY_KERNEL_TLS_KEY: "/certs/safety.key"

  cordum-scheduler:
    environment:
      SAFETY_KERNEL_ADDR: "cordum-safety-kernel:50051"
      SAFETY_KERNEL_TLS_CA: "/certs/ca.crt"
      SAFETY_KERNEL_TLS_REQUIRED: "true"
      SAFETY_KERNEL_INSECURE: "false"

  cordum-context-engine:
    environment:
      CONTEXT_ENGINE_ADDR: ":50070"
      CONTEXT_ENGINE_TLS_CERT: "/certs/context.crt"
      CONTEXT_ENGINE_TLS_KEY: "/certs/context.key"
```

## CordumApi (`cordum.v1`)

RPC signatures:

```proto
rpc SubmitJob(SubmitJobRequest) returns (SubmitJobResponse);
rpc GetJobStatus(GetJobStatusRequest) returns (GetJobStatusResponse);
```

### SubmitJobRequest fields

| Field | Type | Notes |
| --- | --- | --- |
| `prompt` | `string` | Required job prompt payload. |
| `topic` | `string` | Job topic, for example `job.default`. |
| `adapter_id` | `string` | Adapter identifier. |
| `priority` | `string` | Priority string (`interactive`, `batch`, `critical`). |
| `org_id` | `string` | Tenant/org identifier for isolation. |
| `team_id` | `string` | Team scope. |
| `project_id` | `string` | Project scope. |
| `principal_id` | `string` | Principal identity. |
| `idempotency_key` | `string` | Client idempotency key. |
| `actor_id` | `string` | Actor id metadata. |
| `actor_type` | `string` | Actor type (`human`, `service`). |
| `pack_id` | `string` | Pack id metadata. |
| `capability` | `string` | Capability metadata. |
| `risk_tags` | `repeated string` | Risk tags metadata. |
| `requires` | `repeated string` | Required capabilities/dependencies. |
| `labels` | `map<string,string>` | Arbitrary labels. |
| `memory_id` | `string` | Context memory id. |

### SubmitJobResponse fields

| Field | Type | Notes |
| --- | --- | --- |
| `job_id` | `string` | Created or deduplicated job id. |
| `trace_id` | `string` | Trace id for job lineage. |

### GetJobStatus request/response

| Message | Field | Type | Notes |
| --- | --- | --- | --- |
| `GetJobStatusRequest` | `job_id` | `string` | Job id lookup key. |
| `GetJobStatusResponse` | `job_id` | `string` | Echoed job id. |
| `GetJobStatusResponse` | `status` | `string` | Job state string. |
| `GetJobStatusResponse` | `result_ptr` | `string` | Result pointer (`redis://...`) when present. |

### Error behavior

Common status codes from `core/controlplane/gateway/gateway_grpc.go`:

- `InvalidArgument`: invalid request fields
- `PermissionDenied`: tenant access violations
- `AlreadyExists`: idempotency key conflict
- `ResourceExhausted`: backpressure/rate limits
- `Unavailable`: backing store or bus unavailable
- `Internal`: internal idempotency/memory policy failures

### grpcurl example

```bash
grpcurl -plaintext \
  -H "x-api-key: ${CORDUM_API_KEY}" \
  -d '{"prompt":"hello","topic":"job.default","org_id":"default"}' \
  localhost:8080 cordum.v1.CordumApi/SubmitJob
```

## ContextEngine (`cordum.v1`)

RPC signatures:

```proto
rpc BuildWindow(BuildWindowRequest) returns (BuildWindowResponse);
rpc UpdateMemory(UpdateMemoryRequest) returns (UpdateMemoryResponse);
```

### ContextMode enum

- `CONTEXT_MODE_UNSPECIFIED = 0`
- `CONTEXT_MODE_RAW = 1`
- `CONTEXT_MODE_CHAT = 2`
- `CONTEXT_MODE_RAG = 3`

### BuildWindowRequest fields

| Field | Type | Notes |
| --- | --- | --- |
| `memory_id` | `string` | Memory partition key. |
| `mode` | `ContextMode` | RAW/CHAT/RAG behavior. |
| `model` | `string` | Target model name (advisory). |
| `logical_payload` | `bytes` | Input payload blob for context extraction. |
| `max_input_tokens` | `int32` | Input budget hint. |
| `max_output_tokens` | `int32` | Output budget hint. |

### BuildWindowResponse fields

| Field | Type | Notes |
| --- | --- | --- |
| `messages` | `repeated ModelMessage` | Model-ready prompt messages. |
| `input_tokens` | `int32` | Estimated input token usage. |
| `output_tokens` | `int32` | Suggested output token budget. |

`ModelMessage`:

| Field | Type |
| --- | --- |
| `role` | `string` |
| `content` | `string` |

### UpdateMemoryRequest/Response

| Field | Type | Notes |
| --- | --- | --- |
| `memory_id` | `string` | Memory partition key. |
| `logical_payload` | `bytes` | Input payload. |
| `model_response` | `bytes` | Model output payload. |
| `mode` | `ContextMode` | Memory behavior mode. |

`UpdateMemoryResponse` is empty.

### grpcurl example

```bash
grpcurl -plaintext \
  -d '{"memory_id":"m-123","mode":"CONTEXT_MODE_CHAT","logical_payload":"eyJwcm9tcHQiOiJoZWxsbyJ9"}' \
  localhost:50070 cordum.v1.ContextEngine/BuildWindow
```

## OutputPolicyService (`cordum.v1`)

RPC signature:

```proto
rpc CheckOutput(OutputCheckRequest) returns (OutputCheckResponse);
```

This service is hosted by the Safety Kernel process (`pb.RegisterOutputPolicyServiceServer` in `core/controlplane/safetykernel/kernel.go`).

### OutputCheckRequest fields

| Field | Type |
| --- | --- |
| `job_id` | `string` |
| `topic` | `string` |
| `tenant` | `string` |
| `labels` | `map<string,string>` |
| `result_ptr` | `string` |
| `artifact_ptrs` | `repeated string` |
| `error_message` | `string` |
| `error_code` | `string` |
| `worker_id` | `string` |
| `execution_ms` | `int64` |
| `output_size_bytes` | `int64` |
| `content_hash` | `string` |
| `workflow_id` | `string` |
| `step_id` | `string` |
| `output_content` | `bytes` |
| `capabilities` | `repeated string` |
| `risk_tags` | `repeated string` |
| `principal_id` | `string` |
| `pack_id` | `string` |
| `content_type` | `string` |
| `original_labels` | `map<string,string>` |

### OutputCheckResponse fields

| Field | Type | Notes |
| --- | --- | --- |
| `decision` | `OutputDecision` | `ALLOW`, `QUARANTINE`, `REDACT`. |
| `reason` | `string` | Rule reason or scanner reason. |
| `rule_id` | `string` | Matched output rule id. |
| `policy_snapshot` | `string` | Active policy snapshot id/hash. |
| `findings` | `repeated OutputFinding` | Scanner findings. |
| `redacted_ptr` | `string` | Pointer to sanitized output when available. |

### OutputDecision enum

- `OUTPUT_DECISION_ALLOW = 0`
- `OUTPUT_DECISION_QUARANTINE = 1`
- `OUTPUT_DECISION_REDACT = 2`

### OutputFinding fields

| Field | Type |
| --- | --- |
| `type` | `string` |
| `severity` | `string` |
| `detail` | `string` |
| `offset` | `int64` |
| `length` | `int64` |
| `scanner` | `string` |
| `confidence` | `float` |
| `matched_pattern` | `string` |

### grpcurl example

```bash
grpcurl -plaintext \
  -d '{"job_id":"job-1","topic":"job.default","tenant":"default","output_content":"c2VjcmV0Cg=="}' \
  localhost:50051 cordum.v1.OutputPolicyService/CheckOutput
```

## SafetyKernel (`cordum.agent.v1`)

SafetyKernel is re-exported in this repository via `core/protocol/pb/v1/pb.go`.  
Underlying generated source is from CAP module `github.com/cordum-io/cap/v2/cordum/agent/v1` (`safety.proto`).

RPC signatures:

```proto
rpc Check(PolicyCheckRequest) returns (PolicyCheckResponse);
rpc Evaluate(PolicyCheckRequest) returns (PolicyCheckResponse);
rpc Explain(PolicyCheckRequest) returns (PolicyCheckResponse);
rpc Simulate(PolicyCheckRequest) returns (PolicyCheckResponse);
rpc ListSnapshots(ListSnapshotsRequest) returns (ListSnapshotsResponse);
```

### PolicyCheckRequest fields

| Field | Type | Notes |
| --- | --- | --- |
| `job_id` | `string` | Job id. |
| `topic` | `string` | Job topic. |
| `tenant` | `string` | Tenant id. |
| `priority` | `JobPriority` | Scheduling priority. |
| `estimated_cost` | `double` | Optional cost hint. |
| `budget` | `Budget` | Optional execution budget. |
| `principal_id` | `string` | Principal id. |
| `labels` | `map<string,string>` | Policy label context. |
| `memory_id` | `string` | Context memory key. |
| `effective_config` | `bytes` | Marshaled effective config JSON. |
| `meta` | `JobMetadata` | Structured actor/capability metadata. |

### PolicyCheckResponse fields

| Field | Type | Notes |
| --- | --- | --- |
| `decision` | `DecisionType` | Policy decision enum. |
| `reason` | `string` | Decision reason. |
| `redacted_context_ptr` | `string` | Optional sanitized context pointer. |
| `policy_snapshot` | `string` | Policy snapshot id/hash. |
| `rule_id` | `string` | Matched rule id. |
| `constraints` | `PolicyConstraints` | Optional execution constraints. |
| `approval_required` | `bool` | Human approval required. |
| `approval_ref` | `string` | Approval correlation ref. |
| `remediations` | `repeated PolicyRemediation` | Suggested alternative actions. |

### DecisionType enum

- `DECISION_TYPE_UNSPECIFIED`
- `DECISION_TYPE_ALLOW`
- `DECISION_TYPE_DENY`
- `DECISION_TYPE_REQUIRE_HUMAN`
- `DECISION_TYPE_THROTTLE`
- `DECISION_TYPE_ALLOW_WITH_CONSTRAINTS`

### grpcurl example

```bash
grpcurl -plaintext \
  -d '{"job_id":"job-1","topic":"job.default","tenant":"default"}' \
  localhost:50051 cordum.agent.v1.SafetyKernel/Check
```

## Health Service (`grpc.health.v1`)

Registered in:

- `core/controlplane/safetykernel/kernel.go`
- `cmd/cordum-context-engine/main.go`

RPC signatures:

```proto
rpc Check(HealthCheckRequest) returns (HealthCheckResponse);
rpc Watch(HealthCheckRequest) returns (stream HealthCheckResponse);
```

Health RPCs are treated as public in gateway gRPC rate limiting logic (`/grpc.health.v1.Health/Check`, `/grpc.health.v1.Health/Watch`).

Example:

```bash
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
```

## Scheduler Client Timeouts and Circuit Breakers

### Safety client (`core/controlplane/scheduler/safety_client.go`)

- Request timeout: `2s`
- Open duration: `30s`
- Failures to open: `3`
- Half-open max probes: `3`
- Half-open successes to close: `2`

State machine:

```text
CLOSED --(3 failures)--> OPEN --(30s elapsed)--> HALF_OPEN
HALF_OPEN --(2 successes)--> CLOSED
HALF_OPEN --(failure)------> OPEN
```

When Safety RPC fails, scheduler records `SafetyUnavailable` (decision fallback) instead of hard-failing scheduling.

### Output safety client (`core/controlplane/scheduler/output_safety_client.go`)

- Metadata check timeout: `100ms`
- Content check timeout: `30s`
- Content cap: `2 MiB`
- Circuit breaker constants mirror Safety client (`3` failures, `30s` open, half-open probes `3`, close after `2` successes)

If output checks fail, scheduler currently treats that as skipped/fail-open for output policy evaluation path.

## Regenerating Protobuf Code

Cordum local proto generation:

```bash
make proto
```

Current `Makefile` `proto` target generates code for local files:

- `api.proto`
- `context.proto`
- `output_policy.proto`

SafetyKernel proto is sourced from CAP module and re-exported; updating those definitions requires updating the module version and generated CAP artifacts.

## Cross-References

- [Safety Kernel Reference](./safety-kernel.md)
- [Output Policy Operator Guide](./output-policy.md)
- [API Reference](./api-reference.md)
