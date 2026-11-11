# Cordum platform threat model

This M0 security contract under `docs/contract/` states who Cordum defends against, what it trusts and what it does not promise across the platform, not only Edge. It distinguishes source-reviewed mechanisms from qualified protection; roadmap capabilities remain planned.

Reviewed commit: `d027ae97401c411fb62037a11b783700d1501cfe` (cordum origin/main, 2026-11-11).

Findings R01-R15 come from the roadmap review at `ebc0dd6a`; none is recorded fixed there, and repro cases are planned in rows M0-08 to M0-12. *(not verified)*

Claim-status vocabulary:

- `planned` (not built)
- `supported` (the feature exists, but a workload could bypass it or it is untested against bypass)
- `enforced` (an enforcement point exists that the workload cannot skip inside the declared boundary)
- `verified` (enforced, with passing negative tests at a pinned release)
- `unsupported` (explicitly not provided)

Statuses are code tokens, for example `supported`. No row uses the release-qualified status: no release has been qualified with negative tests by this review. Existing mechanisms are source-reviewed only; these tokens do not certify roadmap work or deployment operation.

A current-behavior statement cites a repository path at the reviewed commit as `path`, `path:N` or `path:N-M`, with symbol names outside the path backticks. A statement without a citation ends with *(not verified)*. Open findings are named by id only; this document gives neither descriptions nor trigger instructions.

Related documents (their scope is not an endorsement of every claim they contain):

- [Edge P0 threat model](../internal/security/edge-p0-threat-model.md): the historical Edge closure checklist.
- [Delegation](../auth/delegation.md): delegated authority, token scope and key rotation.
- [Policy signing](../deployment/policy-signing.md): policy signing modes, key distribution and rollout.
- [Audit deployment](../deployment/audit-chain.md): audit-chain operation, retention and incident handling.
- [Audit architecture](../audit-chain.md): chain representation, hashing and authentication.
- [MCP outbound signing](../mcp/outbound-signing.md): signatures and cooperating-peer trust.
- [MCP approval gates](../mcp/per-tool-approval.md): tool approval configuration and operator flows.
- [Agentd keychain](../security/agentd-keychain.md): credential bootstrap and service integration.
- [Binary signing](../security/binary-signing.md): release integrity and endpoint verification scope.
- [Classifier boundary](../edge/classifier-trust-boundary.md): classifier-owned labels versus request data.
- [Policy mechanism roadmap](../edge-074-policy-mechanism.md): planned separation of classification and resource mediation.

## Assets

Assets identify authority, data and evidence whose integrity or confidentiality matters.

| id | asset | why it matters | where it lives (cite) | protected by |
| --- | --- | --- | --- | --- |
| A1 | Authority to cause a protected effect | A decision matters only if the effect obeys it. | Job dispatch: `core/controlplane/scheduler/engine.go`; Edge: `core/controlplane/gateway/handlers_edge_evaluate.go`; MCP: `core/mcp/policy_evaluate.go`; workflow: `core/workflow/engine.go`. | These decision paths, not universal resource control; Kubernetes protected-operation definition is M0-02 (planned), broker authority is M2 (planned). |
| A2 | Policy content and identity | Determines the rules operators intend to apply. | `core/controlplane/safetykernel/`, `core/controlplane/safetykernel/file_loader.go`, `core/controlplane/gateway/handlers_policy_bundles.go`, `core/infra/config/safety_policy.go`. | Loader signature modes and Gateway policy permissions; policy infrastructure remains trusted. |
| A3 | Approvals and approver identity | A human decision must not become unrelated authority. | `core/controlplane/gateway/handlers_approvals.go`, `core/controlplane/gateway/mcp_approvals.go`, `core/edge/approval_store.go`. | Gateway authentication/permissions and approval stores; no end-to-end qualification is claimed. |
| A4 | Credentials and key material | Possession can confer identity or signing authority. | API keys/JWT: `core/controlplane/gateway/auth/`; worker credentials: `core/controlplane/scheduler/credential_cache.go`; delegation keys: `cmd/cordumctl/delegation_keygen.go`; endpoint nonce/keychain: `core/edge/keychain/`; TLS keys: `core/controlplane/gateway/gateway.go`; licenses: `core/licensing/`. | Credential holders, OS secret storage, configured transport and signing checks; not protection against a compromised holder. |
| A5 | Audit and evidence | Operators need attributable records, not merely successful API responses. | `core/audit/`, `core/edge/export.go`. | Audit-chain/export mechanisms and trusted storage; source existence is not completeness proof. |
| A6 | Tenant data and isolation | One tenant must not exercise another tenant's authority or read its data. | `core/controlplane/gateway/auth/rbac.go`, `core/controlplane/gateway/gateway.go`, `core/edge/export.go`. | Gateway tenant context, permission checks and tenant-scoped store operations. |
| A7 | Secrets in tool inputs, outputs and context | Accidental disclosure can create lasting external authority. | `core/edge/redaction.go`, `core/controlplane/safetykernel/scanners.go`, `core/contextwindow/engine/`. | Redaction/scanning mechanisms and access controls; arbitrary-content secrecy is not certified. |
| A8 | Decision-path availability | Outages must have explicit safety and recovery semantics. | `core/edge/agentd/fail_modes.go`. | Mode-dependent handling; the complete partition/failure matrix is M0-06 (planned). |

## Adversaries

Adversaries distinguish untrusted workloads from trusted infrastructure and administrators outside the promise.

Capabilities and goals below are threat assumptions, not reports of successful attacks. Model output is data, never authority: this is a requirement, not a claim of universal enforcement.

| id | adversary | assumed capabilities | goal | inside the boundary? | where Cordum meets it today (cite) |
| --- | --- | --- | --- | --- | --- |
| D1 | Untrusted agents | Choose tool calls and submit jobs using their granted credentials. | Cause an unauthorized effect. | Yes, on declared governed paths. | Edge evaluation and MCP calls: `core/controlplane/gateway/handlers_edge_evaluate.go`, `core/mcp/policy_evaluate.go`. |
| D2 | Repository scripts | Code invoked by build, test or other tools runs with the agent's privileges (threat assumption). | Turn allowed execution into undesired effects. | Yes as untrusted input; arbitrary execution confinement is planned in M3. | Classification rather than an OS sandbox: `core/edge/classifier.go`; design scope: `docs/edge-074-policy-mechanism.md`. |
| D3 | MCP plugins | Supply upstream tool descriptions, behavior and results. | Mislead the caller or misuse a tool invocation. | Yes for Gateway-fronted calls, not every direct connection. | `core/controlplane/gateway/mcp_upstream_serving.go`, `core/controlplane/gateway/handlers_mcp_upstreams.go`. |
| D4 | Model output | Propose commands, arguments and purported approvals. | Convert persuasive text into authority. | Yes as untrusted data. | Tool-call approval claims are treated as data in `core/mcp/policy_evaluate.go:109-114`; classifier boundary: `core/edge/policy_mapper.go`. |
| D5 | Compromised policy service | Control a trusted policy source or publication principal. | Change what the system authorizes. | A trusted-component compromise, not a tolerated workload adversary. | `core/controlplane/safetykernel/file_loader.go`, `core/controlplane/gateway/handlers_policy_bundles.go`. |
| D6 | Malicious tenant | Send hostile requests using its own tenant identity. | Cross tenant or privilege boundaries. | Yes. | Gateway middleware/RBAC and tenant-scoped Edge export: `core/controlplane/gateway/gateway.go`, `core/controlplane/gateway/auth/rbac.go`, `core/edge/export.go`. |
| D7 | Network attacker | Observe, inject, alter or interrupt traffic on reachable links. | Steal authority, alter messages or deny service. | Yes subject to transport/deployment conditions; availability under all attacks is not promised. | TLS: `core/controlplane/gateway/gateway.go`, `core/controlplane/safetykernel/kernel.go`; admission: `core/infra/bus/raw_admission.go`. |
| D8 | Endpoint administrator | Control the OS, binary files and managed configuration (threat assumption). | Replace or disable endpoint controls. | No, outside the boundary; see non-promise 1. | Endpoint trust assumptions: `docs/security/binary-signing.md`, `docs/security/agentd-keychain.md`. |

## Trust boundaries

Boundaries identify where data or authority changes trust domains.

| id | sides and crossing | present protection and limitation | status |
| --- | --- | --- | --- |
| TB1 | Agent process → Edge hook → agentd: action metadata and decisions. | Developer launcher children share the launching OS user's trust domain (`cmd/cordumctl/edge_claude.go`). Loopback validation and request nonce checks exist (`core/edge/agentd/config.go:225-248`, `core/edge/agentd/local_server.go:141-150`); keychain bootstrap and managed-settings generation exist (`core/edge/keychain/bootstrap.go`, `core/edge/claude/managed_settings.go`). These are not isolation from same-user interference or the endpoint administrator (`docs/security/agentd-keychain.md`, `docs/security/binary-signing.md`). | `supported` on a trusted endpoint. |
| TB2 | HTTP/gRPC clients → Gateway: identity, tenant, jobs and administrative requests. | HTTP logging/tracing, CORS, rate limiting, auth, read audit, tenant and body middleware (`core/controlplane/gateway/gateway.go:1236-1242`); gRPC auth/rate interceptors (`core/controlplane/gateway/gateway.go:990-1005`); RBAC (`core/controlplane/gateway/auth/rbac.go`). TLS conditions are in Deployment boundary. | `supported`. |
| TB3 | MCP clients → Gateway-hosted or standalone server; Gateway → upstream MCP servers: calls, metadata and results. | Gateway MCP authentication and opt-in action-gate wiring (`core/controlplane/gateway/handlers_mcp.go`, `core/controlplane/gateway/mcp_policy_wire.go`); upstream registration/use-time checks (`core/controlplane/gateway/handlers_mcp_upstreams.go`, `core/controlplane/gateway/mcp_upstream_serving.go`). Standalone server uses Gateway API clients and enables local scopes only with an agent id (`cmd/cordum-mcp/main.go:103-155`); it is not a separate authority boundary in an agent-controlled process. | `supported` for configured Gateway mechanisms; standalone boundary `unsupported`. |
| TB4 | Gateway/Scheduler/Workflow Engine → Safety Kernel gRPC: policy requests and decisions. | Server TLS is configured with certificate/key and required in production only (`core/controlplane/safetykernel/kernel.go:349-371`, `core/infra/env/env.go:33-40`). That TLS configuration has no client-certificate requirement, and the server construction adds no caller-auth interceptor (`core/controlplane/safetykernel/kernel.go:481-497`); trusted private reachability is required, not authenticated caller identity inferred from TLS. Workflow's output-safety client is wired in `cmd/cordum-workflow-engine/main.go`. | `supported` transport; caller-authentication boundary `unsupported` in this server wiring. |
| TB5 | Control plane → NATS → workers: CAP packets, credentials and results. | CAP handshake keys/mode checks (`cmd/cordum-scheduler/handshake_security_keys.go`, `cmd/cordum-scheduler/worker_security_modes.go`), worker attestation (`core/controlplane/scheduler/credential_cache.go`), raw admission (`core/infra/bus/raw_admission.go`) and shared replay storage (`core/infra/replay/redis_store.go`) exist. Deployment mode and broker access remain load-bearing. | `supported`. |
| TB6 | Services → Redis: policy, jobs, context, approvals and evidence. | Shared configuration reads one `REDIS_URL` (`core/infra/config/config.go:48-57`), loaded by Gateway, Safety Kernel, Scheduler, Workflow Engine and Context Engine (`cmd/cordum-api-gateway/main.go`, `cmd/cordum-safety-kernel/main.go`, `cmd/cordum-scheduler/main.go`, `cmd/cordum-workflow-engine/main.go`, `cmd/cordum-context-engine/main.go`). Per-service/per-key ACL separation in a deployed Redis is not established by this review. *(not verified)* | `supported` connection configuration; deployed privilege separation *(not verified)*. |
| TB7 | Tenant → tenant: identifiers, data and authority. | Tenant middleware and handler/store scoping are separate checks (`core/controlplane/gateway/gateway.go`, `core/controlplane/gateway/handlers_mcp_upstreams.go`, `core/edge/export.go`). Application checks do not substitute for trusted shared storage. | `supported`. |
| TB8 | Administrators → policy/configuration: publication, rules and keys. | Policy permissions and signature modes exist (`core/controlplane/gateway/handlers_policy_bundles.go`, `core/controlplane/gateway/auth/rbac.go`, `core/controlplane/safetykernel/file_loader.go`). A signature authenticates a trusted publisher; it does not make that publisher honest. | `supported`. |
| TB9 | Cordum → external systems: the actual protected effect. | Agents/workers are assumed to hold external credentials independently; no exhaustive credential-ownership inventory was measured. *(not verified)* Credential brokering is M2 (planned); universal resource mediation is outside the current claim. | `unsupported` universal mediation; broker `planned`. |

### Relationship to the Edge P0 threat model

The Edge closure checklist and this platform contract have different scopes and claim-status vocabularies.

## Trusted components

Classification follows whether a protection claim depends on the component deciding correctly or retaining a credential.

### Trusted-component inventory

The inventory records authority, compromise consequences and protection status.

### Explicit untrusted list

Clients and workload-controlled components require re-checks at trusted boundaries.

## Reference-monitor properties

These properties assess mediation, tamper resistance and analyzability without assuming deployment qualification.

## Non-promises

These exclusions bound the security promise rather than weakening an implicit universal guarantee.

## Deployment boundary

Deployment conditions limit which protection claims can eventually be qualified.
